package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveclarke/ucmix/internal/errs"
	"github.com/steveclarke/ucmix/internal/ui"

	ucmix "github.com/steveclarke/ucmix"
)

// newFiltersCmd builds `filters` with `ls` and `set`: the scope-filter tiles
// that decide what a store, recall or reset actually touches. A tile is either
// included in the operation or excluded from it — the board ships with the scene
// filter's 48v tile excluded, which is why a scene recall leaves phantom power
// alone.
func newFiltersCmd(g *globals) *cobra.Command {
	filters := &cobra.Command{
		Use:   "filters",
		Short: "Scope filters — what a store, recall or reset touches",
	}
	filters.AddCommand(newFiltersLsCmd(g), newFiltersSetCmd(g))
	return filters
}

// newFiltersLsCmd builds `filters ls [group]`: show each tile's state. With no
// group every group is shown.
func newFiltersLsCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "ls [group]",
		Short: "Show the scope-filter tiles and their state",
		Example: `  ucmix filters ls
  ucmix filters ls scene`,
		Args: requireArgs(cobra.MaximumNArgs(1), "filters ls takes at most one filter group",
			filterGroupHint()),
		RunE: func(cmd *cobra.Command, args []string) error {
			var groups []ucmix.FilterGroup
			if len(args) == 1 {
				group, err := parseFilterGroup(args[0])
				if err != nil {
					return err
				}
				groups = append(groups, group)
			}

			c, err := g.dialClient(cmd.Context())
			if err != nil {
				return err
			}
			defer func() { _ = c.Close() }()

			tiles, err := c.Filters(groups...)
			if err != nil {
				return errs.CLIError{Message: fmt.Sprintf("listing filters failed: %v", err)}
			}

			if g.json {
				out := make([]map[string]any, len(tiles))
				for i, t := range tiles {
					out[i] = map[string]any{
						"group": string(t.Group), "tile": t.Tile, "path": t.Path,
						"included": t.Included, "present": t.Present,
					}
				}
				return printJSON(map[string]any{"filters": out})
			}
			printFilterTiles(tiles)
			return nil
		},
	}
}

// printFilterTiles prints one section per group, each tile with its state.
func printFilterTiles(tiles []ucmix.FilterTile) {
	var group ucmix.FilterGroup
	var rows [][2]string
	flush := func() {
		if len(rows) == 0 {
			return
		}
		fmt.Println(ui.Table(rows))
		rows = nil
	}
	for _, t := range tiles {
		if t.Group != group {
			flush()
			if group != "" {
				fmt.Println()
			}
			group = t.Group
			fmt.Println(ui.Header(string(group) + " filter"))
		}
		rows = append(rows, [2]string{t.Tile, filterState(t)})
	}
	flush()
}

// filterState describes one tile for display. A tile the board never reported is
// called out as such: absent is not the same fact as excluded.
func filterState(t ucmix.FilterTile) string {
	switch {
	case !t.Present:
		return ui.Hint("not reported by the mixer")
	case t.Included:
		return "included"
	default:
		return "excluded"
	}
}

// newFiltersSetCmd builds `filters set <group> <tile> <on|off>`: include or
// exclude one tile.
func newFiltersSetCmd(g *globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <group> <tile> <on|off>",
		Short: "Include or exclude one scope-filter tile",
		Example: `  ucmix filters set scene 48v on
  ucmix filters set project inputpatching off`,
		Args: requireArgs(cobra.ExactArgs(3), "filters set needs a group, a tile, and on or off",
			"run `ucmix filters ls` to see the tiles"),
		RunE: func(cmd *cobra.Command, args []string) error {
			group, err := parseFilterGroup(args[0])
			if err != nil {
				return err
			}
			if _, ok := parseBool(args[2]); !ok {
				return errs.CLIError{
					Message: fmt.Sprintf("%q is not on or off", args[2]),
					Hint:    "a tile is either included in a store/recall/reset (on) or excluded from it (off)",
				}
			}
			// Resolve the tile before dialing, so a typo fails without touching
			// the mixer and the error can name the tiles that do exist.
			path, err := ucmix.FilterPath(group, args[1])
			if err != nil {
				return errs.CLIError{
					Message: err.Error(),
					Hint:    fmt.Sprintf("run `ucmix filters ls %s` to see this group's tiles", group),
				}
			}

			c, err := g.dialClient(cmd.Context())
			if err != nil {
				return err
			}
			// A filter is a write like any other: applyNoun sends it, then reads
			// it back on a fresh connection and reports what the board holds. A
			// tile this firmware does not carry reads back absent instead of
			// reporting a success nobody confirmed.
			return applyNoun(cmd.Context(), g, c, path, args[2])
		},
	}
	// A tile value is a plain word, but keep the same positional handling every
	// other write command uses so --no-verify lands before the arguments.
	cmd.Flags().SetInterspersed(false)
	addNoVerifyFlag(cmd, g)
	return cmd
}

// parseFilterGroup resolves a group argument, case-insensitively.
func parseFilterGroup(arg string) (ucmix.FilterGroup, error) {
	want := strings.ToLower(strings.TrimSpace(arg))
	for _, g := range ucmix.FilterGroups() {
		if string(g) == want {
			return g, nil
		}
	}
	return "", errs.CLIError{
		Message: fmt.Sprintf("no filter group %q", arg),
		Hint:    filterGroupHint(),
	}
}

// filterGroupHint lists the filter groups for a usage error.
func filterGroupHint() string {
	names := make([]string, 0, 3)
	for _, g := range ucmix.FilterGroups() {
		names = append(names, string(g))
	}
	return "the groups are " + strings.Join(names, ", ")
}
