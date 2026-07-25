package proto_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/steveclarke/ucmix/internal/proto"
)

// The fixtures below are the exact frame payloads UC Surface and a real 32R
// exchanged for a store and a rename, carved out of
// testdata/uc-surface-store-rename.pcap. They pin the wire format so a refactor
// cannot quietly drift away from what the hardware accepts.
func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	return b
}

// The store request ucmix builds must be byte-identical to UC Surface's.
func TestStorePresetMatchesCapture(t *testing.T) {
	want := readFixture(t, "jm-storepreset-request.bin")

	got := proto.MarshalJM(proto.StorePresetCmd{
		PresetFile: "presets/proj/03.135 Main Live.proj/04.ucstore2.scn",
	})

	// UC Surface pretty-prints with a space after each colon; the board is
	// whitespace-insensitive and ucmix emits compact JSON, so compare the decoded
	// message rather than raw bytes.
	wantMsg, err := proto.ParseJM(want)
	if err != nil {
		t.Fatalf("parsing captured request: %v", err)
	}
	gotMsg, err := proto.ParseJM(got)
	if err != nil {
		t.Fatalf("parsing built request: %v", err)
	}
	if gotMsg.ID != wantMsg.ID {
		t.Errorf("id = %q, want %q", gotMsg.ID, wantMsg.ID)
	}

	wantAck, err := proto.UnmarshalPresetAck(wantMsg.Body)
	if err != nil {
		t.Fatal(err)
	}
	gotAck, err := proto.UnmarshalPresetAck(gotMsg.Body)
	if err != nil {
		t.Fatal(err)
	}
	if gotAck.PresetFile != wantAck.PresetFile {
		t.Errorf("presetFile = %q, want %q", gotAck.PresetFile, wantAck.PresetFile)
	}
}

// The board's real acknowledgment must parse into the fields StoreScene matches on.
func TestStoredPresetAckFromCapture(t *testing.T) {
	m, err := proto.ParseJM(readFixture(t, "jm-storedpreset-ack.bin"))
	if err != nil {
		t.Fatalf("parsing captured ack: %v", err)
	}
	if m.ID != proto.JMStoredPreset {
		t.Fatalf("ack id = %q, want %q", m.ID, proto.JMStoredPreset)
	}

	ack, err := proto.UnmarshalPresetAck(m.Body)
	if err != nil {
		t.Fatal(err)
	}
	if want := "presets/proj/03.135 Main Live.proj/04.ucstore2.scn"; ack.PresetFile != want {
		t.Errorf("presetFile = %q, want %q", ack.PresetFile, want)
	}
	if want := "ucstore2"; ack.PresetName != want {
		t.Errorf("presetName = %q, want %q", ack.PresetName, want)
	}
	if want := "scn"; ack.PresetType != want {
		t.Errorf("presetType = %q, want %q", ack.PresetType, want)
	}
}

// A rename ucmix builds must be byte-identical to UC Surface's.
func TestRenameRequestMatchesCapture(t *testing.T) {
	want := readFixture(t, "fr-rename-request.bin")

	resource := proto.VerbRena + "presets/proj/03.135 Main Live.proj/04.ucstore2.scn"
	got := proto.MarshalFR(1, resource, "ucstore2-renamed")

	if string(got) != string(want) {
		t.Errorf("rename request does not match the capture\n got: %q\nwant: %q", got, want)
	}
}
