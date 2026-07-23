package proto

import (
	"bytes"
	"testing"
)

func TestMarshalPVGolden(t *testing.T) {
	got := MarshalPV("line/ch1/mute", 1.0)
	want := []byte{
		'l', 'i', 'n', 'e', '/', 'c', 'h', '1', '/', 'm', 'u', 't', 'e',
		0x00, 0x00, 0x00, // separator: terminator + partA(00 00)
		0x00, 0x00, 0x80, 0x3f, // float32 LE 1.0
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("MarshalPV =\n% x\nwant\n% x", got, want)
	}
}

func TestMarshalPSGolden(t *testing.T) {
	got := MarshalPS("line/ch1/username", "Drums")
	want := append([]byte("line/ch1/username"), 0x00, 0x00, 0x00)
	want = append(want, []byte("Drums")...)
	want = append(want, 0x00) // trailing NUL
	if !bytes.Equal(got, want) {
		t.Fatalf("MarshalPS =\n% x\nwant\n% x", got, want)
	}
}

func TestMarshalPCGolden(t *testing.T) {
	raw := []byte{0x4e, 0xd2, 0xff, 0xff} // color hex + alpha
	got := MarshalPC("line/ch1/color", raw)
	want := append([]byte("line/ch1/color"), 0x00, 0x00, 0x00)
	want = append(want, raw...)
	if !bytes.Equal(got, want) {
		t.Fatalf("MarshalPC =\n% x\nwant\n% x", got, want)
	}
}

func TestPVRoundTrip(t *testing.T) {
	tests := []struct {
		key string
		val float32
	}{
		{"line/ch1/mute", 1.0},
		{"line/ch1/mute", 0.0},
		{"aux/ch3/volume", 0.746},
		{"fx/ch1/type", 0.375},
		{"main/ch1/volume", -0.5},
	}
	for _, tc := range tests {
		key, val, err := UnmarshalPV(MarshalPV(tc.key, tc.val))
		if err != nil {
			t.Fatalf("%s: %v", tc.key, err)
		}
		if key != tc.key || val != tc.val {
			t.Errorf("round-trip = (%q, %v), want (%q, %v)", key, val, tc.key, tc.val)
		}
	}
}

func TestPSRoundTrip(t *testing.T) {
	tests := []struct{ key, val string }{
		{"line/ch1/username", "Drums"},
		{"line/ch2/username", ""},
		{"line/ch3/iconid", "drums/drumset"},
		{"line/ch4/username", "Vocal — Steve"}, // multibyte UTF-8
	}
	for _, tc := range tests {
		key, val, err := UnmarshalPS(MarshalPS(tc.key, tc.val))
		if err != nil {
			t.Fatalf("%s: %v", tc.key, err)
		}
		if key != tc.key || val != tc.val {
			t.Errorf("round-trip = (%q, %q), want (%q, %q)", key, val, tc.key, tc.val)
		}
	}
}

func TestPCRoundTrip(t *testing.T) {
	tests := []struct {
		key string
		raw []byte
	}{
		{"line/ch1/color", []byte{0x4e, 0xd2, 0xff, 0xff}},
		{"line/ch2/color", []byte{0x00, 0x00, 0x00}},
		{"line/ch3/color", []byte{}},
	}
	for _, tc := range tests {
		key, raw, err := UnmarshalPC(MarshalPC(tc.key, tc.raw))
		if err != nil {
			t.Fatalf("%s: %v", tc.key, err)
		}
		if key != tc.key || !bytes.Equal(raw, tc.raw) {
			t.Errorf("round-trip = (%q, % x), want (%q, % x)", key, raw, tc.key, tc.raw)
		}
	}
}

// TestDecodePartA verifies decoders skip the 2-byte partA correctly and do not
// assume it is 00 00 — filter-group deltas use 00 01.
func TestDecodePartAVariant(t *testing.T) {
	// key "filtergroup/x" + 0x00 + partA(00 01) + float32 LE 1.0
	payload := append([]byte("filtergroup/x"), 0x00, 0x00, 0x01)
	payload = append(payload, 0x00, 0x00, 0x80, 0x3f)
	key, val, err := UnmarshalPV(payload)
	if err != nil {
		t.Fatal(err)
	}
	if key != "filtergroup/x" || val != 1.0 {
		t.Fatalf("got (%q, %v), want (filtergroup/x, 1)", key, val)
	}
}

func TestPayloadDecodeMalformed(t *testing.T) {
	t.Run("PV no terminator", func(t *testing.T) {
		if _, _, err := UnmarshalPV([]byte("nokey")); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("PV short value", func(t *testing.T) {
		// key + sep + only 2 value bytes
		p := append([]byte("k"), 0x00, 0x00, 0x00, 0x01, 0x02)
		if _, _, err := UnmarshalPV(p); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("PV truncated after key", func(t *testing.T) {
		p := append([]byte("k"), 0x00, 0x00) // missing full partA
		if _, _, err := UnmarshalPV(p); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("PS missing trailing NUL", func(t *testing.T) {
		p := append([]byte("k"), 0x00, 0x00, 0x00)
		p = append(p, []byte("val")...) // no trailing 0x00
		if _, _, err := UnmarshalPS(p); err == nil {
			t.Fatal("expected error")
		}
	})
}
