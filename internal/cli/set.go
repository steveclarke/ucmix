package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/steveclarke/ucmix/internal/errs"
	"github.com/steveclarke/ucmix/internal/schema"
	"github.com/steveclarke/ucmix/internal/ui"
)

// newSetCmd builds `set <path> <value>`: parse the value (units, booleans,
// strings, hex color) against the schema, then write it via the client, which
// picks the wire encoding and taper. A single write, no confirmation. Dotted
// paths are translated to the wire form.
func newSetCmd(g *globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <path> <value>",
		Short: "Write one mixer value",
		Long: "Write one mixer value.\n\n" +
			"Paths use slashes (line/ch1/volume) or dots (line.ch1.volume).\n\n" +
			"Value forms: on/off, true/false; numbers with an optional unit " +
			"(-6dB, 100Hz, 400ms, 0.5); a quoted string for names; and hex for " +
			"color (4ed2ff or #4ed2ff).",
		Example: `  ucmix set line/ch1/volume -6dB
  ucmix set line/ch1/username "Kick"
  ucmix set line/ch1/48v on`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := normalizePath(args[0])
			spec, known := schema.Lookup(path)
			value, err := parseSetValue(spec, known, args[1])
			if err != nil {
				return errs.CLIError{
					Message: fmt.Sprintf("invalid value for %s: %v", path, err),
					Hint:    "see `ucmix set --help` for accepted value forms",
				}
			}

			c, err := g.dialClient(cmd.Context())
			if err != nil {
				return err
			}
			defer func() { _ = c.Close() }()

			if err := c.Set(cmd.Context(), path, value); err != nil {
				return errs.CLIError{
					Message: fmt.Sprintf("could not set %s: %v", path, err),
					Hint:    "check the value is in range for this control",
				}
			}

			if g.json {
				return printJSON(map[string]any{"path": path, "value": jsonValue(value), "ok": true})
			}
			fmt.Println(ui.Success(fmt.Sprintf("set %s = %s", path, displayValue(value))))
			return nil
		},
	}
	// A value like -6dB otherwise looks like a flag to the parser. Stop
	// interspersing flags with positionals so the value is taken verbatim;
	// global flags still work before the positionals or at the root
	// (e.g. `ucmix --json set line.ch1.volume -6dB`).
	cmd.Flags().SetInterspersed(false)
	return cmd
}
