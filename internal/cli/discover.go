package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveclarke/ucmix/internal/discovery"
	"github.com/steveclarke/ucmix/internal/errs"
	"github.com/steveclarke/ucmix/internal/ui"
)

// defaultScanWindow is how long discover/setup listen for mixer broadcasts.
const defaultScanWindow = 3 * time.Second

// newDiscoverCmd builds `discover [--timeout d]`: scan the LAN for mixers and
// print what is found, changing no config. It reports nothing found rather than
// erroring on a quiet network.
func newDiscoverCmd(g *globals) *cobra.Command {
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "discover",
		Short: "Find StudioLive mixers on the local network",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !g.json {
				fmt.Println(ui.Hint(fmt.Sprintf("scanning for mixers (%s)…", timeout)))
			}
			mixers, err := discovery.Scan(cmd.Context(), timeout)
			if err != nil {
				return errs.CLIError{
					Message: fmt.Sprintf("discovery failed: %v", err),
					Hint:    "check that UDP port 47809 is not blocked and you are on the mixer's network",
				}
			}
			if g.json {
				return printJSON(map[string]any{"mixers": mixers})
			}
			if len(mixers) == 0 {
				fmt.Println(ui.Hint("(no mixers found — check the network, or add one with `ucmix profile add --host`)"))
				return nil
			}
			fmt.Println(mixerTable(mixers))
			return nil
		},
	}
	cmd.Flags().DurationVar(&timeout, "timeout", defaultScanWindow, "how long to listen for broadcasts")
	return cmd
}

// mixerTable renders discovered mixers as NAME / HOST / SERIAL columns.
func mixerTable(mixers []discovery.Mixer) string {
	nameW := len("NAME")
	hostW := len("HOST")
	for _, m := range mixers {
		if len(m.Name) > nameW {
			nameW = len(m.Name)
		}
		if len(m.IP) > hostW {
			hostW = len(m.IP)
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%-*s  %-*s  %s\n", nameW, "NAME", hostW, "HOST", "SERIAL")
	for _, m := range mixers {
		fmt.Fprintf(&b, "%-*s  %-*s  %s\n", nameW, m.Name, hostW, m.IP, m.Serial)
	}
	return strings.TrimRight(b.String(), "\n")
}

// mixerLabel is the human label for a discovered mixer in a select list.
func mixerLabel(m discovery.Mixer) string {
	label := m.Name
	if label == "" {
		label = "StudioLive mixer"
	}
	label += " — " + m.IP
	if m.Serial != "" {
		label += " (SN " + m.Serial + ")"
	}
	return label
}
