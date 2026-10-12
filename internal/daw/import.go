package daw

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Importing audio, proven live on Studio Pro 8.0.2:
//
//   - Session / Import File ignores its arguments and opens the macOS file
//     panel, which a remote cannot close. Not used.
//   - Opening an audio file with Studio Pro (`open -a`) imports it into the
//     open song, onto the selected track at the playhead.
//   - So each stem gets its own new track (selected as it is added), the
//     cursor goes to the bar, then the file is opened.
//   - Studio Pro sends nothing over UCNET for an import (captures show the
//     new track, then nothing; the BeginProgress/EndProgress pair seen near
//     it belongs to Return to Zero). The one sign a clip landed is the
//     waveform file Studio Pro writes into the song's Cache/Images folder,
//     named <hash><file name><frames in hex>.peak. A file the song has seen
//     before already has one, so that import is given a measured settle time
//     instead, and the answer says so.

// Test seams.
var (
	// openInStudioPro hands one file to Studio Pro. Never through a shell.
	openInStudioPro = func(ctx context.Context, path string) error {
		app := "Studio Pro 8"
		if _, err := os.Stat(studioProApp); err == nil {
			app = studioProApp
		}
		// -g keeps Studio Pro where it is instead of bringing it forward.
		out, err := exec.CommandContext(ctx, "open", "-g", "-a", app, path).CombinedOutput()
		if err != nil {
			return fmt.Errorf("open: %v: %s", err, strings.TrimSpace(stripControl(string(out))))
		}
		return nil
	}
	songsDir = func() string {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "Documents", "Studio One", "Songs")
	}
	isLocalAddress    = localAddress
	landingTimeoutFor = landingTimeout
	// importSettle is how long an import is given when its landing cannot
	// be watched; landingSettle is the pause after the waveform file appears,
	// before the next track is added.
	importSettle  = 2 * time.Second
	landingSettle = 300 * time.Millisecond
	renameDelay   = 1500 * time.Millisecond
	renameGap     = 500 * time.Millisecond
	pollEvery     = 100 * time.Millisecond
)

const studioProApp = "/Applications/Studio Pro 8.app"

// ImportPaths makes paths absolute (expanding a leading ~) on the machine
// that names them, before a request crosses to serve.
func ImportPaths(paths []string) ([]string, error) {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if p == "~" || strings.HasPrefix(p, "~/") {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, err
			}
			p = filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			return nil, err
		}
		out = append(out, filepath.Clean(abs))
	}
	return out, nil
}

// imported is one stem in an import's answer.
type imported struct {
	File    string    `json:"file"`
	Track   string    `json:"track"`
	Channel string    `json:"channel"`
	Format  string    `json:"trackFormat"`
	Audio   AudioFile `json:"audio"`
	Bar     int       `json:"bar"`
	// Landed is how ucdaw knows Studio Pro took the file: "waveform" (its
	// waveform file appeared in the song's cache), "settle" (waited a
	// measured time; the song had seen this file before or its folder is
	// unknown).
	Landed  string  `json:"landed"`
	Seconds float64 `json:"landedAfter"`
	Dialog  string  `json:"dialog,omitempty"`
	// Clip is the name given to the clip, when ucdaw renamed it.
	Clip string `json:"clip,omitempty"`
}

