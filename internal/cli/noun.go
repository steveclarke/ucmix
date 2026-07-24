package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/steveclarke/ucmix/internal/errs"

	ucmix "github.com/steveclarke/ucmix"
)

// The noun-grouped commands (channel/mix/send) are a thin, human-facing veneer
// over `set`/`get`: each verb builds a path and writes it through the same
// setItem/Set path that `set` uses. The raw `set <path> <value>` model stays
// authoritative and covers every parameter; this layer only shortcuts the common
// actions and needs no path knowledge. Path construction is kept pure so the
// verb→path mapping is unit testable without a board.

// channelPath builds a line/ch{n} path with the given suffix.
func channelPath(n int, suffix string) string {
	return fmt.Sprintf("line/ch%d/%s", n, suffix)
}

// auxPath builds an aux/ch{n} path with the given suffix. Aux buses are the
// monitor mixes.
func auxPath(n int, suffix string) string {
	return fmt.Sprintf("aux/ch%d/%s", n, suffix)
}

// sendPath builds a channel's send into a monitor mix: line/ch{ch}/aux{mix}.
func sendPath(ch, mix int) string {
	return fmt.Sprintf("line/ch%d/aux%d", ch, mix)
}

// parseIndex parses a 1-based channel or mix number.
func parseIndex(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 1 {
		return 0, errs.CLIError{
			Message: fmt.Sprintf("invalid channel number %q", s),
			Hint:    "channels and mixes are numbered from 1",
		}
	}
	return n, nil
}

// maxMixScan bounds the aux-bus scan when resolving a mix by name. It covers the
// largest StudioLive bus count; a board simply exposes fewer of these paths.
const maxMixScan = 64

// resolveMixIndex turns a `mix` reference into an aux bus number. A number is
// used directly; a name is matched case-insensitively against aux usernames in
// the current snapshot, so a human can address "Steve's mix" by its scribble
// name instead of its bus number.
func resolveMixIndex(c *ucmix.Client, ref string) (int, error) {
	if n, err := strconv.Atoi(strings.TrimSpace(ref)); err == nil && n >= 1 {
		return n, nil
	}
	snap := c.Snapshot()
	want := strings.TrimSpace(ref)
	match := 0
	for n := 1; n <= maxMixScan; n++ {
		v, ok := snap[auxPath(n, "username")]
		if !ok {
			continue
		}
		name, ok := v.(string)
		if !ok || !strings.EqualFold(strings.TrimSpace(name), want) {
			continue
		}
		if match != 0 {
			return 0, errs.CLIError{
				Message: fmt.Sprintf("more than one mix is named %q", ref),
				Hint:    "address it by number instead",
			}
		}
		match = n
	}
	if match == 0 {
		return 0, errs.CLIError{
			Message: fmt.Sprintf("no mix named %q", ref),
			Hint:    "give a mix number, or name the mix first with `ucmix mix <n> name`",
		}
	}
	return match, nil
}

// applyNoun parses one value against the schema, writes it over the already-
// connected client, and reports the single write. The library holds the commit
// barrier, so the caller neither sleeps nor closes early. This is the shared
// write path for the single-value noun verbs (channel/mix/send).
func applyNoun(ctx context.Context, g *globals, c *ucmix.Client, path, value string) error {
	it, err := parseSetItem(path, value)
	if err != nil {
		return err
	}
	if err := c.Set(ctx, it.path, it.value); err != nil {
		return errs.CLIError{
			Message: fmt.Sprintf("could not set %s: %v", it.path, err),
			Hint:    "check the value is in range for this control",
		}
	}
	return reportSet(g, []setItem{it})
}
