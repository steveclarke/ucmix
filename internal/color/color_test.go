package color

import (
	"bytes"
	"testing"
)

// TestCanonicalFoldsEveryForm is the core contract: every shape a color arrives
// in folds to the same 8-digit lowercase RGBA hex string. 4288910991 is the
// ABGR-packed integer a real 32R reports for 8f96a3ff.
func TestCanonicalFoldsEveryForm(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
	}{
		{"6-digit hex gains opaque alpha", "8f96a3", "8f96a3ff"},
		{"8-digit hex passes through", "8f96a3ff", "8f96a3ff"},
		{"uppercase lowercases", "8F96A3FF", "8f96a3ff"},
		{"leading hash is stripped", "#8f96a3", "8f96a3ff"},
		{"packed int from a snapshot", int64(4288910991), "8f96a3ff"},
		{"packed int as int", int(4288910991), "8f96a3ff"},
		{"packed int as uint32", uint32(4288910991), "8f96a3ff"},
		{"packed int through a float (JSON)", float64(4288910991), "8f96a3ff"},
		{"RGBA bytes from a PC delta", []byte{0x8f, 0x96, 0xa3, 0xff}, "8f96a3ff"},
		{"RGB bytes gain opaque alpha", []byte{0x8f, 0x96, 0xa3}, "8f96a3ff"},
		{"unset color is transparent black", int64(0), "00000000"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Canonical(tc.in)
			if !ok {
				t.Fatalf("Canonical(%#v) reported not-a-color", tc.in)
			}
			if got != tc.want {
				t.Errorf("Canonical(%#v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestCanonicalRejectsNonColors(t *testing.T) {
	tests := []struct {
		name string
		in   any
	}{
		{"short hex", "8f9"},
		{"non-hex digits", "zzzzzz"},
		{"wrong byte count", []byte{0x8f, 0x96}},
		{"fractional float", 0.746},
		{"bool", true},
		{"nil", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got, ok := Canonical(tc.in); ok {
				t.Errorf("Canonical(%#v) = %q, true; want not-a-color", tc.in, got)
			}
		})
	}
}

func TestParseProducesRGBAWireBytes(t *testing.T) {
	tests := []struct {
		in   string
		want []byte
	}{
		{"4ed2ff", []byte{0x4e, 0xd2, 0xff, 0xff}},
		{"#4ed2ff80", []byte{0x4e, 0xd2, 0xff, 0x80}},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got, err := Parse(tc.in)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.in, err)
			}
			if !bytes.Equal(got, tc.want) {
				t.Errorf("Parse(%q) = %x, want %x", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseRejectsNonHex(t *testing.T) {
	if _, err := Parse("nothex"); err == nil {
		t.Error("Parse(\"nothex\") = nil error, want an error")
	}
}

// TestCanonicalRoundTripsThroughWireBytes pins Parse and Canonical as inverses:
// hex → wire bytes → packed int (what the board stores) → the same hex.
func TestCanonicalRoundTripsThroughWireBytes(t *testing.T) {
	raw, err := Parse("8f96a3")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	packed := int64(uint32(raw[0]) | uint32(raw[1])<<8 | uint32(raw[2])<<16 | uint32(raw[3])<<24)
	if packed != 4288910991 {
		t.Fatalf("packed = %d, want 4288910991 (what a real 32R reports)", packed)
	}
	got, ok := Canonical(packed)
	if !ok || got != "8f96a3ff" {
		t.Errorf("Canonical(%d) = %q, %v; want \"8f96a3ff\", true", packed, got, ok)
	}
}