func (st *Studio) importAudio(ctx context.Context, req Request) (Result, error) {
	if len(req.Files) == 0 {
		return Result{}, Problem{Message: "say which audio files or folder to import"}
	}
	if ip := st.session.Endpoint().IP; !isLocalAddress(ip) {
		return Result{}, Problem{
			Message: fmt.Sprintf("Studio Pro is on another computer (%s); ucdaw can only import files into Studio Pro on this Mac", ip),
			Hint:    "run ucdaw on the Mac that runs Studio Pro",
		}
	}
	bar := req.At
	if bar == 0 {
		bar = 1
	}
	if bar < 1 || bar > 9999 {
		return Result{}, Problem{Message: fmt.Sprintf("give a bar number from 1, not %d", req.At)}
	}
	files, skips, err := collectAudio(req.Files)
	if err != nil {
		return Result{}, err
	}
	if len(files) == 0 {
		msg := "Nothing to import"
		if len(skips) > 0 {
			msg += ": " + skipText(skips)
		}
		return Result{}, Problem{Message: msg, Hint: "ucdaw imports WAV and AIFF files"}
	}
	var onto Channel
	if strings.TrimSpace(req.Onto) != "" {
		if onto, err = st.resolveChannel(req.Onto); err != nil {
			return Result{}, err
		}
		if onto.Kind != "AudioTrack" {
			return Result{}, Problem{Message: fmt.Sprintf("%s is not an audio track", onto.Name), Hint: "pick an audio track, or leave out --onto to get new tracks"}
		}
	}
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.Path
	}
	names := trackNames(paths)
	peaks := st.peakDir()
	var done []imported
	for i, f := range files {
		one, err := st.importOne(ctx, f, names[i], onto, bar, peaks)
		if err != nil {
			return Result{}, importProblem(done, err)
		}
		done = append(done, one)
	}
	if onto.Path == "" {
		if err := st.nameClips(ctx, done); err != nil {
			return Result{}, importProblem(done, err)
		}
	}
	title, _ := st.model().Str("document/title")
	recordImports(title, done)
	if skips == nil {
		skips = []skipped{}
	}
	return Result{Message: importMessage(done, skips, onto, bar), Data: map[string]any{"imported": done, "skipped": skips}}, nil
}

func (st *Studio) importOne(ctx context.Context, f AudioFile, name string, onto Channel, bar int, peaks string) (imported, error) {
	if d, open := st.model().Dialog(); open {
		return imported{}, dialogProblem(d)
	}
	one := imported{File: filepath.Base(f.Path), Audio: f, Bar: bar}
	if onto.Path == "" {
		res, err := st.addTrack(ctx, f.Format(), name)
		if err != nil {
			return imported{}, err
		}
		data, _ := res.Data.(map[string]string)
		one.Channel, one.Track = data["path"], data["name"]
	} else {
		one.Channel, one.Track = onto.Path, onto.Name
	}
	// Studio Pro imports onto the selected track: make sure it is this one.
	if err := st.session.SetNum(ctx, one.Channel+"/selected", 1); err != nil {
		return imported{}, st.unconfirmed(err, "Selecting "+one.Track)
	}
	one.Format, _ = st.model().Str(one.Channel + "/speakerFormat")
	if err := st.invokeAll(ctx, cursorSteps(bar)...); err != nil {
		return imported{}, err
	}
	suffix := peakSuffix(f)
	watch := peaks != "" && !hasPeak(peaks, suffix)
	start := time.Now()
	if err := openInStudioPro(ctx, f.Path); err != nil {
		return imported{}, Problem{Message: fmt.Sprintf("Studio Pro did not take %s: %v", one.File, err)}
	}
	timeout := landingTimeoutFor(f)
	deadline := time.Now().Add(timeout)
	if !watch {
		deadline = time.Now().Add(importSettle)
	}
	for {
		if d, open := st.model().Dialog(); open {
			answer, err := st.answerImportDialog(ctx, d)
			if err != nil {
				return imported{}, err
			}
			one.Dialog = answer
			// A conversion keeps going; give it the full time again.
			deadline = time.Now().Add(timeout)
		}
		if watch && hasPeak(peaks, suffix) {
			one.Landed = "waveform"
			break
		}
		if time.Now().After(deadline) {
			if watch {
				return imported{}, Problem{
					Message: fmt.Sprintf("Studio Pro did not show it had read %s within %s", one.File, timeout.Round(time.Second)),
					Hint:    "check the song: the clip may still be loading",
				}
			}
			one.Landed = "settle"
			break
		}
		if err := pause(ctx, pollEvery); err != nil {
			return imported{}, err
		}
	}
	one.Seconds = time.Since(start).Round(10 * time.Millisecond).Seconds()
	if one.Landed == "waveform" {
		if err := pause(ctx, landingSettle); err != nil {
			return imported{}, err
		}
	}
	if d, open := st.model().Dialog(); open {
		answer, err := st.answerImportDialog(ctx, d)
		if err != nil {
			return imported{}, err
		}
		one.Dialog = answer
	}
	return one, nil
}

