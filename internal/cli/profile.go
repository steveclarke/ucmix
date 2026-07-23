package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
	"github.com/steveclarke/ucmix/internal/config"
	"github.com/steveclarke/ucmix/internal/errs"
	"github.com/steveclarke/ucmix/internal/ui"
)

// newProfileCmd builds the `profile` command group: saved mixer targets with a
// current-selection pointer. Profiles are stored in ~/.config/ucmix/config.yml;
// mutations preserve the file's comments and unmanaged keys.
func newProfileCmd(g *globals) *cobra.Command {
	p := &cobra.Command{
		Use:   "profile",
		Short: "Manage saved mixer connection profiles",
		Long: "Manage saved mixer connection profiles.\n\n" +
			"A profile is a named mixer target (host[:port]). One profile is current;\n" +
			"commands use it unless overridden with --host or -p/--profile.",
	}
	p.AddCommand(
		newProfileAddCmd(g),
		newProfileLsCmd(g),
		newProfileUseCmd(g),
		newProfileShowCmd(g),
		newProfileRmCmd(g),
		newProfileRenameCmd(g),
	)
	return p
}

// newProfileAddCmd builds `profile add <name> [--host h] [--use]`. The host is
// taken from --host, or prompted for on an interactive terminal, else it errors.
func newProfileAddCmd(g *globals) *cobra.Command {
	var host string
	var makeCurrent bool
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add or update a profile",
		Long: "Add or update a saved profile.\n\n" +
			"Example:\n  ucmix profile add foh --host 192.168.1.50\n" +
			"  ucmix profile add monitor --host 192.168.1.51:53000 --use",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			if name == "" {
				return errs.CLIError{Message: "profile name cannot be empty"}
			}
			host = strings.TrimSpace(host)
			if host == "" {
				h, err := promptHost(name)
				if err != nil {
					return err
				}
				host = h
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
			return nil
		},
	}
	cmd.Flags().StringVar(&host, "host", "", "mixer host[:port]")
	cmd.Flags().BoolVar(&makeCurrent, "use", false, "also make this the current profile")
	return cmd
}

// promptHost asks for a host on an interactive terminal, erroring otherwise.
func promptHost(name string) (string, error) {
	if !term.IsTerminal(os.Stdin.Fd()) {
		return "", errs.CLIError{
			Message: fmt.Sprintf("no host given for profile %q", name),
			Hint:    "pass --host <ip[:port]> (no interactive terminal to prompt on)",
		}
	}
	var host string
	in := huh.NewInput().
		Title(fmt.Sprintf("Mixer host for profile %q", name)).
		Placeholder("192.168.1.50").
		Value(&host)
	if err := in.Run(); err != nil {
		return "", errs.CLIError{Message: fmt.Sprintf("prompt failed: %v", err)}
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return "", errs.CLIError{Message: "no host entered"}
	}
	return host, nil
}

// newProfileLsCmd builds `profile ls`: list profiles, marking the current one.
func newProfileLsCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List saved profiles",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := config.File{}.Load()
			if err != nil {
				return err
			}
			names := cfg.ProfileNames()

			if g.json {
				rows := make([]map[string]any, len(names))
				for i, n := range names {
					rows[i] = map[string]any{
						"name":    n,
						"host":    cfg.Profiles[n].Host,
						"current": n == cfg.Current,
					}
				}
				return printJSON(map[string]any{"profiles": rows, "current": cfg.Current})
			}

			if len(names) == 0 {
				fmt.Println(ui.Hint("(no profiles — add one with `ucmix profile add` or run `ucmix setup`)"))
				return nil
			}
			fmt.Println(profileTable(cfg, names))
			return nil
		},
	}
}

// profileTable renders the marker/name/host columns, marking the current
// profile with a leading asterisk.
func profileTable(cfg config.Config, names []string) string {
	nameW := len("NAME")
	for _, n := range names {
		if len(n) > nameW {
			nameW = len(n)
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "  %-*s  %s\n", nameW, "NAME", "HOST")
	for _, n := range names {
		marker := " "
		if n == cfg.Current {
			marker = "*"
		}
		fmt.Fprintf(&b, "%s %-*s  %s\n", marker, nameW, n, cfg.Profiles[n].Host)
	}
	return strings.TrimRight(b.String(), "\n")
}

// newProfileUseCmd builds `profile use <name>`: set the current profile.
func newProfileUseCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "use <name>",
		Short: "Set the current profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			cfg, err := config.File{}.Load()
			if err != nil {
				return err
			}
			if _, ok := cfg.Profiles[name]; !ok {
				return errs.CLIError{
					Message: fmt.Sprintf("no profile named %q", name),
					Hint:    "list profiles with `ucmix profile ls`",
				}
			}
			if err := (config.File{}.NewWriter()).SetCurrent(name); err != nil {
				return err
			}
			fmt.Println(ui.Success(fmt.Sprintf("current profile is now %q", name)))
			return nil
		},
	}
}

// newProfileShowCmd builds `profile show [name]`: print one profile's resolved
// host and port (defaults to the current profile).
func newProfileShowCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "show [name]",
		Short: "Show a profile's host (defaults to current)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, err := config.File{}.Load()
			if err != nil {
				return err
			}
			name := cfg.Current
			if len(args) == 1 {
				name = strings.TrimSpace(args[0])
			}
			if name == "" {
				return errs.CLIError{
					Message: "no profile specified and none is current",
					Hint:    "name a profile, or set one with `ucmix profile use <name>`",
				}
			}
			p, ok := cfg.Profiles[name]
			if !ok {
				return errs.CLIError{Message: fmt.Sprintf("no profile named %q", name)}
			}
			if g.json {
				return printJSON(map[string]any{"name": name, "host": p.Host, "current": name == cfg.Current})
			}
			fmt.Println(ui.KeyValue("name", name))
			fmt.Println(ui.KeyValue("host", p.Host))
			fmt.Println(ui.KeyValue("current", name == cfg.Current))
			return nil
		},
	}
}

// newProfileRmCmd builds `profile rm <name>`: delete a profile.
func newProfileRmCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "rm <name>",
		Short: "Remove a profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			if err := (config.File{}.NewWriter()).RemoveProfile(name); err != nil {
				return err
			}
			fmt.Println(ui.Success(fmt.Sprintf("removed profile %q", name)))
			return nil
		},
	}
}

// newProfileRenameCmd builds `profile rename <old> <new>`.
func newProfileRenameCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "rename <old> <new>",
		Short: "Rename a profile",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			old, newName := strings.TrimSpace(args[0]), strings.TrimSpace(args[1])
			if newName == "" {
				return errs.CLIError{Message: "new profile name cannot be empty"}
			}
			if err := (config.File{}.NewWriter()).RenameProfile(old, newName); err != nil {
				return err
			}
			fmt.Println(ui.Success(fmt.Sprintf("renamed profile %q → %q", old, newName)))
			return nil
		},
	}
}
