// Package schema is a data-driven table of known UCNET keys for the StudioLive
// Series III board. Each row (a [KeySpec]) records how to encode a write and
// humanize a read for one key family: its wire kind, whether it is writable,
// the ×100 read quirk on volume fields, and the taper that converts its 0..1
// wire position to human units.
//
// The table adds safety and units; it never gates access. [Lookup] returns
// (spec, true) for a known key and (zero, false) for anything else — an unknown
// key is a raw pass-through value, not an error, so the library never breaks on
// new firmware keys.
//
// Seeded from the "Verified WRITE operations" table in
// knowledge/ucnet-studiolive/protocol-and-port-plan.md.
package schema

import (
	"regexp"
	"strings"

	"github.com/steveclarke/ucmix/internal/taper"
)

// Kind is the wire representation of a key's value.
type Kind int

const (
	// KindFloat is a normalized float on the wire (PV). Most are 0..1 and reach
	// human units through a Taper; some are raw enums or already-human floats.
	KindFloat Kind = iota
	// KindBool is a float 1.0/0.0 on the wire (PV) treated as on/off.
	KindBool
	// KindString is a UTF-8 string with a trailing null (PS): names, icon ids.
	KindString
	// KindChars is a byte payload (PC): channel color as hex + alpha.
	KindChars
)

// KeySpec describes one known key family.
type KeySpec struct {
	// Pattern is the path template. {n} and {m} each match one run of digits;
	// {A..H} matches one FX-send letter A–H. Matching is anchored to the whole
	// path.
	Pattern string
	// Kind is the wire representation.
	Kind Kind
	// Writable reports whether the key is in the verified-write table.
	Writable bool
	// ReadScale divides a raw read to reach the stored value: 1 normally, 100
	// for the */volume read quirk (reads 0..100, wire wants 0..1).
	ReadScale float64
	// Taper converts the 0..1 wire position to human units. nil = raw
	// pass-through (the value carries no dB/Hz/input meaning, or the curve is
	// undecoded).
	Taper taper.Taper
}

// specs is the seed table: one row per verified-write key family. Keys that are
// bare 0..1 floats with no decoded human unit (pan, preampgain, auxpremode, the
// reverb type enum, EQ/comp params) carry a nil Taper — a documented raw
// pass-through, not an oversight.
var specs = []KeySpec{
	// --- line/chN — channel strip ---
	{Pattern: "line/ch{n}/mute", Kind: KindBool, Writable: true, ReadScale: 1},
	{Pattern: "line/ch{n}/solo", Kind: KindBool, Writable: true, ReadScale: 1},
	{Pattern: "line/ch{n}/48v", Kind: KindBool, Writable: true, ReadScale: 1},
	{Pattern: "line/ch{n}/polarity", Kind: KindBool, Writable: true, ReadScale: 1},
	{Pattern: "line/ch{n}/lr", Kind: KindBool, Writable: true, ReadScale: 1},
	{Pattern: "line/ch{n}/link", Kind: KindBool, Writable: true, ReadScale: 1},
	{Pattern: "line/ch{n}/linkmaster", Kind: KindBool, Writable: true, ReadScale: 1},
	{Pattern: "line/ch{n}/panlinkstate", Kind: KindBool, Writable: true, ReadScale: 1},
	{Pattern: "line/ch{n}/assign_fx{m}", Kind: KindBool, Writable: true, ReadScale: 1},
	// volume carries the ×100 read quirk (reads 0..100, wire wants 0..1).
	{Pattern: "line/ch{n}/volume", Kind: KindFloat, Writable: true, ReadScale: 100, Taper: taper.Fader},
	// aux{m} = monitor send; FX{A..H} = reverb send. Both use the send taper.
	{Pattern: "line/ch{n}/aux{m}", Kind: KindFloat, Writable: true, ReadScale: 1, Taper: taper.SendLevel},
	{Pattern: "line/ch{n}/FX{A..H}", Kind: KindFloat, Writable: true, ReadScale: 1, Taper: taper.SendLevel},
	{Pattern: "line/ch{n}/adc_src", Kind: KindFloat, Writable: true, ReadScale: 1, Taper: taper.InputPatch},
	{Pattern: "line/ch{n}/filter/hpf", Kind: KindFloat, Writable: true, ReadScale: 1, Taper: taper.HPF},
	// pan, preampgain: raw 0..1, no decoded human unit in the table → nil taper.
	{Pattern: "line/ch{n}/pan", Kind: KindFloat, Writable: true, ReadScale: 1},
	{Pattern: "line/ch{n}/preampgain", Kind: KindFloat, Writable: true, ReadScale: 1},
	{Pattern: "line/ch{n}/username", Kind: KindString, Writable: true, ReadScale: 1},
	{Pattern: "line/ch{n}/iconid", Kind: KindString, Writable: true, ReadScale: 1},
	{Pattern: "line/ch{n}/color", Kind: KindChars, Writable: true, ReadScale: 1},
	// EQ: 6-band parametric. Representative families gain/freq/Q ({b} = band
	// number). Raw floats — no human taper decoded.
	{Pattern: "line/ch{n}/eq/eqgain{m}", Kind: KindFloat, Writable: true, ReadScale: 1},
	{Pattern: "line/ch{n}/eq/eqfreq{m}", Kind: KindFloat, Writable: true, ReadScale: 1},
	{Pattern: "line/ch{n}/eq/eqq{m}", Kind: KindFloat, Writable: true, ReadScale: 1},
	// Comp: on is bool, the rest are raw floats.
	{Pattern: "line/ch{n}/comp/on", Kind: KindBool, Writable: true, ReadScale: 1},
	{Pattern: "line/ch{n}/comp/threshold", Kind: KindFloat, Writable: true, ReadScale: 1},
	{Pattern: "line/ch{n}/comp/ratio", Kind: KindFloat, Writable: true, ReadScale: 1},
	{Pattern: "line/ch{n}/comp/attack", Kind: KindFloat, Writable: true, ReadScale: 1},
	{Pattern: "line/ch{n}/comp/release", Kind: KindFloat, Writable: true, ReadScale: 1},
	{Pattern: "line/ch{n}/comp/gain", Kind: KindFloat, Writable: true, ReadScale: 1},

	// --- aux/chN — monitor mix master ---
	// volume carries the same ×100 read quirk as line volume.
	{Pattern: "aux/ch{n}/volume", Kind: KindFloat, Writable: true, ReadScale: 100, Taper: taper.Fader},
	{Pattern: "aux/ch{n}/username", Kind: KindString, Writable: true, ReadScale: 1},
	// link/linkmaster read as float on aux but encode 1.0/0.0 → keep KindBool.
	{Pattern: "aux/ch{n}/link", Kind: KindBool, Writable: true, ReadScale: 1},
	{Pattern: "aux/ch{n}/linkmaster", Kind: KindBool, Writable: true, ReadScale: 1},
	// auxpremode: 0.5 = Pre 2 — an enum-ish raw float, no taper.
	{Pattern: "aux/ch{n}/auxpremode", Kind: KindFloat, Writable: true, ReadScale: 1},
	{Pattern: "aux/ch{n}/limit/limiteron", Kind: KindBool, Writable: true, ReadScale: 1},
	{Pattern: "aux/ch{n}/limit/threshold", Kind: KindFloat, Writable: true, ReadScale: 1, Taper: taper.LimiterThresh},
	{Pattern: "aux/ch{n}/limit/release", Kind: KindFloat, Writable: true, ReadScale: 1, Taper: taper.Release},

	// --- fx/chN — FX bus ---
	// type: 0.375 = Vintage Plate — a raw enum float, taper nil for now.
	{Pattern: "fx/ch{n}/type", Kind: KindFloat, Writable: true, ReadScale: 1},

	// --- fxreturn/chN — FX return ---
	{Pattern: "fxreturn/ch{n}/username", Kind: KindString, Writable: true, ReadScale: 1},
	{Pattern: "fxreturn/ch{n}/aux{m}", Kind: KindFloat, Writable: true, ReadScale: 1, Taper: taper.SendLevel},
	{Pattern: "fxreturn/ch{n}/mute", Kind: KindBool, Writable: true, ReadScale: 1},
}

