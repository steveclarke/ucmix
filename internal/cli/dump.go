package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveclarke/ucmix/internal/boardconfig"
	"github.com/steveclarke/ucmix/internal/errs"
	"github.com/steveclarke/ucmix/internal/ui"

	"gopkg.in/yaml.v3"
)

// newDumpCmd builds `dump [prefix] [--raw] [--as-config]`: connect, take a full
// snapshot, and print it. By default every path→value is printed sorted,
// humanized through the schema (--raw shows wire values, --json a path→value
// object). --as-config instead reconstructs the declarative board config (the
// inverse of Compile for modeled fields) and prints it as YAML.
func newDumpCmd(g *globals) *cobra.Command {
	var raw, asConfig bool
	cmd := &cobra.Command{
		Use:   "dump [prefix]",
		Short: "Print every mixer path and value (or --as-config as YAML)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := g.dialClient(cmd.Context())
			if err != nil {
				return err
			}
			defer func() { _ = c.Close() }()

			if asConfig {
				return dumpAsConfig(c.Snapshot())
			}

			prefix := ""
			if len(args) == 1 {
				prefix = normalizePath(args[0])
			}

			snap := c.Snapshot()
			paths := make([]string, 0, len(snap))
			for p := range snap {
				if prefix == "" || strings.HasPrefix(p, prefix) {
					paths = append(paths, p)
				}
			}
			sort.Strings(paths)

			values := make(map[string]any, len(paths))
			for _, p := range paths {
				if raw {
					values[p], _ = c.GetRaw(p)
				} else {
					values[p], _ = c.Get(p)
				}
			}

			if g.json {
				jsonValues := make(map[string]any, len(values))
				for p, v := range values {
					jsonValues[p] = jsonValue(v)
				}
				return printJSON(jsonValues)
			}
			display := make(map[string]string, len(values))
			for p, v := range values {
				display[p] = displayValue(v)
			}
			if len(display) == 0 {
				fmt.Println(ui.Hint("(no matching paths)"))
				return nil
			}
			fmt.Println(ui.SortedTable(display))
			return nil
		},
	}
	cmd.Flags().BoolVar(&raw, "raw", false, "show raw wire values (skip humanizing)")
	cmd.Flags().BoolVar(&asConfig, "as-config", false, "print the board as a declarative YAML config")
	return cmd
}

// dumpAsConfig reconstructs the declarative config from a raw wire snapshot and
// prints it as YAML — the inverse of Compile over modeled fields. Unmodeled keys
// are dropped (a config is a statement of modeled intent, not a full dump).
func dumpAsConfig(snapshot map[string]any) error {
	cfg, err := boardconfig.ToConfig(snapshot)
	if err != nil {
		return errs.CLIError{Message: fmt.Sprintf("could not build config from snapshot: %v", err)}
	}
	out, err := yaml.Marshal(cfg)
	if err != nil {
		return errs.CLIError{Message: fmt.Sprintf("could not marshal config: %v", err)}
	}
	_, err = os.Stdout.Write(out)
	return err
}
