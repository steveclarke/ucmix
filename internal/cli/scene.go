package cli

import (
	"context"
	"errors"
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
//
// Both arguments are display titles ("135 Main Live", "Opening") or the board's
// slot names; either resolves. Success is printed only after the board confirms
// the recall.
func newRecallCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:     "recall <project> <scene>",
		Short:   "Recall a stored scene",
		Example: `  ucmix recall "135 Main Live" "Opening"`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			projectArg, sceneArg := args[0], args[1]
			c, err := g.dialClient(ctx)
			if err != nil {
				return err
			}
			defer func() { _ = c.Close() }()

			project, scene, err := resolveScene(ctx, c, projectArg, sceneArg, "recall")
			if err != nil {
				return err
			}

			if err := c.RecallScene(ctx, project.Name, scene.Name); err != nil {
				if errors.Is(err, ucmix.ErrRecallTimeout) {
					return errs.CLIError{
						Message: "recall failed: the mixer never confirmed the recall",
						Hint:    "the board may be mid-recall; check its current scene before assuming it did not load",
					}
				}
				return errs.CLIError{Message: fmt.Sprintf("recall failed: %v", err)}
			}
			if g.json {
				return printJSON(map[string]any{
					"action": "recall", "project": project.Name, "scene": scene.Name,
					"title": scene.Title, "ok": true,
				})
			}
			fmt.Println(ui.Success(fmt.Sprintf("recalled %s / %s", project.Title, scene.Title)))
			return nil
		},
	}
}

// resolveScene turns a project and scene argument (title or slot name) into the
// board's entries, wrapping failures as CLIErrors that point at the listing
// commands. verb names the operation for the error message.
func resolveScene(ctx context.Context, c *ucmix.Client, projectArg, sceneArg, verb string) (ucmix.Project, ucmix.Scene, error) {
	project, err := c.ResolveProject(ctx, projectArg)
	if err != nil {
		return ucmix.Project{}, ucmix.Scene{}, errs.CLIError{
			Message: fmt.Sprintf("%s failed: %v", verb, err),
			Hint:    "run `ucmix ls projects` to see the projects on the board",
		}
	}
	scene, err := c.ResolveScene(ctx, project.Name, sceneArg)
	if err != nil {
		return ucmix.Project{}, ucmix.Scene{}, errs.CLIError{
			Message: fmt.Sprintf("%s failed: %v", verb, err),
			Hint:    fmt.Sprintf("run `ucmix ls scenes %q` to see this project's scenes", project.Name),
		}
	}
	return project, scene, nil
}

// newStoreCmd builds `store <project> <name>`: store the current mixer state as a
// new scene, or replace an existing one with --replace.
//
// <project> and <name> are display titles ("135 Main Live", "Opening"); the slot
// name the board keys the scene by is resolved here, mirroring the allocation UC
// Surface does client-side. Success is printed only after the board confirms the
// write.
func newStoreCmd(g *globals) *cobra.Command {
	var replace bool
	cmd := &cobra.Command{
		Use:   "store <project> <name>",
		Short: "Store the current state as a scene",
		Example: `  ucmix store "135 Main Live" "Opening"
  ucmix store "135 Main Live" "Opening" --replace`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			projectArg, name := args[0], args[1]
			c, err := g.dialClient(ctx)
			if err != nil {
				return err
			}
			defer func() { _ = c.Close() }()

			project, err := c.ResolveProject(ctx, projectArg)
			if err != nil {
				return errs.CLIError{
					Message: fmt.Sprintf("store failed: %v", err),
					Hint:    "run `ucmix ls projects` to see the projects on the board",
				}
			}

			var slot string
			if replace {
				existing, err := c.ResolveScene(ctx, project.Name, name)
				if err != nil {
					return errs.CLIError{
						Message: fmt.Sprintf("store failed: %v", err),
						Hint:    fmt.Sprintf("run `ucmix ls scenes %q` to see this project's scenes", project.Name),
					}
				}
				slot = existing.Name
			} else {
				slot, err = c.NextSceneSlot(ctx, project.Name, name)
				if err != nil {
					hint := "pass --replace to overwrite it"
					if errors.Is(err, ucmix.ErrNoFreeSlot) {
						hint = "delete a scene in UC Surface to free a slot"
					}
					return errs.CLIError{Message: fmt.Sprintf("store failed: %v", err), Hint: hint}
				}
			}

			if err := c.StoreScene(ctx, project.Name, slot); err != nil {
				return storeErr(err)
			}
			if g.json {
				return printJSON(map[string]any{
					"action": "store", "project": project.Name, "scene": slot, "title": name, "ok": true,
				})
			}
			fmt.Println(ui.Success(fmt.Sprintf("stored %s / %s", project.Title, name)))
			return nil
		},
	}
	cmd.Flags().BoolVar(&replace, "replace", false, "overwrite an existing scene of that name")
	return cmd
}

// storeErr turns a store failure into a CLIError. An unacknowledged store is
// called out explicitly: nothing was written, so the user must not assume the
// scene is safe.
func storeErr(err error) error {
	if errors.Is(err, ucmix.ErrStoreTimeout) {
		return errs.CLIError{
			Message: "store failed: the mixer never confirmed the write, so nothing was saved",
			Hint:    "check the connection and try again; do not assume the scene is stored",
		}
	}
	return errs.CLIError{Message: fmt.Sprintf("store failed: %v", err)}
}

// newRenameCmd builds `rename <project> <scene> <new-name>`: change a stored
// scene's display title. The scene keeps its slot.
func newRenameCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:     "rename <project> <scene> <new-name>",
		Short:   "Rename a stored scene",
		Example: `  ucmix rename "135 Main Live" "Opening" "Opening Set"`,
		Args:    cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			projectArg, sceneArg, newName := args[0], args[1], args[2]
			c, err := g.dialClient(ctx)
			if err != nil {
				return err
			}
			defer func() { _ = c.Close() }()

			project, scene, err := resolveScene(ctx, c, projectArg, sceneArg, "rename")
			if err != nil {
				return err
			}

			if err := c.RenameScene(ctx, project.Name, scene.Name, newName); err != nil {
				return errs.CLIError{Message: fmt.Sprintf("rename failed: %v", err)}
			}
			if g.json {
				return printJSON(map[string]any{
					"action": "rename", "project": project.Name, "scene": scene.Name,
					"from": scene.Title, "to": newName, "ok": true,
				})
			}
			fmt.Println(ui.Success(fmt.Sprintf("renamed %s / %s to %s", project.Title, scene.Title, newName)))
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
