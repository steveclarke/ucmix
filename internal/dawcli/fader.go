package dawcli

import (
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveclarke/ucmix/internal/daw"
)

func newFaderCmd(o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "fader <track> [dB] [--up dB | --down dB]",
		Short: "Read or set a track's fader in dB, or move it --up / --down by some dB",
		Long: "Levels use Studio Pro's own fader curve, so they match the console: 0 is unity,\n" +
			"+10 the top, -inf the bottom. A level ending in % sets fader travel instead.",
		Example: "  ucdaw fader Pad\n  ucdaw fader Pad -6\n  ucdaw fader Pad --down 3",
		// Levels are often negative ("-6"), which a flag parser would take
		// for a flag, so this command reads its own arguments.
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			req, help, err := faderRequest(args, o)
			if help {
				return cmd.Help()
			}
			if err != nil {
				return err
			}
			return run(cmd, o, req)
		},
	}
	return cmd
}

// faderRequest parses "<track> [dB] [--up N|--down N]" plus the global
// --json and --direct flags.
func faderRequest(args []string, o *options) (daw.Request, bool, error) {
	req := daw.Request{Op: "fader"}
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-h" || a == "--help":
			return req, true, nil
		case a == "--json":
			o.json = true
		case a == "--direct":
			o.direct = true
		case a == "--up" || a == "--down":
			if i+1 >= len(args) {
				return req, false, daw.Problem{Message: a + " needs a number of dB"}
			}
			req.Action, req.Value = strings.TrimPrefix(a, "--"), args[i+1]
			i++
		case strings.HasPrefix(a, "--up=") || strings.HasPrefix(a, "--down="):
			name, value, _ := strings.Cut(strings.TrimPrefix(a, "--"), "=")
			req.Action, req.Value = name, value
		default:
			rest = append(rest, a)
		}
	}
	switch {
	case len(rest) == 0:
		return req, false, daw.Problem{Message: "name the track, e.g. ucdaw fader Pad -6"}
	case len(rest) > 2:
		return req, false, daw.Problem{Message: "a fader takes a track and a level; quote a track name with spaces"}
	case len(rest) == 2 && req.Action != "":
		return req, false, daw.Problem{Message: "give a level or --up/--down, not both"}
	case len(rest) == 2:
		req.Action, req.Value = "set", rest[1]
	}
	req.Path = rest[0]
	return req, false, nil
}
