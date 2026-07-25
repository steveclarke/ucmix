package cli

import (
	"math"
	"testing"
)

// TestWriteLanded covers the comparison the post-write read-back turns on: a
// value the board took versus one it clamped, rejected, or never had.
func TestWriteLanded(t *testing.T) {
	cases := []struct {
		name string
		path string
		want any // what was written
		got  any // what the read-back holds, humanized like `get`
		land bool
	}{
		{"bool round-trips", "line/ch1/mute", true, true, true},
		{"bool flipped by the board", "line/ch1/mute", true, false, false},
		{"string round-trips", "line/ch1/username", "Kick", "Kick", true},
		{"string truncated by the board", "line/ch1/username", "Kick Drum", "Kick Dru", false},
		// Color is a byte payload on both sides; it compares by its hex rendering,
		// not by == (which panics on a slice).
		{
			name: "color round-trips",
			path: "line/ch1/color",
			want: []byte{0x4e, 0xd2, 0xff, 0xff},
			got:  []byte{0x4e, 0xd2, 0xff, 0xff},
			land: true,
		},
		{
			name: "color differs",
			path: "line/ch1/color",
			want: []byte{0x4e, 0xd2, 0xff, 0xff},
			got:  []byte{0x94, 0x78, 0xce, 0xff},
			land: false,
		},
		// A dB write comes back through a 32-bit wire position, so a hair of drift
		// is the wire, not the board disagreeing.
		{"fader within the dB tolerance", "line/ch1/volume", -6.0, -6.0001, true},
		{"fader pinned to the top", "line/ch1/volume", -6.0, 10.0, false},
		// The case this whole path exists for: Hz written straight to a 0..1
		// control, pinned by the board, reported as success before the read-back.
		{"hpf clamped by the board", "line/ch3/filter/hpf", 90.0, 1.0, false},
		// An untapered key compares as a raw wire position.
		{"raw float within quantization", "line/ch1/pan", 0.5, 0.50000001, true},
		{"raw float clearly different", "line/ch1/pan", 0.5, 0.9, false},
		// An unknown key is a raw pass-through in both directions.
		{"unknown key round-trips", "some/new/key", 0.25, 0.25, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := writeLanded(tc.path, tc.want, tc.got); got != tc.land {
				t.Errorf("writeLanded(%s, %v, %v) = %v, want %v", tc.path, tc.want, tc.got, got, tc.land)
			}
		})
	}
}

