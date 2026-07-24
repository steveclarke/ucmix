package proto

import (
	"bytes"
	"os"
	"testing"
)

// TestMarshalFRMatchesCapture asserts the built list-projects request payload is
// byte-for-byte the one UC Surface sent to a real 32R (captured in
// testdata/uc-surface-listpresets.pcap, extracted to fr-listproj-request.bin).
// The connID differs per client and is not part of the payload, so the fixture
// is the payload only.
func TestMarshalFRMatchesCapture(t *testing.T) {
	want, err := os.ReadFile("testdata/fr-listproj-request.bin")
	if err != nil {
		t.Fatal(err)
	}
	got := MarshalFR(1, "Listpresets/proj", "")
	if !bytes.Equal(got, want) {
		t.Fatalf("FR payload =\n% x\nwant\n% x", got, want)
	}
}

// TestMarshalFRChannelArg exercises the two-cstr form: a non-empty arg lands
// between the resource terminator and the trailing null, matching the channel
// preset request from the capture.
func TestMarshalFRChannelArg(t *testing.T) {
	got := MarshalFR(2, "Listpresets/channel", "Vocal")
	want := append([]byte{0x02, 0x00}, "Listpresets/channel\x00Vocal\x00"...)
	if !bytes.Equal(got, want) {
		t.Fatalf("FR channel payload =\n% x\nwant\n% x", got, want)
	}
}
