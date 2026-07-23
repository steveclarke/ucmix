package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/steveclarke/ucmix/internal/errs"
	"github.com/steveclarke/ucmix/internal/ui"
)

// newGetCmd builds `get <path> [--raw]`: read one value, humanized through the
// schema by default (--raw for the wire value). --json emits an envelope with
// the path, value, and whether it was raw. Dotted paths are translated to the
// wire form.
func newGetCmd(g *globals) *cobra.Command {
	var raw bool
	cmd := &cobra.Command{
		Use:     "get <path>",
		Short:   "Read one mixer value",
		Example: "  ucmix get line/ch1/volume       # slashes or dots: line.ch1.volume also works",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := normalizePath(args[0])
			c, err := g.dialClient(cmd.Context())
			if err != nil {
				return err
			}
			defer func() { _ = c.Close() }()

			var value any
			var ok bool
			if raw {
				value, ok = c.GetRaw(path)
			} else {
				value, ok = c.Get(path)
			}
			if !ok {
				return errs.CLIError{
					Message: fmt.Sprintf("path not found: %s", path),
					Hint:    "run `ucmix dump` to see the paths the mixer exposes",
				}
			}

			if g.json {
				return printJSON(map[string]any{"path": path, "value": jsonValue(value), "raw": raw})
			}
			fmt.Println(ui.KeyValue(path, displayValue(value)))
			return nil
		},
	}
	cmd.Flags().BoolVar(&raw, "raw", false, "show the raw wire value (skip humanizing)")
	return cmd
}
