package boardconfig

import (
	"testing"

	"github.com/steveclarke/ucmix/internal/taper"
)

// sendWire returns the snapshot wire value a monitor send would read for a given
// dB, using the same taper Compile uses (ReadScale 1 on sends).
func sendWire(t *testing.T, db float64) float64 {
	t.Helper()
	pos, err := taper.SendLevel.ToWire(db)
	if err != nil {
		t.Fatalf("SendLevel.ToWire(%v): %v", db, err)
	}
	return pos
}

// TestDiffTolerance pins the per-taper 0.5 dB band: a snapshot within tolerance
// is clean, just outside is a mismatch, and the mismatch is humanized.
func TestDiffTolerance(t *testing.T) {
	desired := []Desired{{Path: "line/ch1/aux1", WireValue: sendWire(t, -6), HumanValue: -6.0}}

	cases := []struct {
		name    string
		gotDB   float64 // dB the snapshot value decodes to
		wantMis bool
	}{
		{"inside band", -6.4, false}, // 0.4 dB off → clean
		{"outside band", -6.6, true}, // 0.6 dB off → mismatch
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snap := map[string]any{"line/ch1/aux1": sendWire(t, tc.gotDB)}
			mis := Diff(desired, snap)
			if tc.wantMis {
				if len(mis) != 1 {
					t.Fatalf("mismatches = %d, want 1", len(mis))
				}
				gotH, ok := mis[0].Got.(float64)
				if !ok || approx(gotH, tc.gotDB, 0.05) == false {
					t.Errorf("mismatch Got = %v, want humanized ~%v dB", mis[0].Got, tc.gotDB)
				}
			} else if len(mis) != 0 {
				t.Errorf("mismatches = %v, want none", mis)
			}
		})
	}
}

// TestDiffMissingKey reports a desired path absent from the snapshot with Got
// nil.
func TestDiffMissingKey(t *testing.T) {
	desired := []Desired{{Path: "line/ch1/mute", WireValue: true, HumanValue: true}}
	mis := Diff(desired, map[string]any{})
	if len(mis) != 1 {
		t.Fatalf("mismatches = %d, want 1", len(mis))
	}
	if mis[0].Got != nil {
		t.Errorf("Got = %v, want nil", mis[0].Got)
	}
	if mis[0].Want != true {
		t.Errorf("Want = %v, want true", mis[0].Want)
	}
}

// TestDiffIgnoresExtraKeys confirms snapshot keys outside the desired set never
// produce a mismatch.
func TestDiffIgnoresExtraKeys(t *testing.T) {
	desired := []Desired{{Path: "line/ch1/mute", WireValue: false, HumanValue: false}}
	snap := map[string]any{
		"line/ch1/mute":  false, // matches
		"line/ch1/48v":   true,  // not desired → ignored
		"line/ch99/solo": true,  // not desired → ignored
	}
	if mis := Diff(desired, snap); len(mis) != 0 {
		t.Errorf("mismatches = %v, want none (extra keys ignored)", mis)
	}
}

// TestDiffBoolAndString covers non-taper comparisons: a bool desired against a
// 1.0/0.0 snapshot float, and a color string.
func TestDiffBoolAndString(t *testing.T) {
	desired := []Desired{
		{Path: "line/ch1/link", WireValue: true, HumanValue: true},
		{Path: "line/ch1/color", WireValue: "4ed2ffff", HumanValue: "4ed2ff"},
	}
	snap := map[string]any{
		"line/ch1/link":  1.0,        // wire float truthy → matches bool true
		"line/ch1/color": "4ed2ffff", // matches
	}
	if mis := Diff(desired, snap); len(mis) != 0 {
		t.Errorf("mismatches = %v, want none", mis)
	}

	bad := map[string]any{
		"line/ch1/link":  0.0,        // false → mismatch
		"line/ch1/color": "000000ff", // mismatch
	}
	if mis := Diff(desired, bad); len(mis) != 2 {
		t.Errorf("mismatches = %d, want 2", len(mis))
	}
}

// TestDiffColorAcrossRepresentations is the issue #30 regression: a board
// reports a color as the ABGR-packed integer 4288910991 while the config
// declares it as hex 8f96a3. Every form of the same color must Diff clean, and
// a genuinely different color must still drift.
func TestDiffColorAcrossRepresentations(t *testing.T) {
	desired, err := Compile(Config{
		Version:  1,
		Channels: map[int]Channel{19: {Color: strp("8f96a3")}},
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	same := []struct {
		name string
		got  any
	}{
		{"packed int from a ZB snapshot", int64(4288910991)},
		{"packed int as int", int(4288910991)},
		{"packed int through a float", float64(4288910991)},
		{"RGBA bytes from a PC delta", []byte{0x8f, 0x96, 0xa3, 0xff}},
		{"canonical hex string", "8f96a3ff"},
		{"6-digit hex string", "8f96a3"},
		{"uppercase hex string", "8F96A3FF"},
	}
	for _, tc := range same {
		t.Run(tc.name, func(t *testing.T) {
			snap := map[string]any{"line/ch19/color": tc.got}
			if mis := Diff(desired, snap); len(mis) != 0 {
				t.Errorf("mismatches = %+v, want none (%v is 8f96a3ff)", mis, tc.got)
			}
		})
	}

	// A different color still drifts, and both sides of the row are canonical.
	snap := map[string]any{"line/ch19/color": int64(4278190335)} // ff0000ff
	mis := Diff(desired, snap)
	if len(mis) != 1 {
		t.Fatalf("mismatches = %d, want 1", len(mis))
	}
	if mis[0].Want != "8f96a3ff" || mis[0].Got != "ff0000ff" {
		t.Errorf("drift row = want %v got %v, want canonical 8f96a3ff / ff0000ff", mis[0].Want, mis[0].Got)
	}
}

// TestDiffColorDoesNotPanicOnByteSnapshot pins that two []byte values never
// reach the == comparison, which panics on uncomparable types.
func TestDiffColorDoesNotPanicOnByteSnapshot(t *testing.T) {
	desired := []Desired{{Path: "line/ch1/color", WireValue: []byte{1, 2, 3, 4}, HumanValue: "01020304"}}
	snap := map[string]any{"line/ch1/color": []byte{1, 2, 3, 4}}
	if mis := Diff(desired, snap); len(mis) != 0 {
		t.Errorf("mismatches = %v, want none", mis)
	}
}

func strp(s string) *string { return &s }
