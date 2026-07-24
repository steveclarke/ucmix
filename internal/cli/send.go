package cli

import "github.com/spf13/cobra"

// newSendCmd builds `send <channel> <mix> <dB>`, the human shortcut for a
// channel's send level into a monitor mix. It is a thin veneer over `set`:
// it writes line/ch{channel}/aux{mix}.
func newSendCmd(g *globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "send <channel> <mix> <dB>",
		Short: "Set a channel's send level into a monitor mix",
		Long: "Set how much of a channel feeds a monitor mix: line/ch{channel}/aux{mix}.\n\n" +
			"A thin veneer over `set`. The channel and mix are numbers; the level is dB.",
		Example: "  ucmix send 3 1 -6dB   # channel 3 into mix 1 at -6 dB",
		Args:    cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			ch, err := parseIndex(args[0])
			if err != nil {
				return err
			}
			mix, err := parseIndex(args[1])
			if err != nil {
				return err
			}
			path := sendPath(ch, mix)

			c, err := g.dialClient(cmd.Context())
			if err != nil {
				return err
			}
			defer func() { _ = c.Close() }()

			return applyNoun(cmd.Context(), g, c, path, args[2])
		},
	}
	// A level like -6dB otherwise looks like a flag; take positionals verbatim.
	cmd.Flags().SetInterspersed(false)
	return cmd
}
