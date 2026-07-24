package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/steveclarke/ucmix/internal/errs"

	ucmix "github.com/steveclarke/ucmix"
)

// mixSuffix maps a single-write mix verb to its path suffix under aux/ch{n}.
var mixSuffix = map[string]string{
	"name":   "username",
	"stereo": "link",
	"fader":  "volume",
}

// newMixCmd builds `mix <name|n> <verb> [value]`, the human shortcut for a
// monitor mix (aux bus). A mix can be addressed by number or by its name. It is
// a thin veneer over `set`: name/stereo/fader are single writes; limiter is up
// to three (limiteron, and optional --threshold / --release).
func newMixCmd(g *globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mix <name|n> <verb> [value]",
		Short: "Monitor-mix shortcuts (name, fader, limiter) over aux/ch{n}",
		Long:  mixHelp(),
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, verb := args[0], args[1]

			c, err := g.dialClient(cmd.Context())
			if err != nil {
				return err
			}
			defer func() { _ = c.Close() }()

			n, err := resolveMixIndex(c, ref)
			if err != nil {
				return err
			}

			if verb == "limiter" {
				return runMixLimiter(g, cmd, c, n, args[2:])
			}

			suffix, ok := mixSuffix[verb]
			if !ok {
				return errs.CLIError{
					Message: fmt.Sprintf("unknown mix verb %q", verb),
					Hint:    "run `ucmix mix --help` for the verb list",
				}
			}
			if len(args) != 3 {
				return errs.CLIError{
					Message: fmt.Sprintf("mix %s takes one value", verb),
					Hint:    fmt.Sprintf("usage: ucmix mix <name|n> %s <value>", verb),
				}
			}
			path := auxPath(n, suffix)
			return applyNoun(cmd.Context(), g, c, path, args[2])
		},
	}
	// A fader value like -6dB, and the --threshold/--release values, otherwise
	// look like flags; take positionals verbatim and parse the limiter options
	// by hand. Global flags still work before the positionals.
	cmd.Flags().SetInterspersed(false)
	return cmd
}

// runMixLimiter handles `mix <ref> limiter on|off [--threshold <dB>] [--release <ms>]`.
// It writes limit/limiteron, plus limit/threshold and limit/release when their
// options are given, over a single connection with one commit barrier, and
// reports every write.
func runMixLimiter(g *globals, cmd *cobra.Command, c *ucmix.Client, n int, rest []string) error {
	if len(rest) == 0 {
		return errs.CLIError{
			Message: "mix limiter takes on|off",
			Hint:    "usage: ucmix mix <name|n> limiter on|off [--threshold <dB>] [--release <ms>]",
		}
	}
	threshold, release, err := parseLimiterOpts(rest[1:])
	if err != nil {
		return err
	}

	steps := []struct{ path, value string }{
		{auxPath(n, "limit/limiteron"), rest[0]},
	}
	if threshold != "" {
		steps = append(steps, struct{ path, value string }{auxPath(n, "limit/threshold"), threshold})
	}
	if release != "" {
		steps = append(steps, struct{ path, value string }{auxPath(n, "limit/release"), release})
	}

	items := make([]setItem, 0, len(steps))
	for _, s := range steps {
		it, err := parseSetItem(s.path, s.value)
		if err != nil {
			return err
		}
		items = append(items, it)
	}

	settings := make([]ucmix.Setting, len(items))
	for i, it := range items {
		settings[i] = ucmix.Setting{Path: it.path, Value: it.value}
	}
	if err := c.SetMany(cmd.Context(), settings); err != nil {
		return errs.CLIError{
			Message: fmt.Sprintf("could not write settings: %v", err),
			Hint:    "check each value is in range for its control",
		}
	}
	return reportSet(g, items)
}

// parseLimiterOpts reads the optional --threshold/--release pairs that follow
// the limiter on|off argument. They are parsed by hand because the mix command
// takes positionals verbatim (so a value like -6 is not seen as a flag).
func parseLimiterOpts(args []string) (threshold, release string, err error) {
	for i := 0; i < len(args); {
		switch args[i] {
		case "--threshold":
			if i+1 >= len(args) {
				return "", "", errs.CLIError{Message: "--threshold needs a dB value"}
			}
			threshold, i = args[i+1], i+2
		case "--release":
			if i+1 >= len(args) {
				return "", "", errs.CLIError{Message: "--release needs a ms value"}
			}
			release, i = args[i+1], i+2
		default:
			return "", "", errs.CLIError{
				Message: fmt.Sprintf("unexpected argument %q", args[i]),
				Hint:    "mix limiter takes on|off with optional --threshold <dB> and --release <ms>",
			}
		}
	}
	return threshold, release, nil
}

// mixHelp renders the long help listing the mix verbs.
func mixHelp() string {
	return "Monitor-mix shortcuts over the aux/ch{n} paths.\n\n" +
		"A mix can be addressed by number or by name (matched against the mix's\n" +
		"username), so `mix Steve fader -6dB` and `mix 1 fader -6dB` are the same\n" +
		"when aux 1 is named Steve.\n\n" +
		"Verbs:\n" +
		"  name    <name>   Set the mix name (aux/ch{n}/username)\n" +
		"  stereo  on|off   Stereo-link the mix (aux/ch{n}/link)\n" +
		"  fader   <dB>     Set the mix master level (aux/ch{n}/volume)\n" +
		"  limiter on|off   Switch the mix limiter (aux/ch{n}/limit/limiteron)\n" +
		"                     --threshold <dB>   also set limit/threshold\n" +
		"                     --release <ms>     also set limit/release\n\n" +
		"Examples:\n" +
		"  ucmix mix 1 name \"Steve\"\n" +
		"  ucmix mix Steve fader -6dB\n" +
		"  ucmix mix 1 limiter on --threshold -6 --release 400"
}
