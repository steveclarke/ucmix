package dawcli

import (
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveclarke/ucmix/internal/daw"
)

// Levels are often negative ("-6"), which cobra's flag parser would take for
// a flag ("unknown shorthand flag: '6'"). Commands that take a level parse
// their own arguments: globalArgs pulls out the global flags, wherever they
// are, and leaves everything else, negative numbers included, as arguments.

func newFaderCmd(o *options) *cobra.Command {
	return newLevelCmd(o, "fader",
		"fader <track> [dB] [--up dB | --down dB]",
		"Read or set a track's fader in dB, or move it --up / --down by some dB",
		"Levels use Studio Pro's own fader curve, so they match the console: 0 is unity,\n"+
			"+10 the top, -inf the bottom. A level ending in % sets fader travel instead.\n"+
			"The fader is for the balance; to bring a stem to a working level first, use trim.",
		"  ucdaw fader Pad\n  ucdaw fader Pad -6\n  ucdaw fader Pad --down 3")
}

func newTrimCmd(o *options) *cobra.Command {
	return newLevelCmd(o, "trim",
		"trim <track> [dB] [--up dB | --down dB]",
		"Read or set a track's input trim (Studio Pro's Input Gain) in dB, -24 to +24",
		"The input trim sets the level a stem enters the channel at, before plug-ins and the\n"+
			"fader. Use it to bring a stem that was exported too quiet or too loud to a working\n"+
			"level (gain staging), and keep the fader for the balance. `ucdaw analyze` says how\n"+
			"much each stem needs; `ucdaw gainstage` sets every trim at once.",
		"  ucdaw trim Bass\n  ucdaw trim Bass 20\n  ucdaw trim Bass -6\n  ucdaw trim Bass --down 2")
}

func newLevelCmd(o *options, op, use, short, long, example string) *cobra.Command {
	return &cobra.Command{
		Use:                use,
		Short:              short,
		Long:               long,
		Example:            example,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			req, help, err := levelRequest(op, args, o)
			if help {
				return cmd.Help()
			}
			if err != nil {
				return err
			}
			return run(cmd, o, req)
		},
	}
}

// levelRequest parses "<track> [dB] [--up N|--down N]" plus the global flags.
func levelRequest(op string, args []string, o *options) (daw.Request, bool, error) {
	req := daw.Request{Op: op}
	args, help, err := globalArgs(args, o)
	if help || err != nil {
		return req, help, err
	}
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
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
		return req, false, daw.Problem{Message: "name the track, e.g. ucdaw " + op + " Pad -6"}
	case len(rest) > 2:
		return req, false, daw.Problem{Message: "a " + op + " takes a track and a level; quote a track name with spaces"}
	case len(rest) == 2 && req.Action != "":
		return req, false, daw.Problem{Message: "give a level or --up/--down, not both"}
	case len(rest) == 2:
		req.Action, req.Value = "set", rest[1]
	}
	req.Path = rest[0]
	return req, false, nil
}

// globalArgs applies the global flags (--json, --direct, --host, --socket,
// --schema) found anywhere in args and returns the rest. -h/--help asks for
// help.
func globalArgs(args []string, o *options) ([]string, bool, error) {
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, value, hasValue := strings.Cut(a, "=")
		switch name {
		case "-h", "--help":
			return nil, true, nil
		case "--json":
			o.json = true
			continue
		case "--direct":
			o.direct = true
			continue
		case "--host", "--socket", "--schema":
			if !hasValue {
				if i+1 >= len(args) {
					return nil, false, daw.Problem{Message: name + " needs a value"}
				}
				value = args[i+1]
				i++
			}
			switch name {
			case "--host":
				o.host = value
			case "--socket":
				o.socket = value
			case "--schema":
				o.schemaPath = value
			}
			continue
		}
		rest = append(rest, a)
	}
	return rest, false, nil
}
