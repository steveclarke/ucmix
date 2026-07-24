package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
	"github.com/steveclarke/ucmix/internal/boardconfig"
	"github.com/steveclarke/ucmix/internal/errs"
	"github.com/steveclarke/ucmix/internal/schema"
	"github.com/steveclarke/ucmix/internal/ui"

	ucmix "github.com/steveclarke/ucmix"
)

// newApplyCmd builds `apply <config.yml> [--dry-run] [--reset] [--yes]`: compile
// the config and write every declared setting to the board, then re-verify on a
// fresh connection. --dry-run prints the ordered write plan and never connects.
// --reset factory-resets first (destructive; gated like `reset`). Exit codes: 0
// when the post-apply verify is clean, 1 on residual drift, 2 on error.
func newApplyCmd(g *globals) *cobra.Command {
	var dryRun, reset, yes bool
	cmd := &cobra.Command{
		Use:   "apply <config.yml>",
		Short: "Write a config to the board, then verify",
		Example: `  ucmix apply board.yml
  ucmix apply board.yml --dry-run
  ucmix apply board.yml --reset --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			desired, err := loadAndCompile(args[0])
			if err != nil {
				return &exitError{code: 2, err: err}
			}

			if dryRun {
				printPlan(g, desired)
				return nil
			}

			if reset {
				proceed, err := confirmReset(yes)
				if err != nil {
					return err // already an *exitError
				}
				if !proceed {
					fmt.Println(ui.Hint("apply canceled"))
					return nil
				}
			}

			ctx := cmd.Context()

			// Connection A: reset (optional) then write every desired setting.
			ca, err := g.dialClient(ctx)
			if err != nil {
				return &exitError{code: 2, err: err}
			}
			if reset {
				if err := ca.ResetMixer(ctx, ucmix.ResetScope{Scene: true, Project: true}); err != nil {
					_ = ca.Close()
					return &exitError{code: 2, err: errs.CLIError{Message: fmt.Sprintf("reset failed: %v", err)}}
				}
			}
			written, werr := writeDesired(ctx, ca, desired, g)
			if werr == nil {
				// Write barrier: a round-trip on A forces the board's sequential
				// per-connection reader to consume every write frame before we
				// close A. Without it, closing A races delivery of the last
				// frames and the tail of the burst is silently lost (surfaced as
				// absent keys on slow CI runners).
				_, _ = ca.ListProjects(ctx)
			}
			_ = ca.Close()
			if werr != nil {
				return &exitError{code: 2, err: errs.CLIError{
					Message: fmt.Sprintf("apply failed after %d writes: %v", written, werr),
				}}
			}

			// Connection B (fresh): the in-session read-back quirk means A cannot
			// verify its own writes reliably; a new ZB is required.
			drift, err := verifyAfterApply(ctx, g, desired)
			if err != nil {
				return &exitError{code: 2, err: err}
			}
			return reportApply(g, written, drift)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the write plan without connecting or writing")
	cmd.Flags().BoolVar(&reset, "reset", false, "factory-reset the board before applying (destructive)")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the reset confirmation prompt")
	return cmd
}

// verifyAfterApply runs the fresh-connection verify, tolerating board settle
// latency: a value written on connection A may not appear in a new connection's
// ZB snapshot until the board finishes committing it (connection B missed the
// live delta and its ZB can predate the write). It re-dials a few times,
// returning as soon as the board reads clean; genuine drift survives every
// retry and is reported. A clean apply returns on the first attempt with no
// added delay.
func verifyAfterApply(ctx context.Context, g *globals, desired []boardconfig.Desired) ([]boardconfig.Mismatch, error) {
	const attempts = 5
	var drift []boardconfig.Mismatch
	for i := 0; i < attempts; i++ {
		if i > 0 {
			select {
			case <-time.After(200 * time.Millisecond):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		var err error
		drift, err = verifyOnFreshConn(ctx, g, desired)
		if err != nil {
			return nil, err
		}
		if len(drift) == 0 {
			return nil, nil
		}
	}
	return drift, nil
}

// confirmReset gates the destructive --reset, matching the standalone `reset`
// command. --yes proceeds without prompting. Otherwise it refuses in a non-tty
// (exitError 2) and runs an interactive confirm, returning the user's choice.
func confirmReset(yes bool) (proceed bool, err error) {
	if yes {
		return true, nil
	}
	if !term.IsTerminal(os.Stdin.Fd()) {
		return false, &exitError{code: 2, err: errs.CLIError{
			Message: "apply --reset is destructive and was not confirmed",
			Hint:    "re-run with --yes (no interactive terminal to prompt on)",
		}}
	}
	ok := false
	confirm := huh.NewConfirm().
		Title("Factory-reset the board before applying? This cannot be undone.").
		Value(&ok)
	if err := confirm.Run(); err != nil {
		return false, &exitError{code: 2, err: errs.CLIError{Message: fmt.Sprintf("confirmation failed: %v", err)}}
	}
	return ok, nil
}

// writeDesired writes each desired setting through client.Set in compiled order.
// It returns the number of settings written and the first write error.
//
// The value written is the HUMAN value, sent through client.Set, which applies
// the key's taper (human -> 0..1) and never a read-scale. So a -6 dB fader is
// written as Set(path, -6.0) -> wire 0.746. See applyValueFor for the few keys
// whose wire form must be written verbatim (color, enum floats).
func writeDesired(ctx context.Context, c *ucmix.Client, desired []boardconfig.Desired, g *globals) (int, error) {
	if !g.json {
		fmt.Println(ui.Header(fmt.Sprintf("applying %d settings…", len(desired))))
	}
	for _, d := range desired {
		v, err := applyValueFor(d)
		if err != nil {
			return 0, err
		}
		if err := c.Set(ctx, d.Path, v); err != nil {
			return 0, err
		}
	}
	return len(desired), nil
}

// applyValueFor picks the value to hand client.Set for one desired setting.
//
//   - bool / string: the HumanValue is already the Go type Set wants.
//   - chars (color): the WireValue carries the full 8-digit RGBA; the HumanValue
//     may be 6-digit and Set does NOT append the alpha, so the wire form is used.
//   - float with a numeric human (fader/send/threshold/release/patch/Hz): the
//     HumanValue is written and Set applies the taper (never a read-scale).
//   - float with a non-numeric human (off / raw: / enum): the WireValue is the
//     exact 0..1 wire position. For a nil/passthrough taper it is written
//     directly; for a real taper it is inverted through FromWire so Set re-tapers
//     to the same position (an off level -> the taper bottom -> wire 0.0).
func applyValueFor(d boardconfig.Desired) (any, error) {
	spec, known := schema.Lookup(d.Path)
	if !known {
		return d.WireValue, nil
	}
	switch spec.Kind {
	case schema.KindBool, schema.KindString:
		return d.HumanValue, nil
	case schema.KindChars:
		return d.WireValue, nil
	case schema.KindFloat:
		if f, ok := numeric(d.HumanValue); ok {
			return f, nil
		}
		wf, ok := numeric(d.WireValue)
		if !ok {
			return d.HumanValue, nil
		}
		if spec.Taper == nil {
			return wf, nil
		}
		scale := spec.ReadScale
		if scale == 0 {
			scale = 1
		}
		return spec.Taper.FromWire(wf / scale), nil
	default:
		return d.HumanValue, nil
	}
}

// numeric coerces the common numeric wire/human types to float64.
func numeric(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}

// printPlan prints the ordered write plan (path + human value), or a JSON list.
func printPlan(g *globals, desired []boardconfig.Desired) {
	if g.json {
		rows := make([]map[string]any, len(desired))
		for i, d := range desired {
			rows[i] = map[string]any{"path": d.Path, "value": jsonValue(d.HumanValue)}
		}
		_ = printJSON(map[string]any{"action": "apply", "dryRun": true, "plan": rows})
		return
	}
	fmt.Println(ui.Header(fmt.Sprintf("write plan: %d settings (dry run)", len(desired))))
	rows := make([][2]string, len(desired))
	for i, d := range desired {
		rows[i] = [2]string{d.Path, displayValue(d.HumanValue)}
	}
	fmt.Println(ui.Table(rows))
}

// reportApply prints the post-apply verify result and returns the exit code:
// nil (0) when the fresh-connection verify is clean, exitError(1) on residual
// drift.
func reportApply(g *globals, written int, drift []boardconfig.Mismatch) error {
	if g.json {
		if err := printJSON(map[string]any{
			"action":  "apply",
			"written": written,
			"clean":   len(drift) == 0,
			"drift":   driftJSON(drift),
		}); err != nil {
			return err
		}
	} else if len(drift) == 0 {
		fmt.Println(ui.Success(fmt.Sprintf("applied %d settings; verify clean", written)))
	} else {
		fmt.Println(ui.Header(fmt.Sprintf("applied %d settings; %d still differ", written, len(drift))))
		fmt.Println(renderDrift(drift))
	}
	if len(drift) > 0 {
		return &exitError{code: 1}
	}
	return nil
}