// nameClips gives each clip its track's name (Studio Pro names it after the
// file). It runs after the last import has had renameDelay to settle: a
// rename sent straight after an import was ignored live, while the same
// rename a second later worked. Each rename clears the selection first
// (Deselect All): the clip an import leaves selected otherwise took the
// previous track's name live. Then Select All on Tracks on the one new
// track, so a rename can only touch its clip.
func (st *Studio) nameClips(ctx context.Context, done []imported) error {
	if err := pause(ctx, renameDelay); err != nil {
		return err
	}
	for i := range done {
		if err := st.session.SetNum(ctx, done[i].Channel+"/selected", 1); err != nil {
			return st.unconfirmed(err, "Selecting "+done[i].Track)
		}
		if err := st.invokeAll(ctx,
			step{"Edit", "Deselect All", nil},
			step{"Edit", "Select All on Tracks", nil},
			step{"Event", "Rename Events", map[string]string{"New Name": done[i].Track}},
		); err != nil {
			return err
		}
		// Studio Pro applies a track selection at once but runs commands
		// a moment later; without this pause the next track's selection
		// overtook the rename and each clip took the previous track's
		// name (seen live).
		if err := pause(ctx, renameGap); err != nil {
			return err
		}
		done[i].Clip = done[i].Track
	}
	return nil
}

// answerImportDialog accepts a conversion question (the musician wants the
// stems in) and cancels anything else, reporting it.
func (st *Studio) answerImportDialog(ctx context.Context, d Dialog) (string, error) {
	text := strings.ToLower(d.Title + " " + d.Text)
	// A conversion question has a choice: two buttons, and words about
	// converting. A one-button alert is never one.
	conversion := false
	for _, w := range []string{"convert", "sample rate", "samplerate", "resample"} {
		if strings.Contains(text, w) && len(labeled(d)) == 2 {
			conversion = true
		}
	}
	// The first button that says yes to converting, never one that turns it
	// down: "Don't Convert" holds "convert" too.
	button := -1
	if conversion {
		for i, b := range d.Buttons {
			if !negativeLabel(b) && hasWord(b, "convert", "ok", "yes", "import", "apply", "continue") {
				button = i
				break
			}
		}
	}
	if button >= 0 {
		if err := st.session.AnswerDialog(ctx, button); err != nil {
			return "", Problem{Message: fmt.Sprintf("Studio Pro asked %q and ucdaw could not answer it", d.Title), Hint: "answer it in Studio Pro"}
		}
		return fmt.Sprintf("answered %q with %s", d.Title, d.Buttons[button]), nil
	}
	return "", st.dismiss(ctx, d, fmt.Sprintf("Studio Pro asked %q while importing", strings.TrimSpace(d.Title+" "+d.Text)), "")
}

// landingTimeout allows for big files: 15 s plus a second per 20 MB of
// audio, at most 3 minutes.
func landingTimeout(f AudioFile) time.Duration {
	bytes := float64(f.Frames) * float64(f.Channels) * float64(f.Bits) / 8
	// Capped as a float: a header can claim 2^64 frames, and converting that
	// to a time.Duration overflows.
	seconds := 15 + bytes/20e6
	if !(seconds <= 180) {
		return 3 * time.Minute
	}
	return time.Duration(seconds * float64(time.Second))
}

