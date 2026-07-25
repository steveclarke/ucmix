package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveclarke/ucmix/internal/errs"
	"github.com/steveclarke/ucmix/internal/schema"
	"github.com/steveclarke/ucmix/internal/ui"

	ucmix "github.com/steveclarke/ucmix"
)

// setItem is one parsed write: the wire path, the value to send, and the raw
// literal (kept for the output line and JSON).
type setItem struct {
	path  string
	value any
	raw   string
}

// newSetCmd builds `set`: write one or many mixer values over a single held-open
// connection.
//
//	set <path> <value>          one write (line/ch1/volume -6dB)
//	set <p=v> [<p=v> ...]       several writes (line/ch1/mute=on line/ch1/48v=on)
//	set -f <file>               a write per "path value" line in a file
//
// Every form reuses one connection and holds a single commit barrier (in the
// library) so the board commits the burst before close. Values parse the same
// way in all forms: units, booleans, strings, hex color. Dotted paths translate
// to the wire form.
func newSetCmd(g *globals) *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "set <path> <value> | <p=v>... | -f <file>",
		Short: "Write one or many mixer values",
		Long: "Write one or many mixer values over a single connection.\n\n" +
			"Forms:\n" +
			"  set <path> <value>       one write\n" +
			"  set <p=v> [<p=v> ...]    several writes (each key=value)\n" +
			"  set -f <file>            a write per `path value` line in a file\n\n" +
			"Paths use slashes (line/ch1/volume) or dots (line.ch1.volume).\n\n" +
			"Value forms: on/off, true/false; numbers with an optional unit " +
			"(-6dB, 100Hz, 400ms, 0.5); a quoted string for names; and hex for " +
			"color (4ed2ff or #4ed2ff).",
		Example: `  ucmix set line/ch1/volume -6dB
  ucmix set line/ch1/username "Kick"
  ucmix set line/ch1/mute=on line/ch1/48v=on
  ucmix set -f board.txt`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			items, err := collectSetItems(file, args)
			if err != nil {
				return err
			}

			c, err := g.dialClient(cmd.Context())
			if err != nil {
				return err
			}

			settings := make([]ucmix.Setting, len(items))
			for i, it := range items {
				settings[i] = ucmix.Setting{Path: it.path, Value: it.value}
			}
			// SetMany streams every write over the one connection and holds the
			// commit barrier before returning, so closing does not race delivery.
			werr := c.SetMany(cmd.Context(), settings)
			// The read-back needs a fresh connection, so this one closes first.
			_ = c.Close()
			if werr != nil {
				return errs.CLIError{
					Message: fmt.Sprintf("could not write settings: %v", werr),
					Hint:    "check each value is in range for its control",
				}
			}

			return reportWrites(cmd.Context(), g, items)
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "read `path value` writes from a file (- for stdin)")
	addNoVerifyFlag(cmd, g)
	// A value like -6dB otherwise looks like a flag to the parser. Stop
	// interspersing flags with positionals so the value is taken verbatim;
	// global flags still work before the positionals or at the root
	// (e.g. `ucmix --json set line.ch1.volume -6dB`).
	cmd.Flags().SetInterspersed(false)
	return cmd
}

