package cli

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveclarke/ucmix/internal/errs"
	"github.com/steveclarke/ucmix/internal/schema"
)

// A write is only real once the board holds it. `set` and the noun verbs send the
// value and then read it back on a FRESH connection, reporting what the board
// actually took. This is the shape apply already uses for its post-write verify,
// and it exists for the same reason: the board silently clamps or rejects a value
// it will not accept, so success reported on send describes the request, not the
// result.
//
// The read-back must run on a new connection. The in-session read-back quirk
// (see Client.VerifyAgainst) means the connection that performed a write cannot
// confirm its own writes; a fresh ZB snapshot is required.

// addNoVerifyFlag registers --no-verify on a write command. The read-back costs
// one extra connection per command (not per write), so the escape hatch is for
// callers that want the send and nothing else — a script driving many separate
// invocations, or a board known to be slow to settle.
func addNoVerifyFlag(cmd *cobra.Command, g *globals) {
	cmd.Flags().BoolVar(&g.noVerify, "no-verify", false, "report the write on send, without reading the value back")
}

// reportWrites reads every write back (unless --no-verify) and reports what the
// board holds. It is the shared tail of every write command.
func reportWrites(ctx context.Context, g *globals, items []setItem) error {
	if g.noVerify {
		return reportSet(g, unverified(items))
	}
	results, err := verifyWrites(ctx, g, items)
	if err != nil {
		return errs.CLIError{
			Message: fmt.Sprintf("wrote %d settings but could not read them back: %v", len(items), err),
			Hint:    "the write was sent; re-read with `ucmix get`, or pass --no-verify",
		}
	}
	return reportSet(g, results)
}

// writeVerifyAttempts and writeVerifyBackoff bound the read-back retry. A board
// that is still committing has not drifted, it is late: the new connection's ZB
// can predate the write. A landed write returns on the first attempt with no
// added delay; a genuine mismatch survives every retry. Same shape and budget as
// apply's verifyAfterApply.
const (
	writeVerifyAttempts = 5
	writeVerifyBackoff  = 200 * time.Millisecond
)

// rawWriteTolerance bounds the read-back comparison for keys with no taper. The
// board stores a 0..1 position as a 32-bit float, so an exact equality check on
// the way back would report quantization as drift.
const rawWriteTolerance = 1e-3

// writeResult pairs one attempted write with what the board holds afterwards.
type writeResult struct {
	setItem
	got      any  // the read-back value, humanized like `get` (nil when absent)
	found    bool // the path was present in the read-back snapshot
	verified bool // a read-back ran at all (false under --no-verify)
	ok       bool // the board holds what was written
}

// unverified wraps writes that were sent without a read-back (--no-verify). They
// report as sent, which is all the caller asked for.
func unverified(items []setItem) []writeResult {
	out := make([]writeResult, len(items))
	for i, it := range items {
		out[i] = writeResult{setItem: it, ok: true}
	}
	return out
}

// verifyWrites re-reads every written path and reports what the board holds. It
// retries while anything still mismatches, so board settle latency reads as slow
// rather than as failure.
func verifyWrites(ctx context.Context, g *globals, items []setItem) ([]writeResult, error) {
	var results []writeResult
	for i := 0; i < writeVerifyAttempts; i++ {
		if i > 0 {
			select {
			case <-time.After(writeVerifyBackoff):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		var err error
		results, err = readBack(ctx, g, items)
		if err != nil {
			return nil, err
		}
		if allLanded(results) {
			return results, nil
		}
	}
	return results, nil
}

// readBack opens one fresh connection and reads every written path from its
// snapshot.
func readBack(ctx context.Context, g *globals, items []setItem) ([]writeResult, error) {
	c, err := g.dialClient(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = c.Close() }()

	out := make([]writeResult, len(items))
	for i, it := range items {
		got, found := c.Get(it.path)
		out[i] = writeResult{
			setItem:  it,
			got:      got,
			found:    found,
			verified: true,
			ok:       found && writeLanded(it.path, it.value, got),
		}
	}
	return out, nil
}

// allLanded reports whether every write in the set is on the board.
func allLanded(results []writeResult) bool {
	for _, r := range results {
		if !r.ok {
			return false
		}
	}
	return true
}

// writeLanded reports whether a read-back value is the value that was written.
// Byte payloads (channel color) compare by their hex rendering, numbers within
// the control's tolerance, and everything else by its rendered form.
func writeLanded(path string, want, got any) bool {
	if _, isBytes := want.([]byte); !isBytes {
		if wf, wok := numericOrBool(want); wok {
			if gf, gok := numericOrBool(got); gok {
				return math.Abs(wf-gf) <= writeTolerance(path)
			}
		}
	}
	return displayValue(want) == displayValue(got)
}

// writeTolerance is how far a read-back may sit from the written value. A tapered
// key compares in its human unit, where the tolerance already accounts for wire
// quantization; anything else compares as a raw wire position.
func writeTolerance(path string) float64 {
	if spec, known := schema.Lookup(path); known && spec.Taper != nil {
		return schema.Tolerance(spec.Taper.Unit())
	}
	return rawWriteTolerance
}

// numericOrBool coerces a written or read-back value to a number, mapping bools
// to 1/0 so a bool write compares against a 1.0/0.0 wire float.
func numericOrBool(v any) (float64, bool) {
	if b, ok := v.(bool); ok {
		if b {
			return 1, true
		}
		return 0, true
	}
	return numeric(v)
}
