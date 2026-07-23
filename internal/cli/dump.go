package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveclarke/ucmix/internal/ui"
)

// newDumpCmd builds `dump [prefix] [--raw]`: connect, take a full snapshot, and
// print every path→value sorted. Values are humanized through the schema by
// default; --raw shows the wire values. --json emits a path→value object.
func newDumpCmd(g *globals) *cobra.Command {
	var raw bool
	cmd := &cobra.Command{
		Use:   "dump [prefix]",
		Short: "Print every mixer path and value",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := g.dialClient(cmd.Context())
			if err != nil {
				return err
			}
			defer func() { _ = c.Close() }()

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
				return printJSON(values)
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
	return cmd
}