// compiled pairs each spec with its anchored regexp, built once at init.
type compiled struct {
	re   *regexp.Regexp
	spec KeySpec
}

var table = compile(specs)

func compile(rows []KeySpec) []compiled {
	out := make([]compiled, len(rows))
	for i, r := range rows {
		out[i] = compiled{re: regexp.MustCompile(patternToRegex(r.Pattern)), spec: r}
	}
	return out
}

// patternToRegex turns a path template into an anchored regexp. Literal text is
// escaped; {n}/{m} become one run of digits; {A..H} becomes one letter A–H.
func patternToRegex(pattern string) string {
	var b strings.Builder
	b.WriteString("^")
	for {
		open := strings.IndexByte(pattern, '{')
		if open < 0 {
			b.WriteString(regexp.QuoteMeta(pattern))
			break
		}
		b.WriteString(regexp.QuoteMeta(pattern[:open]))
		close := strings.IndexByte(pattern[open:], '}')
		if close < 0 { // no closing brace — treat the rest as literal
			b.WriteString(regexp.QuoteMeta(pattern[open:]))
			break
		}
		token := pattern[open+1 : open+close]
		b.WriteString(tokenToClass(token))
		pattern = pattern[open+close+1:]
	}
	b.WriteString("$")
	return b.String()
}

// tokenToClass maps a placeholder token to its regexp fragment. "A..H" (or any
// X..Y range) becomes a character class; everything else (n, m, b, …) is a run
// of digits.
func tokenToClass(token string) string {
	if i := strings.Index(token, ".."); i >= 0 {
		lo, hi := token[:i], token[i+2:]
		if len(lo) == 1 && len(hi) == 1 {
			return "[" + regexp.QuoteMeta(lo) + "-" + regexp.QuoteMeta(hi) + "]"
		}
	}
	return `\d+`
}

// Lookup returns the KeySpec for a path and true if the path matches a known
// key family, or the zero KeySpec and false otherwise. Unknown keys are raw
// pass-through values, never an error.
func Lookup(path string) (KeySpec, bool) {
	for _, c := range table {
		if c.re.MatchString(path) {
			return c.spec, true
		}
	}
	return KeySpec{}, false
}
