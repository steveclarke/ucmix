package proto

import (
	"bytes"
	"os"
	"testing"
)

// loadFDFixtures reads the two captured FD chunks of the real 32R's
// list-projects reply (extracted from testdata/uc-surface-listpresets.pcap).
func loadFDFixtures(t *testing.T) (chunk0, chunk1 []byte) {
	t.Helper()
	var err error
	chunk0, err = os.ReadFile("testdata/fd-projects-chunk0.bin")
	if err != nil {
		t.Fatal(err)
	}
	chunk1, err = os.ReadFile("testdata/fd-projects-chunk1.bin")
	if err != nil {
		t.Fatal(err)
	}
	return chunk0, chunk1
}

// TestParseFDCaptureHeader asserts the decoded header fields of the first
// captured FD chunk against the raw bytes: id 1, offset 0, total 6905 (the full
// ~7 KB body), this chunk 4096 bytes.
func TestParseFDCaptureHeader(t *testing.T) {
	chunk0, _ := loadFDFixtures(t)
	fd, err := ParseFD(chunk0)
	if err != nil {
		t.Fatal(err)
	}
	if fd.ID != 1 {
		t.Errorf("id = %d, want 1", fd.ID)
	}
	if fd.Chunk.Offset != 0 || fd.Chunk.Total != 6905 || fd.Chunk.Size != 4096 {
		t.Errorf("chunk = %+v, want offset 0 total 6905 size 4096", fd.Chunk)
	}
	if len(fd.Chunk.Data) != 4096 {
		t.Errorf("data len = %d, want 4096", len(fd.Chunk.Data))
	}
}

// TestFDReassembleAndParse feeds both captured chunks through ChunkAssembler and
// parses the completed body — the real 32R's project list. Reassembly must
// complete on the second chunk (offset+size == total) and yield the three real
// projects Steve had on the board, each a folder.
func TestFDReassembleAndParse(t *testing.T) {
	chunk0, chunk1 := loadFDFixtures(t)

	var asm ChunkAssembler
	fd0, err := ParseFD(chunk0)
	if err != nil {
		t.Fatal(err)
	}
	if _, done := asm.Add(fd0.Chunk); done {
		t.Fatal("assembler completed after chunk 0; want more")
	}
	fd1, err := ParseFD(chunk1)
	if err != nil {
		t.Fatal(err)
	}
	body, done := asm.Add(fd1.Chunk)
	if !done {
		t.Fatal("assembler did not complete after chunk 1")
	}

	files, err := ParsePresetList(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 100 {
		t.Fatalf("files = %d, want 100 (a full slot roster)", len(files))
	}

	// The three occupied projects are folders with real titles; the rest are
	// empty slots.
	want := []PresetFile{
		{Name: "01.Sevenview Live.proj", Title: "Sevenview Live", Dir: true},
		{Name: "02.Steve.proj", Title: "Steve", Dir: true},
		{Name: "03.135 Main Live.proj", Title: "135 Main Live", Dir: true},
	}
	for i, w := range want {
		if files[i] != w {
			t.Errorf("files[%d] = %+v, want %+v", i, files[i], w)
		}
	}
	if files[3].Dir || files[3].Title != EmptyPresetTitle {
		t.Errorf("files[3] = %+v, want an empty slot", files[3])
	}
}

// TestBuildFDPayloadMatchesCaptureHeader locks the encoder to the wire: the
// header BuildFDPayload emits for chunk 0's parameters is byte-for-byte the
// captured chunk's 14-byte header.
func TestBuildFDPayloadMatchesCaptureHeader(t *testing.T) {
	chunk0, _ := loadFDFixtures(t)
	fd, err := ParseFD(chunk0)
	if err != nil {
		t.Fatal(err)
	}
	built := BuildFDPayload(fd.ID, fd.Chunk.Offset, fd.Chunk.Total, fd.Chunk.Data)
	if !bytes.Equal(built[:fdHeaderLen], chunk0[:fdHeaderLen]) {
		t.Fatalf("built header =\n% x\nwant\n% x", built[:fdHeaderLen], chunk0[:fdHeaderLen])
	}
	if !bytes.Equal(built, chunk0) {
		t.Fatal("built FD payload does not round-trip the captured chunk")
	}
}

// TestBuildFDPayloadRoundTrip covers the multi-chunk case: a body split in two
// re-parses and reassembles to the original, with the more flag set only on the
// non-final chunk.
func TestBuildFDPayloadRoundTrip(t *testing.T) {
	body := []byte(`{"files":[{"name":"01.A.proj","title":"A","dir":true}]}`)
	half := len(body) / 2

	c0 := BuildFDPayload(7, 0, uint32(len(body)), body[:half])
	c1 := BuildFDPayload(7, uint32(half), uint32(len(body)), body[half:])

	// more flag: 1 on chunk 0, 0 on the last chunk.
	if c0[10] != 1 || c1[10] != 0 {
		t.Errorf("more flags = %d,%d, want 1,0", c0[10], c1[10])
	}

	var asm ChunkAssembler
	fd0, _ := ParseFD(c0)
	fd1, _ := ParseFD(c1)
	if _, done := asm.Add(fd0.Chunk); done {
		t.Fatal("completed too early")
	}
	got, done := asm.Add(fd1.Chunk)
	if !done || !bytes.Equal(got, body) {
		t.Fatalf("reassembled = %q (done=%v), want %q", got, done, body)
	}
}
