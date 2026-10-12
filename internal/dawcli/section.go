package dawcli

import (
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveclarke/ucmix/internal/daw"
)

func newSectionCmd(o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "section",
		Aliases: []string{"sections"},
		Short:   "List and edit the arranger sections (verse, chorus…)",
		Long: "Edits the song's arrangement through Studio Pro's arranger track: everything in a\n" +
			"section's time moves with it, and later sections move along. A section is named\n" +
			"by its name (Chorus), its number (3), or name and occurrence (\"Chorus 2\",\n" +
			"\"second chorus\", \"last chorus\"). Each edit is confirmed from the arranger list\n" +
			"Studio Pro sends back, and undoes with: ucdaw command run \"Edit > Undo\".",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, o, daw.Request{Op: "section", Action: "list"})
		},
	}
	one := func(use, action, short, example string) *cobra.Command {
		return &cobra.Command{
			Use:     use,
			Short:   short,
			Example: example,
			Args:    cobra.MinimumNArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return run(cmd, o, daw.Request{Op: "section", Action: action, Section: strings.Join(args, " ")})
			},
		}
	}
	list := &cobra.Command{
		Use:   "list",
		Short: "List the sections with their start bars and lengths",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, o, daw.Request{Op: "section", Action: "list"})
		},
	}
	var bars float64
	add := &cobra.Command{
		Use:     "add <name>...",
		Short:   "Add sections at the end of the arrangement, 8 bars each unless --bars",
		Example: "  ucdaw section add Verse Chorus Bridge\n  ucdaw section add Outro --bars 4",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, o, daw.Request{Op: "section", Action: "add", Name: strings.Join(args, ","), Bars: bars})
		},
	}
	add.Flags().Float64Var(&bars, "bars", 0, "length of each new section in bars (default 8)")
	twoPlace := func(use, action, short, example string, where string) *cobra.Command {
		return &cobra.Command{
			Use:     use,
			Short:   short,
			Example: example,
			Args:    cobra.MinimumNArgs(3),
			RunE: func(cmd *cobra.Command, args []string) error {
				section, w, target, err := splitPlacement(args, where)
				if err != nil {
					return err
				}
				return run(cmd, o, daw.Request{Op: "section", Action: action, Section: section, Where: w, Target: target})
			},
		}
	}
	repeat := twoPlace("repeat <section> after <section>", "repeat",
		"Put a copy of a section right after another one (\"repeat the chorus after the bridge\")",
		"  ucdaw section repeat Chorus after Bridge\n  ucdaw section repeat chorus one after the bridge", "after")
	move := twoPlace("move <section> before|after <section>", "move",
		"Move a section before or after another one",
		"  ucdaw section move Bridge before \"Chorus 2\"", "")
	var newName string
	rename := &cobra.Command{
		Use:     "rename <section> <new name>",
		Short:   "Rename a section",
		Example: "  ucdaw section rename 1 Intro\n  ucdaw section rename \"Chorus 2\" \"Final Chorus\"",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			newName = args[1]
			return run(cmd, o, daw.Request{Op: "section", Action: "rename", Section: args[0], Name: newName})
		},
	}
	var seconds float64
	var playBars float64
	play := &cobra.Command{
		Use:     "play <section>",
		Short:   "Play from the start of a section (for --for seconds or --bars, then stop)",
		Example: "  ucdaw section play Chorus --bars 4",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, o, daw.Request{Op: "section", Action: "play", Section: strings.Join(args, " "), Seconds: seconds, Bars: playBars})
		},
	}
	play.Flags().Float64Var(&seconds, "for", 0, "stop after this many seconds")
	play.Flags().Float64Var(&playBars, "bars", 0, "stop after this many bars")
	cmd.AddCommand(list, add,
		one("duplicate <section>", "duplicate", "Copy a section and insert the copy right after it", "  ucdaw section duplicate Chorus"),
		repeat, move,
		one("delete <section>", "delete", "Remove a section and close the gap", "  ucdaw section delete Bridge"),
		rename,
		one("goto <section>", "goto", "Put the cursor at the start of a section", "  ucdaw section goto Chorus"),
		play,
	)
	return cmd
}

// splitPlacement turns "chorus one after the bridge" into the section, the
// word before/after, and the target section.
func splitPlacement(args []string, fixed string) (section, where, target string, err error) {
	for i, a := range args {
		w := strings.ToLower(a)
		if (w == "before" || w == "after") && i > 0 && i < len(args)-1 {
			if fixed != "" && w != fixed {
				continue
			}
			return strings.Join(args[:i], " "), w, strings.Join(args[i+1:], " "), nil
		}
	}
	want := "before or after"
	if fixed != "" {
		want = fixed
	}
	return "", "", "", daw.Problem{Message: "say where: <section> " + want + " <section>"}
}