// TestWriteTolerance checks that a tapered key compares in its human unit and
// everything else as a raw wire position.
func TestWriteTolerance(t *testing.T) {
	cases := []struct {
		path string
		want float64
	}{
		{"line/ch1/volume", 0.5},            // dB
		{"line/ch3/filter/hpf", 5},          // Hz
		{"line/ch1/adc_src", 0.5},           // input
		{"aux/ch1/limit/release", 5},        // ms
		{"line/ch1/pan", rawWriteTolerance}, // known key, no taper
		{"some/new/key", rawWriteTolerance}, // unknown key
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			if got := writeTolerance(tc.path); got != tc.want {
				t.Errorf("writeTolerance(%s) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

// TestUnverifiedReportsAsSent locks --no-verify: the writes report as sent, and
// nothing claims to know what the board holds.
func TestUnverifiedReportsAsSent(t *testing.T) {
	items := []setItem{
		{path: "line/ch1/mute", value: true, raw: "on"},
		{path: "line/ch3/filter/hpf", value: 90.0, raw: "90"},
	}
	results := unverified(items)
	if len(results) != len(items) {
		t.Fatalf("unverified returned %d results, want %d", len(results), len(items))
	}
	for _, r := range results {
		if !r.ok {
			t.Errorf("%s: ok = false, want true (a send with no read-back is not a failure)", r.path)
		}
		if r.verified {
			t.Errorf("%s: verified = true, want false", r.path)
		}
		if row := setJSONRow(r); row["got"] != nil {
			t.Errorf("%s: JSON carries got = %v, want it omitted with no read-back", r.path, row["got"])
		}
	}
	if !allLanded(results) {
		t.Error("allLanded = false for an unverified batch, want true")
	}
}

// TestHeldValueNamesBothValues locks the mismatch line the issue asks for: what
// was written and what the board holds instead.
func TestHeldValueNamesBothValues(t *testing.T) {
	clamped := writeResult{
		setItem:  setItem{path: "line/ch3/filter/hpf", value: 90.0, raw: "90Hz"},
		got:      1.0,
		found:    true,
		verified: true,
	}
	if got, want := heldValue(clamped), "wrote 90, board holds 1"; got != want {
		t.Errorf("heldValue = %q, want %q", got, want)
	}

	absent := writeResult{
		setItem:  setItem{path: "line/ch99/mute", value: true, raw: "on"},
		verified: true,
	}
	if got, want := heldValue(absent), "wrote true, board holds nothing (path absent)"; got != want {
		t.Errorf("heldValue(absent) = %q, want %q", got, want)
	}
}

// TestSettledValue covers the read-back comparing against what the control can
// actually hold. A value past the end of a control's travel settles at the end;
// the board doing that is the control working, not the write failing. Reported
// against the raw written value instead, `set line/ch1/filter/hpf 0` — a filter
// off — would read back as 24 Hz and be called a mismatch.
//
// Numbers compare within the control's tolerance, never exactly. A settled value
// is a round trip through a logarithm or an interpolation, so the last bits
// depend on the platform's libm: 90 Hz settles to 90.00000000000001 on
// linux/amd64 and to 90 on darwin/arm64. Both are the same setting, which is
// what the tolerance means.
func TestSettledValue(t *testing.T) {
	cases := []struct {
		name string
		path string
		want any
		got  any
	}{
		{"hpf off settles at the bottom of the sweep", "line/ch3/filter/hpf", 0.0, 24.0},
		{"hpf below the sweep settles at the bottom", "line/ch3/filter/hpf", 20.0, 24.0},
		{"hpf in range is unchanged", "line/ch3/filter/hpf", 90.0, 90.0},
		{"fader below the floor settles at the floor", "line/ch1/volume", -200.0, -84.0},
		{"fader in range is unchanged", "line/ch1/volume", -6.0, -6.0},
		// Above the top never reaches the board — Set errors first — so it is
		// reported against what was asked for.
		{"above the top is left alone", "line/ch3/filter/hpf", 2000.0, 2000.0},
		// Untapered and non-float keys have no travel to settle into.
		{"untapered float is unchanged", "line/ch1/pan", 5.0, 5.0},
		{"bool is unchanged", "line/ch1/mute", true, true},
		{"string is unchanged", "line/ch1/username", "Kick", "Kick"},
		{"unknown key is unchanged", "some/new/key", 90.0, 90.0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := settledValue(tc.path, tc.want)
			if wf, ok := tc.got.(float64); ok {
				gf, isFloat := got.(float64)
				if !isFloat {
					t.Fatalf("settledValue(%s, %v) = %v (%T), want a float", tc.path, tc.want, got, got)
				}
				if tol := writeTolerance(tc.path); math.Abs(gf-wf) > tol {
					t.Errorf("settledValue(%s, %v) = %v, want %v (±%v)", tc.path, tc.want, gf, wf, tol)
				}
				return
			}
			if got != tc.got {
				t.Errorf("settledValue(%s, %v) = %v, want %v", tc.path, tc.want, got, tc.got)
			}
		})
	}
}

// TestSettledValueIsWhatTheReadBackCompares ties settledValue to the comparison
// it feeds: a write that settles must land, whatever the platform's libm does to
// the last bits, and a write the board pinned must not.
func TestSettledValueIsWhatTheReadBackCompares(t *testing.T) {
	const hpf = "line/ch3/filter/hpf"
	// 90 Hz written, 90 Hz on the board (a 32-bit position inverted).
	if !writeLanded(hpf, settledValue(hpf, 90.0), 90.00000306137774) {
		t.Error("90 Hz written and held did not land")
	}
	// 0 Hz written — a filter off — settles at the bottom of the sweep.
	if !writeLanded(hpf, settledValue(hpf, 0.0), 24.0) {
		t.Error("hpf off did not land")
	}
	// The bug: Hz written straight through, board pinned to the top of the sweep.
	if writeLanded(hpf, settledValue(hpf, 90.0), 1000.0) {
		t.Error("a filter pinned to the top of its sweep counted as landed")
	}
}

// TestHumanizeRoundsTaperedReads is the display rule: a read should look like the
// value that was set. Inverting a 32-bit wire position leaves float noise whose
// exact digits differ by platform, so tapered values round on the way out.
func TestHumanizeRoundsTaperedReads(t *testing.T) {
	cases := []struct {
		name string
		path string
		in   any
		want any
	}{
		{"Hz noise rounds to the dialed value", "line/ch3/filter/hpf", 90.00000306137774, 90.0},
		{"dB noise rounds to the dialed value", "line/ch1/volume", -6.000000847568462, -6.0},
		{"a tenth is kept", "aux/ch1/volume", -31.749999, -31.7},
		// A raw wire position has no human unit; rounding it to a tenth would
		// destroy it.
		{"untapered position is untouched", "line/ch1/pan", 0.3543863892555237, 0.3543863892555237},
		{"unknown key is untouched", "some/new/key", 0.123456789, 0.123456789},
		// Non-floats have nothing to round.
		{"bool is untouched", "line/ch1/mute", true, true},
		{"string is untouched", "line/ch1/username", "Kick", "Kick"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := humanize(tc.path, tc.in); got != tc.want {
				t.Errorf("humanize(%s, %v) = %v, want %v", tc.path, tc.in, got, tc.want)
			}
		})
	}
}