// peakDir is the open song's waveform cache, when the song lives where
// Studio Pro keeps songs and its folder is named after its title.
func (st *Studio) peakDir() string {
	title, _ := st.model().Str("document/title")
	title = strings.TrimSpace(title)
	if title == "" || strings.ContainsAny(title, "/\\:") || strings.Contains(title, "..") || stripControl(title) != title {
		return ""
	}
	song := filepath.Join(songsDir(), title)
	if _, err := os.Stat(filepath.Join(song, title+".song")); err != nil {
		if _, err := os.Stat(filepath.Join(song, title+".song.autosave")); err != nil {
			return ""
		}
	}
	return filepath.Join(song, "Cache", "Images")
}

// peakSuffix is the end of the waveform file name Studio Pro writes for f:
// its file name and its length in frames, in upper-case hex.
func peakSuffix(f AudioFile) string {
	return filepath.Base(f.Path) + strings.ToUpper(strconv.FormatUint(f.Frames, 16)) + ".peak"
}

func hasPeak(dir, suffix string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), suffix) {
			return true
		}
	}
	return false
}

func localAddress(ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	if parsed.IsLoopback() {
		return true
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.Equal(parsed) {
			return true
		}
	}
	return false
}

func importMessage(done []imported, skips []skipped, onto Channel, bar int) string {
	var b strings.Builder
	where := fmt.Sprintf("at bar %d", bar)
	if onto.Path != "" {
		fmt.Fprintf(&b, "Imported %s onto %s %s", fileList(done), onto.Name, where)
		if f := done[0].Format; len(done) == 1 && f != "" && !strings.EqualFold(f, done[0].Audio.Format()) {
			fmt.Fprintf(&b, " (a %s file on a %s track)", done[0].Audio.Format(), strings.ToLower(f))
		}
		if len(done) > 1 {
			b.WriteString("; they overlap there")
		}
	} else {
		var tracks []string
		for _, d := range done {
			tracks = append(tracks, fmt.Sprintf("%s (%s)", d.Track, d.Audio.Format()))
		}
		noun := "a new track"
		if len(done) > 1 {
			noun = "new tracks"
		}
		fmt.Fprintf(&b, "Imported %d %s onto %s %s: %s", len(done), stemWord(len(done)), noun, where, joinAnd(tracks))
	}
	b.WriteString(".")
	if len(skips) > 0 {
		b.WriteString(" Skipped " + skipText(skips) + ".")
	}
	for _, d := range done {
		if d.Dialog != "" {
			fmt.Fprintf(&b, " %s: Studio Pro %s.", d.File, d.Dialog)
		}
	}
	settled := 0
	for _, d := range done {
		if d.Landed == "settle" {
			settled++
		}
	}
	b.WriteString(" Tracks read back; Studio Pro does not report clips to remotes, so placement is not read back")
	switch {
	case settled == 0:
		b.WriteString(" (Studio Pro drew each file's waveform, so it read every file).")
	case settled == len(done):
		b.WriteString(".")
	default:
		fmt.Fprintf(&b, " (Studio Pro drew the waveform of %d of them; the others it had seen before).", len(done)-settled)
	}
	return b.String()
}

// importProblem says what went wrong and what had already been imported.
func importProblem(done []imported, err error) error {
	p, ok := err.(Problem)
	if !ok {
		p = Problem{Message: err.Error()}
	}
	if len(done) > 0 {
		var tracks []string
		for _, d := range done {
			tracks = append(tracks, d.Track)
		}
		p.Message = fmt.Sprintf("Imported %s, then stopped: %s", joinAnd(tracks), p.Message)
	}
	return p
}

func fileList(done []imported) string {
	var names []string
	for _, d := range done {
		names = append(names, d.File)
	}
	return joinAnd(names)
}

func stemWord(n int) string {
	if n == 1 {
		return "stem"
	}
	return "stems"
}

func skipText(skips []skipped) string {
	var parts []string
	for _, s := range skips {
		parts = append(parts, fmt.Sprintf("%s (%s)", s.File, s.Reason))
	}
	return joinAnd(parts)
}

func pause(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
