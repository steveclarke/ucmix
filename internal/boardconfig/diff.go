package boardconfig

import (
	"math"

	"github.com/steveclarke/ucmix/internal/schema"
)

// Mismatch is one desired path whose snapshot value differs from the desired
// value. Want and Got are humanized: dB/Hz/input for tapered keys, the wire
// value otherwise. Got is nil when the path is absent from the snapshot.
type Mismatch struct {
	Path string
	Want any
	Got  any
}

// tolerances by taper unit, in human units. Wire floats quantize, so tapered
// comparisons use a per-unit band rather than exact equality.
var tolerance = map[string]float64{
	"dB":    0.5,
	"Hz":    5,
	"input": 0.5,
	"ms":    5,
}

// floatEpsilon bounds equality for untapered raw floats (enum positions, raw
// passthrough), which are compared directly rather than in human units.
const floatEpsilon = 1e-6

// Diff compares a desired set against a board snapshot and returns the
// mismatches, in desired order. Only declared (desired) paths participate;
// snapshot keys absent from the desired set are ignored. Tapered floats compare
// in human units within a per-taper tolerance; everything else compares by
// value.
func Diff(desired []Desired, snapshot map[string]any) []Mismatch {
	var out []Mismatch
	for _, d := range desired {
		got, ok := snapshot[d.Path]
		if !ok {
			out = append(out, Mismatch{Path: d.Path, Want: d.HumanValue, Got: nil})
			continue
		}
		spec, known := schema.Lookup(d.Path)
		if known && spec.Taper != nil {
			wantH, okW := asFloat(d.HumanValue)
			gotRaw, okG := asFloat(got)
			if okW && okG {
				scale := spec.ReadScale
				if scale == 0 {
					scale = 1
				}
				gotH := spec.Taper.FromWire(gotRaw / scale)
				if math.Abs(wantH-gotH) > tolerance[spec.Taper.Unit()] {
					out = append(out, Mismatch{Path: d.Path, Want: wantH, Got: gotH})
				}
				continue
			}
			// Fall through to value comparison for off/raw human forms.
		}
		if !valuesEqual(d.WireValue, got) {
			out = append(out, Mismatch{Path: d.Path, Want: humanOrWire(d), Got: got})
		}
	}
	return out
}

// humanOrWire prefers the human value for the diff row, falling back to the
// wire value when they are the same untapered scalar.
func humanOrWire(d Desired) any {
	if d.HumanValue != nil {
		return d.HumanValue
	}
	return d.WireValue
}

// valuesEqual compares two wire values loosely: numbers within an epsilon,
// bool/number cross-type by truthiness, everything else by ==.
func valuesEqual(a, b any) bool {
	af, aok := asFloat(a)
	bf, bok := asFloat(b)
	if aok && bok {
		return math.Abs(af-bf) <= floatEpsilon
	}
	return a == b
}

// asFloat coerces numeric and bool wire values to float64. Booleans map to
// 1/0 so a bool desired compares against a 1.0/0.0 snapshot float.
func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case bool:
		if n {
			return 1, true
		}
		return 0, true
	default:
		return 0, false
	}
}
