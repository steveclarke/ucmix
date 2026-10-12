package daw

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveclarke/ucmix/internal/proto"
)

// mixStudio is a song with tracks Drums, Bass, Loops (audio) and Pad
// (instrument), faders at 0 dB and trims at 0, titled "Remix", plus a folder
// of the stems that were imported onto the audio tracks.
func mixStudio(t *testing.T) (*Studio, *fakeStudio, string) {
	t.Helper()
	st, fake := newStudio(t)
	home := t.TempDir()
	stems := filepath.Join(home, "stems")
	if err := os.MkdirAll(stems, 0o700); err != nil {
		t.Fatal(err)
	}
	put := func(name string, samples []float64) string {
		p := filepath.Join(stems, name)
		if err := os.WriteFile(p, encodeAudio(t, "WAV", "int", 24, 2, 48000, samples, ""), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	// Drums average -23.7 dBFS, bass -47 (Steve's exports), loops empty.
	rms := func(db float64) float64 { return math.Pow(10, db/20) * math.Sqrt2 }
	drums := put("Song 129bpm - Drums.wav", sine(2, 48000, 100, rms(-23.7), 1))
	bass := put("Song 129bpm - Bass.wav", sine(2, 48000, 60, rms(-47), 1))
	loops := put("Song 129bpm - Loops.wav", silence(2, 48000, 1))

	origLog, origSongs, origFind := importLogPath, songsDir, findFilesNamed
	t.Cleanup(func() { importLogPath, songsDir, findFilesNamed = origLog, origSongs, origFind })
	importLogPath = func() string { return filepath.Join(home, "imports.json") }
	songsDir = func() string { return filepath.Join(home, "Songs") }
	findFilesNamed = func(context.Context, string) []string { return nil }

	frames := []proto.Frame{ps("document/title", "Remix"), pv("mixer/channel/count", 0.004)}
	for i, tr := range []struct{ name, kind string }{{"Drums", "AudioTrack"}, {"Bass", "AudioTrack"}, {"Loops", "AudioTrack"}, {"Pad", "InstrumentTrack"}} {
		ch := "mixer/channel/ch" + string(rune('1'+i))
		frames = append(frames,
			ps(ch+"/label", tr.name), ps(ch+"/type", tr.kind), ps(ch+"/speakerFormat", "Stereo"),
			pv(ch+"/volume", 0.76), pv(ch+"/inputfx/gain", 0.5))
	}
	frames = append(frames, pv("mixer/channel/ch4/mute", 0))
	fake.mu.Lock()
	for _, f := range frames {
		fake.frames <- f
	}
	fake.mu.Unlock()
	if err := st.session.WaitFor(context.Background(), NumIs("mixer/channel/ch4/mute", 0)); err != nil {
		t.Fatal(err)
	}
	recordImports("Remix", []imported{
		{Track: "Drums", Channel: "mixer/channel/ch1", Audio: AudioFile{Path: drums}},
		{Track: "Bass", Channel: "mixer/channel/ch2", Audio: AudioFile{Path: bass}},
		{Track: "Loops", Channel: "mixer/channel/ch3", Audio: AudioFile{Path: loops}},
	})
	return st, fake, stems
}

func TestTrimSetsInputGainInDB(t *testing.T) {
	st, _, _ := mixStudio(t)
	if got := do(t, st, Request{Op: "trim", Path: "Bass"}); got != "Bass trim is at 0 dB." {
		t.Errorf("read = %q", got)
	}
	if got := do(t, st, Request{Op: "trim", Path: "Bass", Action: "set", Value: "20"}); got != "Bass trim +20 dB (was 0 dB)." {
		t.Errorf("set = %q", got)
	}
	// The wire is linear over -24..+24: +20 dB is 44/48.
	if v, _ := st.model().Num("mixer/channel/ch2/inputfx/gain"); math.Abs(v-44.0/48) > 1e-6 {
		t.Errorf("wire = %v", v)
	}
	if got := do(t, st, Request{Op: "trim", Path: "Bass", Action: "down", Value: "6"}); got != "Bass trim +14 dB (was +20 dB)." {
		t.Errorf("down = %q", got)
	}
	if got := do(t, st, Request{Op: "trim", Path: "Bass", Action: "set", Value: "-6 dB"}); got != "Bass trim -6 dB (was +14 dB)." {
		t.Errorf("negative = %q", got)
	}
	for _, bad := range []Request{
		{Op: "trim", Path: "Bass", Action: "set", Value: "30"},
		{Op: "trim", Path: "Bass", Action: "up", Value: "40"},
		{Op: "trim", Path: "Bass", Action: "set", Value: "loud"},
		{Op: "trim", Path: "Nobody", Action: "set", Value: "1"},
	} {
		if _, err := st.Do(context.Background(), bad); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	// Faders are not touched by a trim.
	if v, _ := st.model().Num("mixer/channel/ch2/volume"); v != float64(float32(0.76)) {
		t.Errorf("fader moved: %v", v)
	}
}

func TestAnalyzeTrackFindsItsImportedStem(t *testing.T) {
	st, fake, _ := mixStudio(t)
	before := len(fake.sent)
	res, err := st.Do(context.Background(), Request{Op: "analyze", Tracks: []string{"bass", "Loops"}})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(res.Message, "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[1], "Bass: average -47 dBFS") || !strings.Contains(lines[1], "trim +24 dB") ||
		!strings.HasPrefix(lines[2], "Loops is silent") {
		t.Errorf("message = %q", res.Message)
	}
	if len(fake.sent) != before {
		t.Errorf("analysis wrote to Studio Pro: %d frames", len(fake.sent)-before)
	}
}

func TestAnalyzeWholeSongIsEveryAudioTrack(t *testing.T) {
	st, _, _ := mixStudio(t)
	got := do(t, st, Request{Op: "analyze"})
	if !strings.HasPrefix(got, "Measured 3 stems") || strings.Contains(got, "Pad") {
		t.Errorf("message = %q", got)
	}
}

// Without an import record (a song imported before ucdaw kept one), the
// track's stem is found from the waveform files Studio Pro keeps for the
// song, and checked by its length in frames.
func TestAnalyzeTrackFromWaveformCache(t *testing.T) {
	st, _, stems := mixStudio(t)
	if err := os.Remove(importLogPath()); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(songsDir(), "Remix", "Cache", "Images")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"Drums", "Bass", "Loops"} {
		name := "Song 129bpm - " + s + ".wav"
		if err := os.WriteFile(filepath.Join(cache, "xB6E75699"+name+"BB80.peak"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	findFilesNamed = func(_ context.Context, name string) []string {
		return []string{filepath.Join("/elsewhere", name), filepath.Join(stems, name)}
	}
	got := do(t, st, Request{Op: "analyze", Tracks: []string{"Drums"}})
	if !strings.Contains(got, "Drums: average -23.7 dBFS") {
		t.Errorf("message = %q", got)
	}
	// A file of the same name but another length is not the stem.
	if err := os.Rename(filepath.Join(cache, "xB6E75699Song 129bpm - Drums.wavBB80.peak"), filepath.Join(cache, "xB6E75699Song 129bpm - Drums.wavBB81.peak")); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Do(context.Background(), Request{Op: "analyze", Tracks: []string{"Drums"}}); err == nil || !strings.Contains(err.Error(), "Drums") {
		t.Errorf("err = %v", err)
	}
}

func TestAnalyzeTrackWithoutStemSaysSo(t *testing.T) {
	st, _, _ := mixStudio(t)
	_, err := st.Do(context.Background(), Request{Op: "analyze", Tracks: []string{"Pad"}})
	if err == nil || !strings.Contains(err.Error(), "Pad") {
		t.Errorf("err = %v", err)
	}
}

func TestGainStageSetsTrimsAndLeavesFadersAlone(t *testing.T) {
	st, fake, _ := mixStudio(t)
	// Steve's balance: drums pulled down 2 dB.
	if got := do(t, st, Request{Op: "fader", Path: "Drums", Action: "set", Value: "-2"}); got != "Drums at -2 dB (was 0 dB)." {
		t.Fatal(got)
	}
	drumsFader, _ := st.model().Num("mixer/channel/ch1/volume")
	before := len(fake.sent)
	res, err := st.Do(context.Background(), Request{Op: "gainstage"})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(res.Message, "\n")
	if len(lines) != 4 ||
		!strings.HasPrefix(lines[0], "Gain-staged 2 tracks to a -18 dBFS average; faders untouched") ||
		lines[1] != "Drums: trim 0 → +5.7 dB (stem averages -23.7 dBFS)." ||
		!strings.HasPrefix(lines[2], "Bass: trim 0 → +24 dB (stem averages -47 dBFS; 5 dB short") ||
		!strings.HasPrefix(lines[3], "Loops: left alone, silent: an empty export") {
		t.Errorf("message =\n%s", res.Message)
	}
	// Only trims were written.
	for _, f := range fake.sent[before:] {
		if f.Code != proto.CodePV {
			continue
		}
		key, _, err := proto.UnmarshalPV(f.Payload)
		if err != nil || !strings.HasSuffix(key, "/inputfx/gain") {
			t.Errorf("gain stage wrote %s", key)
		}
	}
	if v, _ := st.model().Num("mixer/channel/ch1/volume"); v != drumsFader {
		t.Errorf("drums fader moved")
	}
	if v, _ := st.model().Num("mixer/channel/ch2/inputfx/gain"); math.Abs(v-1) > 1e-6 {
		t.Errorf("bass trim wire = %v", v)
	}
	if v, _ := st.model().Num("mixer/channel/ch3/inputfx/gain"); v != 0.5 {
		t.Errorf("loops trim changed: %v", v)
	}
	// Run again: nothing to change.
	again := do(t, st, Request{Op: "gainstage", Tracks: []string{"Drums"}})
	if !strings.Contains(again, "Drums: trim already +5.7 dB") {
		t.Errorf("again = %q", again)
	}
}

func TestGainStageRefusesWhatItCannotMeasure(t *testing.T) {
	st, _, _ := mixStudio(t)
	got := do(t, st, Request{Op: "gainstage", Tracks: []string{"Pad", "Drums"}})
	if !strings.Contains(got, "Pad: left alone, no stem file found") || !strings.Contains(got, "Drums: trim 0 → +5.7 dB") {
		t.Errorf("message = %q", got)
	}
}

func TestImportLogIsBoundedAndPrivate(t *testing.T) {
	dir := t.TempDir()
	orig := importLogPath
	t.Cleanup(func() { importLogPath = orig })
	importLogPath = func() string { return filepath.Join(dir, "ucdaw", "imports.json") }
	for i := 0; i < maxImportRecords+50; i++ {
		recordImports("Song\x1b[31m", []imported{{Track: "T", Channel: "c", Audio: AudioFile{Path: "/x.wav"}}})
	}
	recs := loadImportLog()
	if len(recs) != maxImportRecords {
		t.Errorf("records = %d", len(recs))
	}
	if strings.ContainsRune(recs[0].Song, 0x1b) {
		t.Errorf("control characters kept: %q", recs[0].Song)
	}
	info, err := os.Stat(importLogPath())
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, %v", info.Mode(), err)
	}
	// A damaged or huge file is ignored, not trusted.
	if err := os.WriteFile(importLogPath(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if recs := loadImportLog(); len(recs) != 0 {
		t.Errorf("damaged file gave %d records", len(recs))
	}
}

func TestParsePeakName(t *testing.T) {
	cases := map[string]struct {
		file   string
		frames uint64
		ok     bool
	}{
		"xB6E75699The King Has Come 129bpm - Bass.wavBF8B9C.peak": {"The King Has Come 129bpm - Bass.wav", 0xBF8B9C, true},
		"x00000000Vox.aifF00.peak":                                {"Vox.aif", 0xF00, true},
		"x00000000Vox.AIFF10.peak":                                {"Vox.AIFF", 0x10, true},
		"notes.peak":                                              {"", 0, false},
		"x00000000Vox.wav.peak":                                   {"", 0, false},
		"x00000000../../etc/passwd.wavFF.peak":                    {"", 0, false},
	}
	for in, want := range cases {
		file, frames, ok := parsePeakName(in)
		if file != want.file || frames != want.frames || ok != want.ok {
			t.Errorf("%q = %q %x %v", in, file, frames, ok)
		}
	}
}
