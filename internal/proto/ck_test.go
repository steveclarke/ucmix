package proto

import (
	"encoding/binary"
	"os"
	"strings"
	"testing"
)

// makeCK builds a CK frame payload: 4-byte tag, then offset/total/size u32 LE,
// then data.
func makeCK(offset, total uint32, data []byte) []byte {
	p := make([]byte, 16+len(data))
	copy(p[0:4], []byte{0x00, 0x00, 'Z', 'B'})
	binary.LittleEndian.PutUint32(p[4:8], offset)
	binary.LittleEndian.PutUint32(p[8:12], total)
	binary.LittleEndian.PutUint32(p[12:16], uint32(len(data)))
	copy(p[16:], data)
	return p
}

func TestParseCK(t *testing.T) {
	c, err := ParseCK(makeCK(16, 40, []byte("second-half-payload!")))
	if err != nil {
		t.Fatalf("ParseCK: %v", err)
	}
	if c.Offset != 16 || c.Total != 40 || c.Size != 20 || string(c.Data) != "second-half-payload!" {
		t.Errorf("ParseCK = %+v", c)
	}
}

func TestParseCKShort(t *testing.T) {
	if _, err := ParseCK([]byte{0, 0, 'Z', 'B', 1, 2}); err == nil {
		t.Fatal("want error for short CK payload, got nil")
	}
}

func TestChunkAssembler(t *testing.T) {
	full := []byte("this-is-the-complete-reassembled-zlib-blob-across-two-frames")
	split := 25
	c1 := CKChunk{Offset: 0, Total: uint32(len(full)), Size: uint32(split), Data: full[:split]}
	c2 := CKChunk{Offset: uint32(split), Total: uint32(len(full)), Size: uint32(len(full) - split), Data: full[split:]}

	var a ChunkAssembler
	if blob, done := a.Add(c1); done || blob != nil {
		t.Fatalf("first chunk should not complete: done=%v", done)
	}
	blob, done := a.Add(c2)
	if !done {
		t.Fatal("second chunk should complete the blob")
	}
	if string(blob) != string(full) {
		t.Errorf("reassembled = %q, want %q", blob, full)
	}
}

func TestChunkAssemblerResetsAtOffsetZero(t *testing.T) {
	var a ChunkAssembler
	// A truncated prior blob (never completes) must not corrupt the next one.
	a.Add(CKChunk{Offset: 0, Total: 999, Size: 5, Data: []byte("stale")})
	blob, done := a.Add(CKChunk{Offset: 0, Total: 5, Size: 5, Data: []byte("fresh")})
	if !done || string(blob) != "fresh" {
		t.Errorf("Add = %q done=%v, want fresh/true", blob, done)
	}
}

// TestParseZBRealSnapshot decodes a real ZB blob captured from a StudioLive 32R.
// This is the regression guard for the two bugs that shipped because the client
// was only ever tested against a synthetic board: the snapshot arrives chunked
// (CK) and its paths are wrapped in children/values structural keys. The fake
// board reproduced neither.
func TestParseZBRealSnapshot(t *testing.T) {
	blob, err := os.ReadFile("testdata/real-snapshot-32r.zb")
	if err != nil {
		t.Skipf("no real-hardware fixture: %v", err)
	}
	m, err := ParseZB(blob)
	if err != nil {
		t.Fatalf("ParseZB(real snapshot): %v", err)
	}
	if len(m) < 20000 {
		t.Errorf("decoded %d paths, want > 20000 for a 32R", len(m))
	}
	// Paths must be collapsed: no structural children/values wrappers survive.
	for k := range m {
		if strings.HasPrefix(k, "children/") || strings.Contains(k, "/children/") || strings.Contains(k, "/values/") {
			t.Errorf("path retains structural wrapper: %q", k)
			break
		}
	}
	// Known controls decode to the humanizable namespace.
	for _, want := range []string{"line/ch1/username", "line/ch1/48v", "line/ch1/mute", "aux/ch1/link"} {
		if _, ok := m[want]; !ok {
			t.Errorf("real snapshot missing expected path %q", want)
		}
	}
}

// TestRealSnapshotVolumesArePlainWire is the regression guard for the bogus ×100
// volume read-scale (bug #16). A real 32R returns the plain 0..1 wire position on
// read, so every "volume" leaf in the capture must fall in [0, 1] — a ×100 form
// (e.g. 74.6) would prove the false assumption. The capture also contains
// faders parked at -6 dB (wire 0.746), so the exact value the taper decodes to
// -6 is present in real board state, not only in synthetic fixtures.
func TestRealSnapshotVolumesArePlainWire(t *testing.T) {
	blob, err := os.ReadFile("testdata/real-snapshot-32r.zb")
	if err != nil {
		t.Skipf("no real-hardware fixture: %v", err)
	}
	m, err := ParseZB(blob)
	if err != nil {
		t.Fatalf("ParseZB(real snapshot): %v", err)
	}
	seen := 0
	for k, v := range m {
		if lastSegment(k) != "volume" {
			continue
		}
		f, ok := asFloat64(v)
		if !ok {
			continue // non-float volume leaves are not fader positions
		}
		seen++
		if f < 0 || f > 1 {
			t.Errorf("%s = %v, want a plain 0..1 wire position (not a ×100 read)", k, f)
		}
	}
	if seen == 0 {
		t.Fatal("no volume leaves found in the real snapshot")
	}
	// A parked -6 dB fader reads 0.746 on the wire; the capture has these.
	if v, ok := asFloat64(m["filtergroup/ch1/volume"]); !ok || v < 0.7459 || v > 0.7461 {
		t.Errorf("filtergroup/ch1/volume = %v (ok=%v), want ~0.746 (the -6 dB wire value)", m["filtergroup/ch1/volume"], ok)
	}
}

// lastSegment returns the final "/"-delimited segment of a path.
func lastSegment(path string) string {
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return path
}

// asFloat64 coerces the float kinds ParseZB emits (float64) to float64.
func asFloat64(v any) (float64, bool) {
	switch f := v.(type) {
	case float64:
		return f, true
	case float32:
		return float64(f), true
	default:
		return 0, false
	}
}
