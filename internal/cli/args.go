package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveclarke/ucmix/internal/errs"
)

// requireArgs wraps a cobra positional-arg validator so a wrong argument count
// says what the command wants instead of cobra's bare "accepts 1 arg(s),
// received 0". message is the one-line problem ("ls scenes needs a project
// name"); hint points at how to find the value, and defaults to the command's
// usage line when there is nothing better to say.
func requireArgs(v cobra.PositionalArgs, message, hint string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := v(cmd, args); err == nil {
			return nil
		}
		h := hint
		if h == "" {
			// UseLine appends " [flags]" to anything with flags; the arguments are
			// what went wrong, so drop the noise.
			h = fmt.Sprintf("usage: %s", strings.TrimSuffix(cmd.UseLine(), " [flags]"))
		}
		return errs.CLIError{Message: message, Hint: h}
	}
}
