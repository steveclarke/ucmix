package dawcli

import (
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveclarke/ucmix/internal/daw"
)

func newFavoriteCmd(o *options) *cobra.Command {
	local := func(cmd *cobra.Command, req daw.Request) error {
		res, err := daw.Favorites(req)
		if err != nil {
			return err
		}
		return printResult(cmd.OutOrStdout(), o, res)
	}
	cmd := &cobra.Command{
		Use:     "favorite",
		Aliases: []string{"favorites", "fav"},
		Short:   "Remember your sounds by name: \"clean guitar\" = Guitar Rig 7 with your Clean preset",
		Long: "Favorites map the words you use to a plug-in and one of its Studio Pro presets.\n" +
			"`insert`, `track add --insert` and `instrument add` accept a favorite's name\n" +
			"wherever they take a plug-in (\"my clean guitar\" works too). Stored privately in\n" +
			"your config folder (favorites.json, readable only by you).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return local(cmd, daw.Request{Op: "favorite", Action: "list"})
		},
	}
	var preset string
	set := &cobra.Command{
		Use:     "set <name> <plug-in>",
		Short:   "Remember a plug-in (and preset) under a name; setting a name again replaces it",
		Example: "  ucdaw favorite set \"clean guitar\" \"Guitar Rig 7\" --preset Clean\n  ucdaw favorite set \"worship synth\" \"Syntronik 2\"",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return local(cmd, daw.Request{Op: "favorite", Action: "set", Name: args[0], Plugin: args[1], Preset: preset})
		},
	}
	set.Flags().StringVar(&preset, "preset", "", "the plug-in's Studio Pro preset (a subfolder goes first: Misc/50 Hz Removal)")
	list := &cobra.Command{
		Use:   "list",
		Short: "List your favorites",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return local(cmd, daw.Request{Op: "favorite", Action: "list"})
		},
	}
	remove := &cobra.Command{
		Use:     "remove <name>",
		Aliases: []string{"rm", "forget"},
		Short:   "Forget a favorite",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return local(cmd, daw.Request{Op: "favorite", Action: "remove", Name: strings.Join(args, " ")})
		},
	}
	cmd.AddCommand(set, list, remove)
	return cmd
}

func newInstrumentCmd(o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "instrument",
		Short: "Add instrument tracks",
	}
	var name, preset string
	add := &cobra.Command{
		Use:   "add <instrument or favorite>",
		Short: "Add an instrument track with a virtual instrument, optionally with a preset and a name",
		Long: "Adds a new instrument track with the instrument loaded (no editor window), names it,\n" +
			"and reads the preset back. <instrument> is a name from `ucdaw plugins` or a favorite.",
		Example: "  ucdaw instrument add \"Syntronik 2\" --name Pad\n  ucdaw instrument add \"my worship synth\" --name Keys",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, o, daw.Request{Op: "add_instrument", Plugin: strings.Join(args, " "), Preset: preset, Name: name})
		},
	}
	add.Flags().StringVar(&name, "name", "", "name for the new track")
	add.Flags().StringVar(&preset, "preset", "", "the instrument's Studio Pro preset")
	cmd.AddCommand(add)
	return cmd
}
