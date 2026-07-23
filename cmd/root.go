package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Build-time version info, injected via -ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// newRootCmd builds the root command. Subcommands are wired here as they are
// implemented (constructor style — no package-level command globals).
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "ucmix",
		Short:         "Control PreSonus StudioLive mixers over UCNET",
		Version:       fmt.Sprintf("%s (%s, %s)", version, commit, date),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	return root
}

// Execute runs the CLI and handles top-level error display.
func Execute() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
