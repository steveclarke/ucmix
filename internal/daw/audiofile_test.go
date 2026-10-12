package daw

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// wavBytes builds a WAV header (and silent data) with extra chunks before
// fmt, as BWF files carry.
func wavBytes(channels, rate, bits, frames int, extensible bool) []byte {
	var body bytes.Buffer
	body.WriteString("WAVE")
	chunk := func(id string, data []byte) {
		body.WriteString(id)
		_ = binary.Write(&body, binary.LittleEndian, uint32(len(data)))
		body.Write(data)
		if len(data)%2 == 1 {
			body.WriteByte(0)
		}
	}
	chunk("bext", make([]byte, 603))
	fmtLen := 16
	if extensible {
		fmtLen = 40
	}
	f := make([]byte, fmtLen)
	tag := uint16(1)
	if extensible {
		tag = 0xFFFE
	}
	block := channels * bits / 8
	binary.LittleEndian.PutUint16(f[0:], tag)
	binary.LittleEndian.PutUint16(f[2:], uint16(channels))
	binary.LittleEndian.PutUint32(f[4:], uint32(rate))
	binary.LittleEndian.PutUint32(f[8:], uint32(rate*block))
	binary.LittleEndian.PutUint16(f[12:], uint16(block))
	binary.LittleEndian.PutUint16(f[14:], uint16(bits))
	chunk("fmt ", f)
	chunk("data", make([]byte, frames*block))
	var out bytes.Buffer
	out.WriteString("RIFF")
	_ = binary.Write(&out, binary.LittleEndian, uint32(body.Len()))
	out.Write(body.Bytes())
	return out.Bytes()
}

func aiffBytes(channels int, rate float64, bits, frames int) []byte {
	var body bytes.Buffer
	body.WriteString("AIFF")
	comm := make([]byte, 18)
	binary.BigEndian.PutUint16(comm[0:], uint16(channels))
	binary.BigEndian.PutUint32(comm[2:], uint32(frames))
	binary.BigEndian.PutUint16(comm[6:], uint16(bits))
	// 80-bit extended: exponent and a mantissa with the integer bit set.
	exp := 0
	m := rate
	for m >= 2 {
		m /= 2
		exp++
	}
	binary.BigEndian.PutUint16(comm[8:], uint16(16383+exp))
	binary.BigEndian.PutUint64(comm[10:], uint64(m*(1<<63)))
	body.WriteString("COMM")
	_ = binary.Write(&body, binary.BigEndian, uint32(18))
	body.Write(comm)
	block := channels * bits / 8
	body.WriteString("SSND")
	_ = binary.Write(&body, binary.BigEndian, uint32(8+frames*block))
	body.Write(make([]byte, 8+frames*block))
	var out bytes.Buffer
	out.WriteString("FORM")
	_ = binary.Write(&out, binary.BigEndian, uint32(body.Len()))
	out.Write(body.Bytes())
	return out.Bytes()
}

func writeFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadAudioFileHeaders(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		data []byte
		want AudioFile
	}{
		{"mono.wav", wavBytes(1, 48000, 16, 4800, false), AudioFile{Container: "WAV", Channels: 1, SampleRate: 48000, Bits: 16, Frames: 4800}},
		{"stereo.wav", wavBytes(2, 44100, 24, 441, true), AudioFile{Container: "WAV", Channels: 2, SampleRate: 44100, Bits: 24, Frames: 441}},
		{"vox.aif", aiffBytes(1, 48000, 24, 960), AudioFile{Container: "AIFF", Channels: 1, SampleRate: 48000, Bits: 24, Frames: 960}},
		{"pad.aiff", aiffBytes(2, 44100, 16, 100), AudioFile{Container: "AIFF", Channels: 2, SampleRate: 44100, Bits: 16, Frames: 100}},
	}
	for _, c := range cases {
		p := writeFile(t, dir, c.name, c.data)
		got, err := ReadAudioFile(p)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		c.want.Path = p
		if got != c.want {
			t.Errorf("%s = %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestReadAudioFileRejectsWhatIsNotAudio(t *testing.T) {
	dir := t.TempDir()
	good := wavBytes(1, 48000, 16, 10, false)
	bad := map[string][]byte{
		"empty.wav":     {},
		"text.wav":      []byte("hello, this is not audio at all"),
		"truncated.wav": good[:20],
		"song.song":     []byte("PK\x03\x04 a zip"),
		"no-data.wav":   good[:12+8+604+8+16],
	}
	zeroBlock := wavBytes(1, 48000, 16, 10, false)
	// fmt block align is at RIFF(12) + bext(8+604) + fmt header(8) + 12.
	binary.LittleEndian.PutUint16(zeroBlock[12+8+604+8+12:], 0)
	bad["zero-block.wav"] = zeroBlock
	for name, data := range bad {
		if _, err := ReadAudioFile(writeFile(t, dir, name, data)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if _, err := ReadAudioFile(dir); err == nil {
		t.Error("a folder read as audio")
	}
}

func TestAudioFileFormatAndSampleRate(t *testing.T) {
	if (AudioFile{Channels: 2}).Format() != "stereo" || (AudioFile{Channels: 1}).Format() != "mono" {
		t.Error("format")
	}
	if math.Abs(extended80([]byte{0x40, 0x0e, 0xbb, 0x80, 0, 0, 0, 0, 0, 0})-48000) > 1e-9 {
		t.Error("48000 as 80-bit extended")
	}
}

func TestCollectAudioFolderInNameOrder(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "02 Bass.wav", wavBytes(2, 48000, 16, 10, false))
	writeFile(t, dir, "01 Kick.wav", wavBytes(1, 48000, 16, 10, false))
	writeFile(t, dir, "notes.txt", []byte("session notes"))
	writeFile(t, dir, "guide.mp3", []byte("ID3"))
	writeFile(t, dir, ".DS_Store", []byte("x"))
	writeFile(t, dir, "surround.wav", wavBytes(6, 48000, 16, 10, false))
	if err := os.Mkdir(filepath.Join(dir, "old"), 0o700); err != nil {
		t.Fatal(err)
	}
	files, skips, err := collectAudio([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range files {
		names = append(names, filepath.Base(f.Path))
	}
	if strings.Join(names, ",") != "01 Kick.wav,02 Bass.wav" {
		t.Errorf("files = %v", names)
	}
	if got := skipText(skips); got != "guide.mp3 (ucdaw imports WAV and AIFF files; convert it first), notes.txt (not audio) and surround.wav (6 channels; ucdaw imports mono and stereo files)" {
		t.Errorf("skips = %q", got)
	}
	if _, _, err := collectAudio([]string{"relative/path.wav"}); err == nil {
		t.Error("relative path accepted")
	}
}

func TestCollectAudioBoundsTheBatch(t *testing.T) {
	dir := t.TempDir()
	data := wavBytes(1, 48000, 16, 1, false)
	for i := 0; i <= MaxImportFiles; i++ {
		writeFile(t, dir, filepath.Base(strings.Repeat("a", 1)+string(rune('A'+i%26))+strings.Repeat("x", i/26)+".wav"), data)
	}
	if _, _, err := collectAudio([]string{dir}); err == nil {
		t.Error("no limit")
	}
}
