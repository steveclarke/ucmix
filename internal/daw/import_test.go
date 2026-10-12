package daw

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/steveclarke/ucmix/internal/proto"
)

// importRig is a Studio Pro that adds tracks, plus a song folder whose
// waveform cache fills when a file is "opened".
type importRig struct {
	st     *Studio
	fake   *fakeStudio
	peaks  string
	mu     sync.Mutex
	opened []string
	log    []string
	// onOpen runs instead of writing the waveform file when set.
	onOpen func(path string)
}

func newImportRig(t *testing.T) *importRig {
	t.Helper()
	st, fake := newStudio(t)
	r := &importRig{st: st, fake: fake}
	channels := 0
	fake.mu.Lock()
	fake.echoStrings = true
	fake.onCommand = nil
	fake.onInvoke = func(category, name string, args map[string]string) []proto.Frame {
		r.mu.Lock()
		r.log = append(r.log, category+">"+name+fmtArgs(args))
		r.mu.Unlock()
		if category != "Track" || !strings.HasPrefix(name, "Add Audio Track") {
			return nil
		}
		channels++
		format := "Mono"
		if strings.Contains(name, "stereo") {
			format = "Stereo"
		}
		ch := fmt.Sprintf("mixer/channel/ch%d", channels)
		out := []proto.Frame{
			pv("mixer/channel/count", float32(channels)/1000),
			ps(ch+"/label", fmt.Sprintf("Track %d", channels)),
			ps(ch+"/type", "AudioTrack"),
			ps(ch+"/speakerFormat", format),
			pv(ch+"/selected", 1),
		}
		if channels > 1 {
			out = append(out, pv(fmt.Sprintf("mixer/channel/ch%d/selected", channels-1), 0))
		}
		return out
	}
	fake.mu.Unlock()

	songs := t.TempDir()
	song := filepath.Join(songs, "Demo")
	r.peaks = filepath.Join(song, "Cache", "Images")
	if err := os.MkdirAll(r.peaks, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, song, "Demo.song", []byte("song"))
	st.session.Model().mutate(func(rec func(string)) { st.session.Model().setStr("document/title", "Demo", rec) })

	origOpen, origSongs, origLocal := openInStudioPro, songsDir, isLocalAddress
	origSettle, origLanding, origPoll, origRename, origGap := importSettle, landingSettle, pollEvery, renameDelay, renameGap
	t.Cleanup(func() {
		openInStudioPro, songsDir, isLocalAddress = origOpen, origSongs, origLocal
		importSettle, landingSettle, pollEvery, renameDelay, renameGap = origSettle, origLanding, origPoll, origRename, origGap
	})
	importSettle, landingSettle, pollEvery, renameDelay, renameGap = 50*time.Millisecond, time.Millisecond, 5*time.Millisecond, time.Millisecond, time.Millisecond
	songsDir = func() string { return songs }
	openInStudioPro = func(_ context.Context, path string) error {
		r.mu.Lock()
		r.opened = append(r.opened, path)
		r.log = append(r.log, "open "+filepath.Base(path))
		onOpen := r.onOpen
		r.mu.Unlock()
		if onOpen != nil {
			onOpen(path)
			return nil
		}
		a, err := ReadAudioFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(r.peaks, "x270F117B"+peakSuffix(a)), []byte("peak"), 0o600)
	}
	return r
}

func fmtArgs(args map[string]string) string {
	if v, ok := args["Bars"]; ok {
		return "{" + v + "}"
	}
	return ""
}

func stemFolder(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "01_Kick_Final.wav", wavBytes(1, 48000, 16, 4800, false))
	writeFile(t, dir, "02 Bass DI.wav", wavBytes(2, 48000, 16, 9600, false))
	writeFile(t, dir, "03-Pad (Stereo) v2.wav", wavBytes(2, 44100, 16, 4410, false))
	writeFile(t, dir, "04_Lead_Vox_24bit.aif", aiffBytes(1, 48000, 24, 960))
	writeFile(t, dir, "notes.txt", []byte("notes"))
	return dir
}

