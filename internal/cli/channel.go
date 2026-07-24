package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveclarke/ucmix/internal/errs"
)

// channelVerb describes one `channel <n> <verb> <value>` action: the path suffix
// under line/ch{n} and how the typed value maps to a value `set` accepts.
type channelVerb struct {
	name   string
	suffix string
	arg    string // value placeholder for the help text
	short  string
	// mapped rewrites the typed value before it reaches the set parser (color and
	// icon name lookups); nil passes the value through unchanged.
	mapped func(string) string
}

// channelVerbs is the curated channel-strip verb set. Each row is a thin alias:
// build line/ch{n}/<suffix> and write the value through the shared set path.
var channelVerbs = []channelVerb{
	{name: "name", suffix: "username", arg: "<name>", short: "Set the channel name"},
	{name: "patch", suffix: "adc_src", arg: "<input>", short: "Patch a physical input to the channel"},
	{name: "phantom", suffix: "48v", arg: "on|off", short: "Switch 48V phantom power"},
	{name: "fader", suffix: "volume", arg: "<dB>", short: "Set the channel fader level"},
	{name: "mute", suffix: "mute", arg: "on|off", short: "Mute or unmute the channel"},
	{name: "stereo", suffix: "link", arg: "on|off", short: "Stereo-link the channel with its pair"},
	{name: "color", suffix: "color", arg: "<name|hex>", short: "Set the channel color", mapped: resolveColor},
	{name: "icon", suffix: "iconid", arg: "<name|id>", short: "Set the channel icon", mapped: resolveIcon},
	{name: "hpf", suffix: "filter/hpf", arg: "<Hz>", short: "Set the high-pass filter frequency"},
}

// channelVerbByName looks up a channel verb by its typed name.
func channelVerbByName(name string) (channelVerb, bool) {
	for _, v := range channelVerbs {
		if v.name == name {
			return v, true
		}
	}
	return channelVerb{}, false
}

// newChannelCmd builds `channel <n> <verb> <value>`, the human shortcut for the
// line/ch{n} strip. It is a thin veneer over `set`: the verb picks the path
// suffix, the value flows through the same write path.
func newChannelCmd(g *globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "channel <n> <verb> <value>",
		Short: "Channel-strip shortcuts (name, fader, mute, …) over line/ch{n}",
		Long:  channelHelp(),
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := parseIndex(args[0])
			if err != nil {
				return err
			}
			v, ok := channelVerbByName(args[1])
			if !ok {
				return errs.CLIError{
					Message: fmt.Sprintf("unknown channel verb %q", args[1]),
					Hint:    "run `ucmix channel --help` for the verb list",
				}
			}
			if len(args) != 3 {
				return errs.CLIError{
					Message: fmt.Sprintf("channel %s takes one value", v.name),
					Hint:    fmt.Sprintf("usage: ucmix channel <n> %s %s", v.name, v.arg),
				}
			}
			value := args[2]
			if v.mapped != nil {
				value = v.mapped(value)
			}
			path := channelPath(n, v.suffix)

			c, err := g.dialClient(cmd.Context())
			if err != nil {
				return err
			}
			defer func() { _ = c.Close() }()

			return applyNoun(cmd.Context(), g, c, path, value)
		},
	}
	// A fader value like -6dB otherwise looks like a flag; take positionals
	// verbatim. Global flags still work before the positionals.
	cmd.Flags().SetInterspersed(false)
	return cmd
}

// channelHelp renders the long help with the verb table so `channel --help`
// lists every channel action and the path it maps to.
func channelHelp() string {
	var b strings.Builder
	b.WriteString("Channel-strip shortcuts over the line/ch{n} paths.\n\n")
	b.WriteString("A thin veneer over `set`: each verb builds one path and writes one value.\n")
	b.WriteString("Anything not covered here stays `ucmix set line/ch{n}/<path> <value>`.\n\n")
	b.WriteString("Verbs:\n")
	for _, v := range channelVerbs {
		fmt.Fprintf(&b, "  %-8s %-12s  %s (line/ch{n}/%s)\n", v.name, v.arg, v.short, v.suffix)
	}
	b.WriteString("\nExamples:\n")
	b.WriteString("  ucmix channel 3 name \"Drums\"\n")
	b.WriteString("  ucmix channel 3 fader -6dB\n")
	b.WriteString("  ucmix channel 3 phantom on\n")
	b.WriteString("  ucmix channel 3 color blue")
	return b.String()
}
