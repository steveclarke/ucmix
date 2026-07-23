package cli

import (
	"fmt"
	"os"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
	"github.com/steveclarke/ucmix/internal/errs"
	"github.com/steveclarke/ucmix/internal/ui"

	ucmix "github.com/steveclarke/ucmix"
)

// newRecallCmd builds `recall <project> <scene>`: recall a stored scene. The
// board replies with a fresh snapshot the client loads.
func newRecallCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:     "recall <project> <scene>",
		Short:   "Recall a stored scene",
		Example: `  ucmix recall "Main Live" "Opening"`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			project, scene := args[0], args[1]
			c, err := g.dialClient(cmd.Context())
			if err != nil {
				return err
			}
			defer func() { _ = c.Close() }()

			if err := c.RecallScene(cmd.Context(), project, scene); err != nil {
				return errs.CLIError{Message: fmt.Sprintf("recall failed: %v", err)}
			}
			if g.json {
				return printJSON(map[string]any{"action": "recall", "project": project, "scene": scene, "ok": true})
			}
			fmt.Println(ui.Success(fmt.Sprintf("recalled %s / %s", project, scene)))
			return nil
		},
	}
}

// newStoreCmd builds `store <project> <scene>`: store the current mixer state as
// a scene under (project, scene).
func newStoreCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:     "store <project> <scene>",
		Short:   "Store the current state as a scene",
		Example: `  ucmix store "Main Live" "Opening"`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			project, scene := args[0], args[1]
			c, err := g.dialClient(cmd.Context())
			if err != nil {
				return err
			}
			defer func() { _ = c.Close() }()

			if err := c.StoreScene(cmd.Context(), project, scene); err != nil {
				return errs.CLIError{Message: fmt.Sprintf("store failed: %v", err)}
			}
			if g.json {
				return printJSON(map[string]any{"action": "store", "project": project, "scene": scene, "ok": true})
			}
			fmt.Println(ui.Success(fmt.Sprintf("stored %s / %s", project, scene)))
			return nil
		},
	}
}

// newResetCmd builds `reset [--scene] [--project] [--yes]`: reset the mixer to
// factory defaults. DESTRUCTIVE — it requires --yes or an interactive
// confirmation, and refuses in a non-tty without --yes. With neither --scene nor
// --project, both are reset (a full factory reset).
func newResetCmd(g *globals) *cobra.Command {
	var scene, project, yes bool
	cmd := &cobra.Command{
		Use:   "reset",
		Short: "Reset the mixer to factory defaults (destructive)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			scope := ucmix.ResetScope{Scene: scene, Project: project}
			if !scene && !project {
				scope = ucmix.ResetScope{Scene: true, Project: true}
			}

			if !yes {
				if !term.IsTerminal(os.Stdin.Fd()) {
					return errs.CLIError{
						Message: "reset is destructive and was not confirmed",
						Hint:    "re-run with --yes (no interactive terminal to prompt on)",
					}
				}
				ok := false
				confirm := huh.NewConfirm().
					Title(fmt.Sprintf("Reset the mixer (%s)? This cannot be undone.", scopeLabel(scope))).
					Value(&ok)
				if err := confirm.Run(); err != nil {
					return errs.CLIError{Message: fmt.Sprintf("confirmation failed: %v", err)}
				}
				if !ok {
					fmt.Println(ui.Hint("reset canceled"))
					return nil
				}
			}

			c, err := g.dialClient(cmd.Context())
			if err != nil {
				return err
			}
			defer func() { _ = c.Close() }()

			if err := c.ResetMixer(cmd.Context(), scope); err != nil {
				return errs.CLIError{Message: fmt.Sprintf("reset failed: %v", err)}
			}
			if g.json {
				return printJSON(map[string]any{
					"action": "reset", "scene": scope.Scene, "project": scope.Project, "ok": true,
				})
			}
			fmt.Println(ui.Success(fmt.Sprintf("reset mixer (%s)", scopeLabel(scope))))
			return nil
		},
	}
	cmd.Flags().BoolVar(&scene, "scene", false, "reset scene-level settings")
	cmd.Flags().BoolVar(&project, "project", false, "reset project-level settings")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}

// scopeLabel describes a reset scope for messages.
func scopeLabel(s ucmix.ResetScope) string {
	switch {
	case s.Scene && s.Project:
		return "scene + project"
	case s.Scene:
		return "scene"
	case s.Project:
		return "project"
	default:
		return "nothing"
	}
}