func TestImportFolderMakesOneNamedTrackPerStemAtBarOne(t *testing.T) {
	r := newImportRig(t)
	dir := stemFolder(t)
	res, err := r.st.Do(context.Background(), Request{Op: "import", Files: []string{dir}})
	if err != nil {
		t.Fatal(err)
	}
	want := "Imported 4 stems onto new tracks at bar 1: Kick (mono), Bass DI (stereo), Pad (stereo) and Lead Vox (mono). " +
		"Skipped notes.txt (not audio). Tracks read back; Studio Pro does not report clips to remotes, so placement is not read back " +
		"(Studio Pro drew each file's waveform, so it read every file)."
	if res.Message != want {
		t.Errorf("message =\n%q\nwant\n%q", res.Message, want)
	}
	log := strings.Join(r.log, ", ")
	rename := ", Edit>Deselect All, Edit>Select All on Tracks, Event>Rename Events"
	wantLog := "Track>Add Audio Track (mono), Transport>Return to Zero, open 01_Kick_Final.wav, " +
		"Track>Add Audio Track (stereo), Transport>Return to Zero, open 02 Bass DI.wav, " +
		"Track>Add Audio Track (stereo), Transport>Return to Zero, open 03-Pad (Stereo) v2.wav, " +
		"Track>Add Audio Track (mono), Transport>Return to Zero, open 04_Lead_Vox_24bit.aif" + strings.Repeat(rename, 4)
	if log != wantLog {
		t.Errorf("steps =\n%s\nwant\n%s", log, wantLog)
	}
	for i, name := range []string{"Kick", "Bass DI", "Pad", "Lead Vox"} {
		if got, _ := r.st.model().Str(fmt.Sprintf("mixer/channel/ch%d/label", i+1)); got != name {
			t.Errorf("ch%d label = %q, want %q", i+1, got, name)
		}
	}
	data := res.Data.(map[string]any)["imported"].([]imported)
	if data[2].Audio.SampleRate != 44100 || data[2].Format != "Stereo" || data[2].Landed != "waveform" || data[2].Clip != "Pad" {
		t.Errorf("pad = %+v", data[2])
	}
	for _, p := range r.opened {
		if !filepath.IsAbs(p) {
			t.Errorf("opened a relative path %q", p)
		}
	}
}

func TestImportAtABarOntoAnExistingTrack(t *testing.T) {
	r := newImportRig(t)
	dir := stemFolder(t)
	if _, err := r.st.Do(context.Background(), Request{Op: "add_track", Action: "stereo", Name: "Gtr"}); err != nil {
		t.Fatal(err)
	}
	r.log = nil
	res, err := r.st.Do(context.Background(), Request{Op: "import", Files: []string{filepath.Join(dir, "01_Kick_Final.wav")}, Onto: "Gtr", At: 9})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Message, "Imported 01_Kick_Final.wav onto Gtr at bar 9 (a mono file on a stereo track).") {
		t.Errorf("message = %q", res.Message)
	}
	if got := strings.Join(r.log, ", "); got != "Transport>Return to Zero, Transport>Forward Bar{8}, open 01_Kick_Final.wav" {
		t.Errorf("steps = %s", got)
	}
}

