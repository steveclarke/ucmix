package proto

import (
	"bytes"
	"testing"
)

func TestMarshalUMUsesLittleEndianUDPPort(t *testing.T) {
	got := MarshalUM(58395)
	want := []byte{0x1b, 0xe4}
	if !bytes.Equal(got, want) {
		t.Fatalf("MarshalUM = % x, want % x", got, want)
	}
	port, err := UnmarshalUM(got)
	if err != nil || port != 58395 {
		t.Fatalf("UnmarshalUM = %d, %v", port, err)
	}
}

func TestUnmarshalUMRequiresTwoBytes(t *testing.T) {
	for _, payload := range [][]byte{nil, {1}, {1, 2, 3}} {
		if _, err := UnmarshalUM(payload); err == nil {
			t.Fatalf("UnmarshalUM(% x) succeeded", payload)
		}
	}
}
