package dawcli

import (
	"github.com/spf13/cobra"
	"github.com/steveclarke/ucmix/internal/daw"
)

func newTrackPresetCmd(o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "trackpreset",
		Aliases: []string{"trackpresets"},
		Short:   "List and load your saved track presets (a track or a whole group, with its plug-ins)",
	}
	list := &cobra.Command{
		Use:     "list [filter]",
		Short:   "List the track presets saved on this Mac (the names load takes)",
		Example: "  ucdaw trackpreset list\n  ucdaw trackpreset list bass",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			filter := ""
			if len(args) == 1 {
				filter = args[0]
			}
			res, err := daw.TrackPresets(filter)
			if err != nil {
				return err
			}
			return printResult(cmd.OutOrStdout(), o, res)
		},
	}
	load := &cobra.Command{
		Use:   "load <name>",
		Short: "Load a track preset into the song and report the channels it added",
		Long: "Runs Studio Pro's Track > Load Track Preset with the preset you name and waits for\n" +
			"its channels to arrive. <name> is a name from `ucdaw trackpreset list`, or the end of\n" +
			"one: the preset's own name (Bass) when only one preset has it, otherwise with enough\n" +
			"of its folders to tell it apart (Album/Bass).",
		Example: "  ucdaw trackpreset load \"Drum Kit\"\n  ucdaw trackpreset load \"Album/Bass\"",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := daw.PickTrackPreset(args[0])
			if err != nil {
				return err
			}
			return run(cmd, o, daw.Request{Op: "load_track_preset", Name: name})
		},
	}
	cmd.AddCommand(list, load)
	return cmd
}
