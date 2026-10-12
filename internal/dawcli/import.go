package dawcli

import (
	"time"

	"github.com/spf13/cobra"
	"github.com/steveclarke/ucmix/internal/daw"
)

// importTimeout bounds a whole import: each stem waits for Studio Pro to
// read it, up to three minutes for a very long file.
const importTimeout = 30 * time.Minute

func newImportCmd(o *options) *cobra.Command {
	var onto string
	var at int
	cmd := &cobra.Command{
		Use:   "import <file|folder>...",
		Short: "Import audio files: each stem on its own new track, lined up at bar 1",
		Long: "Imports WAV and AIFF files into the open song. A folder imports its audio files in\n" +
			"name order and says which other files it skipped.\n\n" +
			"Each file gets a new mono or stereo track to match it, named for a musician\n" +
			"(\"01_Kick_Final.wav\" becomes Kick), with the clip at bar 1. --onto places the\n" +
			"files on an existing audio track instead, --at at another bar.\n\n" +
			"Studio Pro does not report clips to remotes: tracks are read back, clip placement\n" +
			"is not. ucdaw watches for the waveform Studio Pro draws for each new file, so it\n" +
			"knows each file was read before importing the next. Works only with Studio Pro on\n" +
			"this Mac. Undo is about three presses per stem.",
		Example: "  ucdaw import ~/Music/stems\n  ucdaw import Vox.wav --onto Vox --at 17",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			files, err := daw.ImportPaths(args)
			if err != nil {
				return err
			}
			return run(cmd, o, daw.Request{Op: "import", Files: files, Onto: onto, At: at})
		},
	}
	cmd.Flags().StringVar(&onto, "onto", "", "existing audio track to place the files on")
	cmd.Flags().IntVar(&at, "at", 1, "bar to place the files at")
	return cmd
}
