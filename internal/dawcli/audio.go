package dawcli

import (
	"github.com/spf13/cobra"
	"github.com/steveclarke/ucmix/internal/daw"
)

func newAudioCmd(o *options) *cobra.Command {
	var bars, ms float64
	cmd := &cobra.Command{
		Use:   "audio <edit> <track> [bar]",
		Short: "Edit the audio on a track: fade-out, fade-in, split, trim-start, trim-end, crossfade, quantize",
		Long: "Runs Studio Pro's own editing commands on every event of one track (it selects\n" +
			"the track and its events first, like clicking them).\n\n" +
			"  fade-out  fade out the last --bars of the take (default 1)\n" +
			"  fade-in   fade in the first --bars (default 1)\n" +
			"  split     split the take at a bar\n" +
			"  trim-start, trim-end   trim the take to a bar\n" +
			"  crossfade crossfade where events overlap and put short fades (--ms, default 10) on every edit\n" +
			"  quantize  quantize to Studio Pro's quantize grid\n\n" +
			"Studio Pro does not report audio events to remotes, so these are sent but not read back.",
		Example: "  ucdaw audio fade-out Guitar\n  ucdaw audio split Guitar 9\n  ucdaw audio crossfade Guitar --ms 20",
		Args:    cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := daw.Request{Op: "audio", Action: args[0], Path: args[1], Bars: bars, Seconds: ms / 1000}
			if len(args) == 3 {
				req.Value = args[2]
			}
			return run(cmd, o, req)
		},
	}
	cmd.Flags().Float64Var(&bars, "bars", 0, "fade length in bars")
	cmd.Flags().Float64Var(&ms, "ms", 0, "crossfade length in milliseconds")
	return cmd
}
