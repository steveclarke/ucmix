package cli

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
	"github.com/steveclarke/ucmix/internal/config"
	"github.com/steveclarke/ucmix/internal/discovery"
	"github.com/steveclarke/ucmix/internal/errs"
	"github.com/steveclarke/ucmix/internal/ui"
)

// manualEntry is the select-list sentinel for "type an IP instead".
const manualEntry = -1

// newSetupCmd builds `setup`: an interactive first-run flow that discovers
// mixers on the LAN, lets the user pick one (or type an IP), names it, and saves
// it as a profile. It requires an interactive terminal.
func newSetupCmd(g *globals) *cobra.Command {
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Interactively find a mixer and save it as a profile",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !term.IsTerminal(os.Stdin.Fd()) {
				return errs.CLIError{
					Message: "setup needs an interactive terminal",
					Hint:    "on a non-interactive shell, use `ucmix profile add <name> --host <ip>`",
				}
			}

			fmt.Println(ui.Hint(fmt.Sprintf("scanning for mixers (%s)…", timeout)))
			mixers, err := discovery.Scan(cmd.Context(), timeout)
			if err != nil {
				return errs.CLIError{
					Message: fmt.Sprintf("discovery failed: %v", err),
					Hint:    "check that UDP port 47809 is not blocked and you are on the mixer's network",
				}
			}

			host, defaultName, err := chooseHost(mixers)
			if err != nil {
				return err
			}

			name := defaultName
			if err := huh.NewInput().
				Title("Name this profile").
				Value(&name).
				Run(); err != nil {
				return errs.CLIError{Message: fmt.Sprintf("prompt failed: %v", err)}
			}
			name = strings.TrimSpace(name)
			if name == "" {
				return errs.CLIError{Message: "profile name cannot be empty"}
			}

			makeCurrent := true
			if err := huh.NewConfirm().
				Title(fmt.Sprintf("Save profile %q → %s and make it current?", name, host)).
				Value(&makeCurrent).
				Run(); err != nil {
				return errs.CLIError{Message: fmt.Sprintf("prompt failed: %v", err)}
			}

			w := config.File{}.NewWriter()
			if err := w.AddProfile(name, host); err != nil {
				return err
			}
			if makeCurrent {
				if err := w.SetCurrent(name); err != nil {
					return err
				}
			}
			msg := fmt.Sprintf("saved profile %q → %s", name, host)
			if makeCurrent {
				msg += " (now current)"
			}
			fmt.Println(ui.Success(msg))
			fmt.Println(ui.Hint("try it: ucmix dump line/ch1"))
			return nil
		},
	}
	cmd.Flags().DurationVar(&timeout, "timeout", defaultScanWindow, "how long to listen for broadcasts")
	return cmd
}

// chooseHost presents discovered mixers (plus a manual-entry option) and returns
// the chosen host and a suggested profile name. With no mixers found it goes
// straight to manual entry.
func chooseHost(mixers []discovery.Mixer) (host, defaultName string, err error) {
	if len(mixers) == 0 {
		fmt.Println(ui.Hint("no mixers found on the network — enter an address manually"))
		return manualHost()
	}

	opts := make([]huh.Option[int], 0, len(mixers)+1)
	for i, m := range mixers {
		opts = append(opts, huh.NewOption(mixerLabel(m), i))
	}
	opts = append(opts, huh.NewOption("Enter an IP manually…", manualEntry))

	choice := 0
	if err := huh.NewSelect[int]().
		Title("Pick a mixer").
		Options(opts...).
		Value(&choice).
		Run(); err != nil {
		return "", "", errs.CLIError{Message: fmt.Sprintf("prompt failed: %v", err)}
	}
	if choice == manualEntry {
		return manualHost()
	}
	m := mixers[choice]
	return m.IP, slugName(m.Name), nil
}

// manualHost prompts for a hand-typed host address.
func manualHost() (host, defaultName string, err error) {
	if err := huh.NewInput().
		Title("Mixer host").
		Placeholder("192.168.1.50").
		Value(&host).
		Run(); err != nil {
		return "", "", errs.CLIError{Message: fmt.Sprintf("prompt failed: %v", err)}
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return "", "", errs.CLIError{Message: "no host entered"}
	}
	return host, "", nil
}

// slugName turns a model string into a lowercase profile-name default, keeping
// only alphanumerics (e.g. "StudioLive 32R" → "studiolive32r").
func slugName(model string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(model) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}
