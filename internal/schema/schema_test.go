package schema

import "testing"

// row asserts one concrete path resolves to the expected spec fields. wantUnit
// is checked only when non-empty (the taper's human unit); wantTaper records
// whether a taper should be present at all.
type row struct {
	name      string
	path      string
	wantKind  Kind
	wantTaper bool
	wantUnit  string
	wantScale float64
}

// One row per seeded KeySpec, using a canonical concrete path.
var rows = []row{
	// line/chN — channel strip
	{"line/mute", "line/ch1/mute", KindBool, false, "", 1},
	{"line/solo", "line/ch1/solo", KindBool, false, "", 1},
	{"line/48v", "line/ch1/48v", KindBool, false, "", 1},
	{"line/polarity", "line/ch1/polarity", KindBool, false, "", 1},
	{"line/lr", "line/ch1/lr", KindBool, false, "", 1},
	{"line/link", "line/ch1/link", KindBool, false, "", 1},
	{"line/linkmaster", "line/ch1/linkmaster", KindBool, false, "", 1},
	{"line/panlinkstate", "line/ch1/panlinkstate", KindBool, false, "", 1},
	{"line/assign_fx", "line/ch1/assign_fx1", KindBool, false, "", 1},
	{"line/volume", "line/ch12/volume", KindFloat, true, "dB", 100},
	{"line/aux", "line/ch3/aux2", KindFloat, true, "dB", 1},
	{"line/FX", "line/ch5/FXA", KindFloat, true, "dB", 1},
	{"line/adc_src", "line/ch9/adc_src", KindFloat, true, "input", 1},
	{"line/hpf", "line/ch1/filter/hpf", KindFloat, true, "Hz", 1},
	{"line/pan", "line/ch1/pan", KindFloat, false, "", 1},
	{"line/preampgain", "line/ch1/preampgain", KindFloat, false, "", 1},
	{"line/username", "line/ch1/username", KindString, false, "", 1},
	{"line/iconid", "line/ch1/iconid", KindString, false, "", 1},
	{"line/color", "line/ch1/color", KindChars, false, "", 1},
	{"line/eqgain", "line/ch1/eq/eqgain1", KindFloat, false, "", 1},
	{"line/eqfreq", "line/ch1/eq/eqfreq1", KindFloat, false, "", 1},
	{"line/eqq", "line/ch1/eq/eqq1", KindFloat, false, "", 1},
	{"line/comp/on", "line/ch1/comp/on", KindBool, false, "", 1},
	{"line/comp/threshold", "line/ch1/comp/threshold", KindFloat, false, "", 1},
	{"line/comp/ratio", "line/ch1/comp/ratio", KindFloat, false, "", 1},
	{"line/comp/attack", "line/ch1/comp/attack", KindFloat, false, "", 1},
	{"line/comp/release", "line/ch1/comp/release", KindFloat, false, "", 1},
	{"line/comp/gain", "line/ch1/comp/gain", KindFloat, false, "", 1},

	// aux/chN — monitor mix master
	{"aux/volume", "aux/ch1/volume", KindFloat, true, "dB", 100},
	{"aux/username", "aux/ch1/username", KindString, false, "", 1},
	{"aux/link", "aux/ch1/link", KindBool, false, "", 1},
	{"aux/linkmaster", "aux/ch1/linkmaster", KindBool, false, "", 1},
	{"aux/auxpremode", "aux/ch1/auxpremode", KindFloat, false, "", 1},
	{"aux/limiteron", "aux/ch1/limit/limiteron", KindBool, false, "", 1},
	{"aux/threshold", "aux/ch1/limit/threshold", KindFloat, true, "dB", 1},
	{"aux/release", "aux/ch1/limit/release", KindFloat, true, "ms", 1},

	// fx/chN — FX bus
	{"fx/type", "fx/ch1/type", KindFloat, false, "", 1},

	// fxreturn/chN — FX return
	{"fxreturn/username", "fxreturn/ch1/username", KindString, false, "", 1},
	{"fxreturn/aux", "fxreturn/ch1/aux2", KindFloat, true, "dB", 1},
	{"fxreturn/mute", "fxreturn/ch1/mute", KindBool, false, "", 1},
}

func TestLookupSeededKeys(t *testing.T) {
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			spec, ok := Lookup(r.path)
			if !ok {
				t.Fatalf("Lookup(%q) = _, false; want a match", r.path)
			}
			if spec.Kind != r.wantKind {
				t.Errorf("Kind = %v, want %v", spec.Kind, r.wantKind)
			}
			if !spec.Writable {
				t.Errorf("Writable = false; every seeded key is a verified write")
			}
			if spec.ReadScale != r.wantScale {
				t.Errorf("ReadScale = %v, want %v", spec.ReadScale, r.wantScale)
			}
			if (spec.Taper != nil) != r.wantTaper {
				t.Errorf("Taper present = %v, want %v", spec.Taper != nil, r.wantTaper)
			}
			if r.wantUnit != "" {
				if spec.Taper == nil {
					t.Fatalf("want taper unit %q but Taper is nil", r.wantUnit)
				}
				if got := spec.Taper.Unit(); got != r.wantUnit {
					t.Errorf("Taper.Unit() = %q, want %q", got, r.wantUnit)
				}
			}
		})
	}
}

// TestVolumeReadScale pins the ×100 read quirk on both volume families.
func TestVolumeReadScale(t *testing.T) {
	for _, path := range []string{"line/ch5/volume", "aux/ch3/volume"} {
		spec, ok := Lookup(path)
		if !ok {
			t.Fatalf("Lookup(%q) = _, false; want a match", path)
		}
		if spec.ReadScale != 100 {
			t.Errorf("Lookup(%q).ReadScale = %v, want 100", path, spec.ReadScale)
		}
	}
}

// TestMultiDigitChannels confirms {n} matches a full digit run, not one digit.
func TestMultiDigitChannels(t *testing.T) {
	for _, path := range []string{"line/ch1/volume", "line/ch32/volume", "line/ch128/volume"} {
		if _, ok := Lookup(path); !ok {
			t.Errorf("Lookup(%q) = _, false; want a match", path)
		}
	}
}

// TestFXSendLetterRange confirms {A..H} matches only A–H.
func TestFXSendLetterRange(t *testing.T) {
	for _, path := range []string{"line/ch1/FXA", "line/ch1/FXH"} {
		if _, ok := Lookup(path); !ok {
			t.Errorf("Lookup(%q) = _, false; want a match", path)
		}
	}
	for _, path := range []string{"line/ch1/FXI", "line/ch1/FXZ", "line/ch1/FX"} {
		if _, ok := Lookup(path); ok {
			t.Errorf("Lookup(%q) = _, true; want no match", path)
		}
	}
}

// TestUnknownKeys confirms non-matching and malformed paths return false —
// unknown keys are raw pass-through, not errors.
func TestUnknownKeys(t *testing.T) {
	cases := []string{
		"line/chx/volume",       // non-numeric channel
		"line/ch12/volumex",     // trailing junk
		"line/ch/volume",        // missing channel number
		"line/ch12/volume/x",    // extra segment
		"xline/ch12/volume",     // leading junk
		"line/ch12",             // partial path
		"line/ch5/somethingnew", // plausible but unmodeled key
		"",                      // empty
		"aux/ch1/volume100",     // trailing junk on aux volume
	}
	for _, path := range cases {
		if _, ok := Lookup(path); ok {
			t.Errorf("Lookup(%q) = _, true; want false", path)
		}
	}
}
