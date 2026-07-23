package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/steveclarke/ucmix/internal/errs"
	"github.com/steveclarke/ucmix/internal/ui"
)

// newLsCmd builds `ls` with `projects` and `scenes` subcommands: list presets
// the mixer exposes.
func newLsCmd(g *globals) *cobra.Command {
	ls := &cobra.Command{
		Use:   "ls",
		Short: "List presets on the mixer",
	}
	ls.AddCommand(newLsProjectsCmd(g), newLsScenesCmd(g))
	return ls
}

// newLsProjectsCmd builds `ls projects`: list the projects/presets the board
// returns for presets/proj.
func newLsProjectsCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "projects",
		Short: "List projects on the mixer",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := g.dialClient(cmd.Context())
			if err != nil {
				return err
			}
			defer func() { _ = c.Close() }()

			projects, err := c.ListProjects(cmd.Context())
			if err != nil {
				return errs.CLIError{Message: fmt.Sprintf("listing projects failed: %v", err)}
			}
			names := make([]string, len(projects))
			for i, p := range projects {
				names[i] = p.Name
			}

			if g.json {
				return printJSON(map[string]any{"projects": names})
			}
			printNames(names, "no projects found")
			return nil
		},
	}
}

// newLsScenesCmd builds `ls scenes <project>`. The client has no per-project
// scene lister (and even ListProjects' JM body is uncaptured against real
// hardware), so this lists whatever ListProjects returns and states the gap
// rather than inventing board behavior. On a real board ListProjects returns
// project names, not scene paths.
func newLsScenesCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "scenes <project>",
		Short: "List scenes (no dedicated lister — see note)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			project := args[0]
			c, err := g.dialClient(cmd.Context())
			if err != nil {
				return err
			}
			defer func() { _ = c.Close() }()

			projects, err := c.ListProjects(cmd.Context())
			if err != nil {
				return errs.CLIError{Message: fmt.Sprintf("listing presets failed: %v", err)}
			}
			names := make([]string, len(projects))
			for i, p := range projects {
				names[i] = p.Name
			}

			const note = "note: the client has no per-project scene lister; " +
				"showing every preset ListProjects returns"
			if g.json {
				return printJSON(map[string]any{"project": project, "presets": names, "note": note})
			}
			fmt.Fprintln(os.Stderr, ui.Hint(note))
			printNames(names, "no presets found")
			return nil
		},
	}
}

// printNames prints one name per line, or a dimmed empty-state message.
func printNames(names []string, empty string) {
	if len(names) == 0 {
		fmt.Println(ui.Hint("(" + empty + ")"))
		return
	}
	for _, n := range names {
		fmt.Println(n)
	}
}
