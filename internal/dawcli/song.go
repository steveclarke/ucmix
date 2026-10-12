package dawcli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveclarke/ucmix/internal/daw"
)

func newSongCmd(o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "song",
		Short: "Start a new song",
	}
	var tempo float64
	var click string
	newCmd := &cobra.Command{
		Use:   "new",
		Short: "Start a new song, optionally at a tempo and with the click on or off",
		Long: "Runs Studio Pro's File > New and presses OK in its New window, so the new song gets\n" +
			"the window's current template and settings and Studio Pro's default name (the date\n" +
			"and your name). The song that was open stays open in its own tab; nothing is saved\n" +
			"or closed. Then sets --tempo and --click and reads everything back.\n\n" +
			"If Studio Pro shows any other window instead, ucdaw cancels it and says so.",
		Example: "  ucdaw song new\n  ucdaw song new --tempo 72 --click on",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			click = strings.ToLower(strings.TrimSpace(click))
			if click != "" && click != "on" && click != "off" {
				return daw.Problem{Message: fmt.Sprintf("--click takes on or off, not %q", click)}
			}
			if tempo < 0 {
				return daw.Problem{Message: fmt.Sprintf("--tempo needs a number of bpm, not %v", tempo)}
			}
			return run(cmd, o, daw.Request{Op: "new_song", BPM: tempo, Click: click})
		},
	}
	newCmd.Flags().Float64Var(&tempo, "tempo", 0, "tempo in bpm (default: the new song's own)")
	newCmd.Flags().StringVar(&click, "click", "", "on or off (default: the new song's own)")
	cmd.AddCommand(newCmd)
	return cmd
}
