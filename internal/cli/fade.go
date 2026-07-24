package cli

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveclarke/ucmix/internal/errs"
	"github.com/steveclarke/ucmix/internal/schema"
	"github.com/steveclarke/ucmix/internal/ui"
)

// offFloorDB is the dB target for `fade <path> off` — the fader taper's bottom
// anchor, which maps to wire 0 (silence).
const offFloorDB = -84.0

// fadeRate is the step cadence: ~33 steps/second is smooth to the ear and well
// within the transport's pacing.
const fadeStep = 30 * time.Millisecond

// newFadeCmd builds `fade <path> <target> [--over d]`: smoothly ramp a dB value
// (a fader or send) from its current level to a target over a duration, by
// streaming stepped writes on one held-open connection. UCNET has no native
// ramp; this is the client-side fade UC Surface also performs. Steps are linear
// in dB so the move sounds even.
func newFadeCmd(g *globals) *cobra.Command {
	var over time.Duration
	cmd := &cobra.Command{
		Use:   "fade <path> <target|off> [--over 2s]",
		Short: "Smoothly ramp a fader/send to a target dB",
		Long: "Smoothly ramp a dB value (fader or send) to a target over a duration.\n\n" +
			"Target is a dB value (e.g. -6, +3 for relative) or `off` (silence).\n\n" +
			"Example:\n  ucmix fade line/ch10/volume off --over 3s\n" +
			"  ucmix fade line/ch10/volume -6 --over 2s",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := normalizePath(args[0])
			ctx := cmd.Context()

			c, err := g.dialClient(ctx)
			if err != nil {
				return err
			}
			defer func() { _ = c.Close() }()

			// Derive the current dB from the raw wire through the taper, not from
			// the humanized read: volume's humanized read is corrupted by a
			// firmware-dependent read-scale, but the raw wire and the taper are
			// correct.
			raw, ok := numericValue(c.GetRaw(path))
			if !ok {
				return errs.CLIError{
					Message: fmt.Sprintf("cannot fade %s: it has no readable value", path),
					Hint:    "fade targets a fader or send (a dB path)",
				}
			}
			cur := raw
			if spec, known := schema.Lookup(path); known && spec.Taper != nil {
				cur = spec.Taper.FromWire(raw)
			}

			target, err := fadeTarget(args[1], cur)
			if err != nil {
				return errs.CLIError{Message: err.Error()}
			}

			if over <= 0 {
				over = 2 * time.Second
			}
			steps := int(over / fadeStep)
			if steps < 2 {
				steps = 2
			}
			interval := over / time.Duration(steps)

			if !g.json {
				fmt.Println(ui.Header(fmt.Sprintf("fading %s: %.1f → %.1f dB over %s", path, cur, target, over)))
			}

			for i := 1; i <= steps; i++ {
				v := cur + (target-cur)*float64(i)/float64(steps)
				if err := c.Set(ctx, path, v); err != nil {
					return errs.CLIError{Message: fmt.Sprintf("fade write failed: %v", err)}
				}
				if i < steps {
					select {
					case <-time.After(interval):
					case <-ctx.Done():
						return ctx.Err()
					}
				}
			}

			// Write barrier: hold briefly so the final value commits before close.
			select {
			case <-time.After(400 * time.Millisecond):
			case <-ctx.Done():
			}

			if g.json {
				return printJSON(map[string]any{"path": path, "from": cur, "to": target, "over": over.String(), "ok": true})
			}
			fmt.Println(ui.Success(fmt.Sprintf("faded %s to %.1f dB", path, target)))
			return nil
		},
	}
	cmd.Flags().DurationVar(&over, "over", 2*time.Second, "fade duration")
	cmd.Flags().SetInterspersed(false) // let -6 / +3 targets through as positionals
	return cmd
}

// fadeTarget resolves the target argument to an absolute dB value. `off` is the
// silence floor; a leading +/- is relative to the current level; otherwise it is
// an absolute dB number (a trailing "dB" is tolerated).
func fadeTarget(arg string, cur float64) (float64, error) {
	arg = strings.TrimSpace(arg)
	if strings.EqualFold(arg, "off") {
		return offFloorDB, nil
	}
	num := strings.TrimSuffix(strings.TrimSuffix(arg, "dB"), "db")
	f, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid fade target %q (want a dB number, +N, or off)", arg)
	}
	// A leading '+' is relative-up. A leading '-' is an absolute dB value (fader
	// levels are normally negative), not relative-down.
	if strings.HasPrefix(arg, "+") {
		return cur + f, nil
	}
	return f, nil
}

// numericValue coerces a humanized value to float64 (dB for faders/sends).
func numericValue(v any, ok bool) (float64, bool) {
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		if math.IsNaN(n) {
			return 0, false
		}
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	default:
		return 0, false
	}
}
