package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/steveclarke/ucmix/internal/errs"
	"github.com/steveclarke/ucmix/internal/ui"
)

// newProjectCmd builds `project` with `ls`: the project layer, the setup a scene
// sits on (input source and patching, AVB/SD/USB routing, flex mode, GEQ, solo).
//
// `store`/`recall` stay scene-oriented, so the project layer gets its own verbs.
// Only `ls` exists: the request UC Surface sends to store or recall a project as
// a unit has not been captured, and the preset layer is not a place to guess (see
// issue #6). What a project layer carries is set by the Project Filter tiles —
// `ucmix filters ls project`.
func newProjectCmd(g *globals) *cobra.Command {
	project := &cobra.Command{
		Use:   "project",
		Short: "Work with the project layer",
		Long: `Work with the project layer — the setup a scene sits on: input source and
patching, AVB/SD/USB routing, flex mode, GEQ, solo.

Storing and recalling a project as a unit is not implemented. The request UC
Surface sends for it has not been captured, and this is not a layer to guess at
(issue #6). What the layer carries is set by the Project Filter tiles:
` + "`ucmix filters ls project`" + `.`,
		// A command with subcommands and no RunE prints help and exits 0, which
		// would make `project store` look like it worked. Name the gap instead.
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			return errs.CLIError{
				Message: fmt.Sprintf("`project %s` does not exist", args[0]),
				Hint: "storing and recalling a project as a unit is not implemented (issue #6);" +
					" `ucmix project ls` lists them and `ucmix filters ls project` shows what the layer carries",
			}
		},
	}
	project.AddCommand(newProjectLsCmd(g))
	return project
}

// newProjectLsCmd builds `project ls`: list the projects on the board and mark
// the one it currently has loaded.
func newProjectLsCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:     "ls",
		Short:   "List the projects on the mixer",
		Example: `  ucmix project ls`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := g.dialClient(cmd.Context())
			if err != nil {
				return err
			}
			defer func() { _ = c.Close() }()

			projects, err := c.ListProjects(cmd.Context())
			if err != nil {
				return listErr("listing projects failed", err)
			}
			loaded := c.LoadedPreset()

			if g.json {
				out := make([]map[string]any, len(projects))
				for i, p := range projects {
					out[i] = map[string]any{
						"name": p.Name, "title": p.Title, "loaded": p.Name == loaded.ProjectName,
					}
				}
				return printJSON(map[string]any{"projects": out})
			}

			if len(projects) == 0 {
				fmt.Println(ui.Hint("(no projects found)"))
				return nil
			}
			rows := make([][2]string, len(projects))
			for i, p := range projects {
				state := p.Title
				if p.Name == loaded.ProjectName {
					state += " " + ui.Hint("(loaded)")
				}
				rows[i] = [2]string{p.Name, state}
			}
			fmt.Println(ui.Table(rows))
			return nil
		},
	}
}
