package cli

import "testing"

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