func TestImportAFileTheSongHasSeenWaitsAndSaysSo(t *testing.T) {
	r := newImportRig(t)
	dir := stemFolder(t)
	kick := filepath.Join(dir, "01_Kick_Final.wav")
	a, _ := ReadAudioFile(kick)
	writeFile(t, r.peaks, "xOLD"+peakSuffix(a), []byte("old"))
	res, err := r.st.Do(context.Background(), Request{Op: "import", Files: []string{kick}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(res.Message, "so placement is not read back.") {
		t.Errorf("message = %q", res.Message)
	}
	if d := res.Data.(map[string]any)["imported"].([]imported); d[0].Landed != "settle" {
		t.Errorf("landed = %q", d[0].Landed)
	}
}

func TestImportStopsWhenAFileNeverLands(t *testing.T) {
	r := newImportRig(t)
	dir := stemFolder(t)
	calls := 0
	r.onOpen = func(path string) {
		calls++
		if calls == 1 {
			a, _ := ReadAudioFile(path)
			_ = os.WriteFile(filepath.Join(r.peaks, "x1"+peakSuffix(a)), nil, 0o600)
		}
	}
	origTimeout := landingTimeoutFor
	landingTimeoutFor = func(AudioFile) time.Duration { return 50 * time.Millisecond }
	t.Cleanup(func() { landingTimeoutFor = origTimeout })
	_, err := r.st.Do(context.Background(), Request{Op: "import", Files: []string{dir}})
	if err == nil || !strings.HasPrefix(err.Error(), "Imported Kick, then stopped: Studio Pro did not show it had read 02 Bass DI.wav") {
		t.Errorf("err = %v", err)
	}
	if len(r.opened) != 2 {
		t.Errorf("kept going after a file did not land: %v", r.opened)
	}
}

func TestImportAcceptsAConversionAndCancelsAnythingElse(t *testing.T) {
	for _, c := range []struct {
		title, first, second string
		wantErr              bool
		wantJM               string
	}{
		{"Convert Audio Files", "Cancel", "Convert", false, `{"id": "ConfirmDialog","dialogResult": 1}`},
		{"Convert Audio Files", "Don't Convert", "Convert", false, `{"id": "ConfirmDialog","dialogResult": 1}`},
		{"Convert Audio Files", "Convert", "Don’t Convert", false, `{"id": "ConfirmDialog","dialogResult": 0}`},
		{"Convert Audio Files", "Import Without Converting", "Convert", false, `{"id": "ConfirmDialog","dialogResult": 1}`},
		{"Convert Audio Files", "Do Not Convert", "Convert", false, `{"id": "ConfirmDialog","dialogResult": 1}`},
		{"Convert Audio Files", "No", "Convert", false, `{"id": "ConfirmDialog","dialogResult": 1}`},
		{"Unknown Format", "OK", "", true, `{"id": "ConfirmDialog","dialogResult": 0}`},
	} {
		r := newImportRig(t)
		dir := stemFolder(t)
		r.fake.mu.Lock()
		r.fake.onConfirm = []proto.Frame{zm(t, `{"id": "EndDialog"}`)}
		r.fake.mu.Unlock()
		r.onOpen = func(path string) {
			r.fake.frames <- zm(t, fmt.Sprintf(`{"id": "BeginDialog","dialogType": 1,"dialogText": "","dialogTitle": %q,"firstButton": %q,"secondButton": %q}`, c.title, c.first, c.second))
			_ = r.st.session.WaitFor(context.Background(), func(m *Model) bool { _, open := m.Dialog(); return open })
			a, _ := ReadAudioFile(path)
			_ = os.WriteFile(filepath.Join(r.peaks, "x1"+peakSuffix(a)), nil, 0o600)
		}
		res, err := r.st.Do(context.Background(), Request{Op: "import", Files: []string{filepath.Join(dir, "03-Pad (Stereo) v2.wav")}})
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err = %v", c.title, err)
		}
		if !c.wantErr && !strings.Contains(res.Message, `Studio Pro answered "Convert Audio Files" with Convert.`) {
			t.Errorf("%s: message = %q", c.title, res.Message)
		}
		if c.wantErr && !strings.Contains(err.Error(), `ucdaw closed it with "OK"`) {
			t.Errorf("%s: err = %v", c.title, err)
		}
		var confirms []string
		for _, jm := range r.fake.sentJM() {
			if strings.Contains(jm, "ConfirmDialog") {
				confirms = append(confirms, jm)
			}
		}
		if len(confirms) != 1 || confirms[0] != c.wantJM {
			t.Errorf("%s: dialog answers = %v", c.title, confirms)
		}
		if _, open := r.st.model().Dialog(); open {
			t.Errorf("%s: dialog left open", c.title)
		}
	}
}

func TestImportRefusals(t *testing.T) {
	r := newImportRig(t)
	dir := stemFolder(t)
	cases := []struct {
		req  Request
		want string
	}{
		{Request{Op: "import"}, "say which audio files or folder to import"},
		{Request{Op: "import", Files: []string{filepath.Join(dir, "notes.txt")}}, "Nothing to import: notes.txt (not a WAV or AIFF file)"},
		{Request{Op: "import", Files: []string{dir}, At: -2}, "give a bar number from 1, not -2"},
		{Request{Op: "import", Files: []string{dir}, Onto: "Nope"}, `no channel called "Nope"`},
	}
	for _, c := range cases {
		if _, err := r.st.Do(context.Background(), c.req); err == nil || err.Error() != c.want {
			t.Errorf("%+v: err = %v, want %q", c.req, err, c.want)
		}
	}
	isLocalAddress = func(string) bool { return false }
	if _, err := r.st.Do(context.Background(), Request{Op: "import", Files: []string{dir}}); err == nil || !strings.Contains(err.Error(), "another computer") {
		t.Errorf("remote Studio Pro: %v", err)
	}
	if len(r.opened) != 0 {
		t.Errorf("opened files on a refusal: %v", r.opened)
	}
}

func TestImportPathsAreAbsolute(t *testing.T) {
	home, _ := os.UserHomeDir()
	got, err := ImportPaths([]string{"~/Music/stems", "rel/x.wav", " "})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != filepath.Join(home, "Music", "stems") || !filepath.IsAbs(got[1]) {
		t.Errorf("paths = %v", got)
	}
}

func TestPeakDirNeedsAKnownSongFolder(t *testing.T) {
	r := newImportRig(t)
	if r.st.peakDir() != r.peaks {
		t.Errorf("peakDir = %q", r.st.peakDir())
	}
	for _, title := range []string{"", "../etc", "a/b", "Other"} {
		r.st.session.Model().mutate(func(rec func(string)) { r.st.session.Model().setStr("document/title", title, rec) })
		if got := r.st.peakDir(); got != "" {
			t.Errorf("title %q: peakDir = %q", title, got)
		}
	}
}
