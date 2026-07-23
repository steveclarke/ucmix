package cli

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
	"github.com/steveclarke/ucmix/internal/config"
	"github.com/steveclarke/ucmix/internal/errs"
)

// newConfigCmd builds the `config` command group for locating and editing the
// config file.
func newConfigCmd(g *globals) *cobra.Command {
	c := &cobra.Command{
		Use:   "config",
		Short: "Show or edit the ucmix config file",
	}
	c.AddCommand(newConfigPathCmd(g), newConfigEditCmd(g))
	return c
}

// newConfigPathCmd builds `config path`: print the config file location.
func newConfigPathCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print the config file path",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			path := config.File{}.Path()
			if g.json {
				return printJSON(map[string]any{"path": path})
			}
			fmt.Println(path)
			return nil
		},
	}
}

// newConfigEditCmd builds `config edit`: open the config file in $EDITOR.
func newConfigEditCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "edit",
		Short: "Open the config file in $EDITOR",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			editor := os.Getenv("EDITOR")
			if editor == "" {
				editor = os.Getenv("VISUAL")
			}
			if editor == "" {
				return errs.CLIError{
					Message: "no editor configured",
					Hint:    "set $EDITOR (or $VISUAL) to your editor",
				}
			}
			path := config.File{}.Path()
			c := exec.Command(editor, path) //nolint:gosec // editor from env is intentional
			c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
			if err := c.Run(); err != nil {
				return errs.CLIError{Message: fmt.Sprintf("editor exited with error: %v", err)}
			}
			return nil
		},
	}
}