// collectSetItems resolves the command form (file, single path/value, or a list
// of key=value pairs) into parsed writes. The single `path value` form is kept
// for back-compat: exactly two positionals whose first has no '='.
func collectSetItems(file string, args []string) ([]setItem, error) {
	if file != "" {
		if len(args) > 0 {
			return nil, errs.CLIError{
				Message: "set -f takes writes from the file, not positional arguments",
				Hint:    "drop the positional arguments, or omit -f",
			}
		}
		return readSetFile(file)
	}

	if len(args) == 2 && !strings.Contains(args[0], "=") {
		it, err := parseSetItem(args[0], args[1])
		if err != nil {
			return nil, err
		}
		return []setItem{it}, nil
	}

	if len(args) == 0 {
		return nil, errs.CLIError{
			Message: "set needs a path and value, key=value pairs, or -f <file>",
			Hint:    "e.g. `ucmix set line/ch1/mute on` or `ucmix set -f board.txt`",
		}
	}

	items := make([]setItem, 0, len(args))
	for _, a := range args {
		path, raw, ok := strings.Cut(a, "=")
		if !ok {
			return nil, errs.CLIError{
				Message: fmt.Sprintf("expected key=value, got %q", a),
				Hint:    "batch writes are `path=value` pairs; use `set <path> <value>` for one write",
			}
		}
		it, err := parseSetItem(path, raw)
		if err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, nil
}

// readSetFile parses a `path value` write per non-blank, non-comment line. A
// file name of "-" reads stdin. The path is the first whitespace-delimited
// field; the rest of the line is the value (so names may contain spaces).
func readSetFile(name string) ([]setItem, error) {
	f := os.Stdin
	if name != "-" {
		opened, err := os.Open(name)
		if err != nil {
			return nil, errs.CLIError{
				Message: fmt.Sprintf("could not read %s: %v", name, err),
				Hint:    "check the path to the writes file",
			}
		}
		defer func() { _ = opened.Close() }()
		f = opened
	}

	var items []setItem
	sc := bufio.NewScanner(f)
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		path, raw, ok := strings.Cut(text, " ")
		if !ok {
			path, raw, ok = strings.Cut(text, "\t")
		}
		if !ok {
			return nil, errs.CLIError{
				Message: fmt.Sprintf("%s:%d: expected `path value`, got %q", name, line, text),
				Hint:    "each line is a path and a value separated by whitespace",
			}
		}
		it, err := parseSetItem(path, strings.TrimSpace(raw))
		if err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	if err := sc.Err(); err != nil {
		return nil, errs.CLIError{Message: fmt.Sprintf("could not read %s: %v", name, err)}
	}
	if len(items) == 0 {
		return nil, errs.CLIError{
			Message: fmt.Sprintf("no writes found in %s", name),
			Hint:    "add `path value` lines (blank lines and # comments are ignored)",
		}
	}
	return items, nil
}

// parseSetItem normalizes a path and parses its value against the schema.
func parseSetItem(rawPath, rawValue string) (setItem, error) {
	path := normalizePath(rawPath)
	spec, known := schema.Lookup(path)
	value, err := parseSetValue(spec, known, rawValue)
	if err != nil {
		return setItem{}, errs.CLIError{
			Message: fmt.Sprintf("invalid value for %s: %v", path, err),
			Hint:    "see `ucmix set --help` for accepted value forms",
		}
	}
	return setItem{path: path, value: value, raw: rawValue}, nil
}

// reportSet prints what the board holds after a write: one line for a single
// write, a per-path table for a batch, or a JSON envelope under --json. A write
// the board did not take names both values and exits 1 — the code verify and
// apply already use for a board that does not match what was asked of it.
func reportSet(g *globals, results []writeResult) error {
	missed := 0
	for _, r := range results {
		if !r.ok {
			missed++
		}
	}
	if g.json {
		if err := printSetJSON(results, missed); err != nil {
			return err
		}
	} else {
		printSetStyled(results, missed)
	}
	if missed > 0 {
		return &exitError{code: 1}
	}
	return nil
}

func printSetJSON(results []writeResult, missed int) error {
	if len(results) == 1 {
		return printJSON(setJSONRow(results[0]))
	}
	rows := make([]map[string]any, len(results))
	for i, r := range results {
		rows[i] = setJSONRow(r)
	}
	return printJSON(map[string]any{
		"written":  len(results),
		"settings": rows,
		"ok":       missed == 0,
	})
}

// setJSONRow renders one write for the JSON envelope. `got` is what the board
// holds; it is omitted when no read-back ran (--no-verify).
func setJSONRow(r writeResult) map[string]any {
	row := map[string]any{"path": r.path, "value": jsonValue(r.value), "ok": r.ok}
	if r.verified {
		row["got"] = jsonValue(humanize(r.path, r.got))
	}
	return row
}

func printSetStyled(results []writeResult, missed int) {
	if len(results) == 1 {
		r := results[0]
		if r.ok {
			fmt.Println(ui.Success(fmt.Sprintf("set %s = %s", r.path, displayValue(r.value))))
			return
		}
		fmt.Println(ui.ErrorLine(fmt.Sprintf("set %s: %s", r.path, heldValue(r))))
		return
	}
	rows := make([][2]string, len(results))
	for i, r := range results {
		if r.ok {
			rows[i] = [2]string{r.path, displayValue(r.value)}
			continue
		}
		rows[i] = [2]string{r.path, heldValue(r)}
	}
	if missed == 0 {
		fmt.Println(ui.Success(fmt.Sprintf("set %d values", len(results))))
	} else {
		fmt.Println(ui.ErrorLine(fmt.Sprintf("set %d values; %d did not take", len(results), missed)))
	}
	fmt.Println(ui.Table(rows))
}

// heldValue describes a write the board did not take: what was sent and what it
// holds instead.
func heldValue(r writeResult) string {
	if !r.found {
		return fmt.Sprintf("wrote %s, board holds nothing (path absent)", displayValue(r.value))
	}
	return fmt.Sprintf("wrote %s, board holds %s", displayValue(r.value), displayValue(humanize(r.path, r.got)))
}
