// Package cli wires the ucmix cobra command tree. Commands use constructor
// style (newXxxCmd) with no package-level command globals; they stay thin and
// delegate to the internal packages. Shared connect logic and the global flags
// (--host, --json, --no-color) hang off a per-invocation globals value passed to
// each subcommand constructor.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveclarke/ucmix/internal/config"
	"github.com/steveclarke/ucmix/internal/errs"
	"github.com/steveclarke/ucmix/internal/ui"

	ucmix "github.com/steveclarke/ucmix"
)

// Build-time version info, injected via -ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// connectTimeout bounds the handshake wait so a wrong/unreachable host fails
// with a hint instead of hanging.
const connectTimeout = 5 * time.Second

// globals carries the root's persistent-flag values and the shared connect
// helper. One value is built per invocation in newRootCmd and threaded into each
// subcommand constructor — no package-level command state.
type globals struct {
	host    string
	json    bool
	noColor bool
}

// dialClient resolves the mixer host (flag > UCMIX_HOST > config file) and opens
// a connection. Connect failures come back as an errs.CLIError with a hint.
func (g *globals) dialClient(ctx context.Context) (*ucmix.Client, error) {
	addr, err := config.ResolveHost(g.host)
	if err != nil {
		return nil, err
	}
	dctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	c, err := ucmix.Connect(dctx, addr)
	if err != nil {
		return nil, errs.CLIError{
			Message: fmt.Sprintf("could not connect to mixer at %s: %v", addr, err),
			Hint:    "check --host or UCMIX_HOST; is the mixer reachable?",
		}
	}
	return c, nil
}

// newRootCmd builds the root command and wires every subcommand.
func newRootCmd() *cobra.Command {
	g := &globals{}
	root := &cobra.Command{
		Use:           "ucmix",
		Short:         "Control PreSonus StudioLive mixers over UCNET",
		Version:       fmt.Sprintf("%s (%s, %s)", version, commit, date),
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRun: func(_ *cobra.Command, _ []string) {
			ui.Init(g.noColor)
		},
	}
	pf := root.PersistentFlags()
	pf.StringVar(&g.host, "host", "", "mixer host[:port] (overrides UCMIX_HOST and config file)")
	pf.BoolVar(&g.json, "json", false, "emit machine-readable JSON")
	pf.BoolVar(&g.noColor, "no-color", false, "disable colored output")

	root.AddCommand(
		newDumpCmd(g),
		newGetCmd(g),
		newSetCmd(g),
		newVerifyCmd(g),
		newApplyCmd(g),
		newRecallCmd(g),
		newStoreCmd(g),
		newResetCmd(g),
		newLsCmd(g),
	)
	return root
}

// exitError carries an explicit process exit code out of a command. verify and
// apply use it to distinguish clean (0), drift found (1), and error (2); the
// inner err (if any) is rendered by Execute, a nil err exits silently with the
// code. Other commands keep returning plain errors, which Execute maps to 1.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string {
	if e.err != nil {
		return e.err.Error()
	}
	return ""
}

func (e *exitError) Unwrap() error { return e.err }

// Execute runs the CLI. A CLIError is rendered specially: the Message on one
// line and the Hint dimmed below it. Any other error prints its text. The
// process exit code is 1 by default; a command that returns an *exitError sets
// it explicitly (verify/apply use 1 for drift, 2 for errors), and a nil inner
// error exits silently with that code.
func Execute() {
	err := newRootCmd().Execute()
	if err == nil {
		return
	}
	code := 1
	inner := err
	var ee *exitError
	if errors.As(err, &ee) {
		code = ee.code
		inner = ee.err
	}
	if inner != nil {
		var ce errs.CLIError
		if errors.As(inner, &ce) {
			fmt.Fprintln(os.Stderr, ui.ErrorLine(ce.Message))
			if ce.Hint != "" {
				fmt.Fprintln(os.Stderr, ui.Hint(ce.Hint))
			}
		} else {
			fmt.Fprintln(os.Stderr, ui.ErrorLine(inner.Error()))
		}
	}
	os.Exit(code)
}

// normalizePath translates a dotted path (line.ch1.mute) to the wire form
// (line/ch1/mute). Paths already using slashes pass through unchanged.
func normalizePath(p string) string {
	return strings.ReplaceAll(p, ".", "/")
}

// printJSON writes v as indented JSON to stdout followed by a newline.
func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
