package dawcli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/steveclarke/ucmix/internal/daw"
)

func TestSafeStripsTerminalControls(t *testing.T) {
	tests := map[string]string{
		"Guitar":                  "Guitar",
		"Bad\x1b]0;pwned\x07Name": "Bad?]0;pwned?Name",
		"C1\u009bcsi":             "C1?csi",
		"del\x7f":                 "del?",
		"tab\tok, café, é中":       "tab\tok, café, é中",
	}
	for in, want := range tests {
		if got := safe(in); got != want {
			t.Errorf("safe(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPrintedRowsAreSanitized(t *testing.T) {
	var buf bytes.Buffer
	o := &options{}
	err := printResult(&buf, o, daw.Result{
		Message: "1 command\x1b[2J.",
		Data:    []daw.Command{{Category: "Track\x1b[31m", Name: "Add\tTrack"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.ContainsRune(buf.Bytes(), 0x1b) {
		t.Errorf("escape reached the terminal: %q", buf.String())
	}
}

func TestDiscoverLineToleratesOddAnnouncements(t *testing.T) {
	for _, e := range []daw.Endpoint{
		{Product: "Studio Pro", Version: " ", Name: "x"},
		{Product: "Studio Pro", Version: "", Name: "evil\x1b[2J"},
	} {
		if line := discoverLine(e); bytes.ContainsRune([]byte(line), 0x1b) {
			t.Errorf("line = %q", line)
		}
	}
}

func TestCaptureFilesAreOwnerOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cap.bin")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := createCapture(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", info.Mode().Perm())
	}
}
