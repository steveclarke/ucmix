package daw

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Which file is on which track. Studio Pro does not tell remotes what audio
// a track plays, so ucdaw keeps its own record of every import (song title,
// track, file), and for songs imported some other way it reads the waveform
// files Studio Pro keeps in the song's Cache/Images folder: each is named
// after the audio file and its length in frames, which is enough to find
// the file and be sure it is the same one.

// importRecord is one file ucdaw imported onto a track.
type importRecord struct {
	Song  string    `json:"song"`
	Track string    `json:"track"`
	Path  string    `json:"path"`
	At    time.Time `json:"at"`
}

// Bounds on the import log, so a damaged or hostile file cannot exhaust
// memory.
const (
	maxImportRecords = 2000
	maxImportLog     = 2 << 20
)

// Test seams.
var (
	// importLogPath is imports.json beside the favorites, or $UCDAW_IMPORTS.
	importLogPath = func() string {
		if p := os.Getenv("UCDAW_IMPORTS"); p != "" {
			return p
		}
		dir, err := os.UserConfigDir()
		if err != nil {
			return ""
		}
		return filepath.Join(dir, "ucdaw", "imports.json")
	}
	// findFilesNamed asks Spotlight for files with exactly this name.
	findFilesNamed = spotlight
)

func loadImportLog() []importRecord {
	path := importLogPath()
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxImportLog+1))
	if err != nil || len(data) > maxImportLog {
		return nil
	}
	var file struct {
		Imports []importRecord `json:"imports"`
	}
	if json.Unmarshal(data, &file) != nil {
		return nil
	}
	if len(file.Imports) > maxImportRecords {
		file.Imports = file.Imports[len(file.Imports)-maxImportRecords:]
	}
	return file.Imports
}

// recordImports adds an import to the log, best effort: losing the log only
// means analysis falls back to the waveform cache.
func recordImports(song string, done []imported) {
	path := importLogPath()
	if path == "" || len(done) == 0 {
		return
	}
	recs := loadImportLog()
	now := time.Now().UTC()
	for _, d := range done {
		recs = append(recs, importRecord{Song: stripControl(song), Track: stripControl(d.Track), Path: stripControl(d.Audio.Path), At: now})
	}
	if len(recs) > maxImportRecords {
		recs = recs[len(recs)-maxImportRecords:]
	}
	data, err := json.MarshalIndent(struct {
		Imports []importRecord `json:"imports"`
	}{recs}, "", "  ")
	if err != nil || len(data) > maxImportLog {
		return
	}
	dir := filepath.Dir(path)
	if os.MkdirAll(dir, 0o700) != nil {
		return
	}
	tmp, err := os.CreateTemp(dir, ".imports-*.json")
	if err != nil {
		return
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Chmod(tmp.Name(), 0o600) != nil || os.Rename(tmp.Name(), path) != nil {
		_ = os.Remove(tmp.Name())
	}
}

// songFolder is the open song's folder under Studio Pro's Songs folder, when
// its title can name one safely and it exists.
func (st *Studio) songFolder() string {
	title, _ := st.model().Str("document/title")
	title = strings.TrimSpace(title)
	if title == "" || strings.ContainsAny(title, "/\\:") || strings.Contains(title, "..") || stripControl(title) != title {
		return ""
	}
	dir := filepath.Join(songsDir(), title)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return ""
	}
	return dir
}

// stemFiles finds the audio files on a track: from ucdaw's import log, else
// from the song's waveform cache.
func (st *Studio) stemFiles(ctx context.Context, ch Channel) ([]string, error) {
	title, _ := st.model().Str("document/title")
	var out []string
	seen := map[string]bool{}
	for _, r := range loadImportLog() {
		if r.Song == title && strings.EqualFold(r.Track, ch.Name) && !seen[r.Path] && isFile(r.Path) {
			seen[r.Path] = true
			out = append(out, r.Path)
		}
	}
	if len(out) > 0 {
		return out, nil
	}
	if path := st.stemFromCache(ctx, ch.Name); path != "" {
		return []string{path}, nil
	}
	hint := "measure the file itself: ucdaw analyze <file or folder>"
	if ch.Kind != "" && ch.Kind != "AudioTrack" {
		hint = fmt.Sprintf("%s is not an audio track; only audio tracks have stems", ch.Name)
	}
	return nil, Problem{Message: fmt.Sprintf("no stem file found for %s", ch.Name), Hint: hint}
}

// cachedStem is one audio file named in the song's waveform cache.
type cachedStem struct {
	file   string
	frames uint64
}

func (st *Studio) stemFromCache(ctx context.Context, track string) string {
	song := st.songFolder()
	if song == "" {
		return ""
	}
	entries, err := os.ReadDir(filepath.Join(song, "Cache", "Images"))
	if err != nil {
		return ""
	}
	var stems []cachedStem
	seen := map[string]bool{}
	for i, e := range entries {
		if i >= 4096 {
			break
		}
		file, frames, ok := parsePeakName(e.Name())
		if ok && !seen[file+strconv.FormatUint(frames, 16)] {
			seen[file+strconv.FormatUint(frames, 16)] = true
			stems = append(stems, cachedStem{file, frames})
		}
	}
	names := make([]string, len(stems))
	for i, s := range stems {
		names[i] = s.file
	}
	tracks := trackNames(names)
	for i, s := range stems {
		if !strings.EqualFold(tracks[i], track) {
			continue
		}
		if path := locateStem(ctx, song, s, &st.stemDirs); path != "" {
			return path
		}
	}
	return ""
}

// locateStem looks for the file in the song's Media folder, the folders of
// earlier imports, then Spotlight, and takes the first with the same length.
func locateStem(ctx context.Context, song string, s cachedStem, known *stemDirs) string {
	var candidates []string
	candidates = append(candidates, filepath.Join(song, "Media", s.file))
	dirs := map[string]bool{}
	for _, r := range loadImportLog() {
		dirs[filepath.Dir(r.Path)] = true
	}
	for _, d := range known.list() {
		dirs[d] = true
	}
	sorted := make([]string, 0, len(dirs))
	for d := range dirs {
		sorted = append(sorted, d)
	}
	sort.Strings(sorted)
	for _, d := range sorted {
		candidates = append(candidates, filepath.Join(d, s.file))
	}
	if c := matchStem(candidates, s); c != "" {
		return c
	}
	if c := matchStem(findFilesNamed(ctx, s.file), s); c != "" {
		known.add(filepath.Dir(c))
		return c
	}
	return ""
}

// matchStem is the first candidate with the stem's name and length.
func matchStem(candidates []string, s cachedStem) string {
	for _, c := range candidates {
		if filepath.Base(c) != s.file || !isFile(c) {
			continue
		}
		if a, err := ReadAudioFile(c); err == nil && a.Frames == s.frames {
			return c
		}
	}
	return ""
}

// stemDirs remembers up to 32 folders stems were found in.
type stemDirs struct {
	mu   sync.Mutex
	dirs []string
}

func (d *stemDirs) list() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.dirs...)
}

func (d *stemDirs) add(dir string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, x := range d.dirs {
		if x == dir {
			return
		}
	}
	if len(d.dirs) == 32 {
		d.dirs = d.dirs[1:]
	}
	d.dirs = append(d.dirs, dir)
}

// peakExtensions are the audio extensions, longest first, so "Vox.aiff10"
// is Vox.aiff with 0x10 frames, not Vox.aif with 0xF10.
var peakExtensions = func() []string {
	var out []string
	for ext := range audioExtensions {
		out = append(out, ext)
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) || len(out[i]) == len(out[j]) && out[i] < out[j] })
	return out
}()

// parsePeakName reads Studio Pro's waveform file name:
// x<8 hex digits><audio file name><frames in upper-case hex>.peak.
func parsePeakName(name string) (file string, frames uint64, ok bool) {
	rest, found := strings.CutSuffix(name, ".peak")
	if !found || len(rest) < 9 || rest[0] != 'x' {
		return "", 0, false
	}
	rest = rest[9:]
	lower := strings.ToLower(rest)
	for _, ext := range peakExtensions {
		i := strings.LastIndex(lower, ext)
		if i <= 0 {
			continue
		}
		hex := rest[i+len(ext):]
		// The extension is written as the file has it, all lower or all
		// upper case: "Vox.aifF00" is Vox.aif with 0xF00 frames.
		if got := rest[i : i+len(ext)]; got != ext && got != strings.ToUpper(ext) {
			continue
		}
		if hex == "" || strings.ToUpper(hex) != hex {
			continue
		}
		n, err := strconv.ParseUint(hex, 16, 64)
		if err != nil {
			continue
		}
		file = rest[:i+len(ext)]
		if strings.ContainsAny(file, "/\\") || stripControl(file) != file {
			return "", 0, false
		}
		return file, n, true
	}
	return "", 0, false
}

func isFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular()
}

// spotlight runs mdfind for an exact file name: at most 5 seconds, 1 MB of
// output and 50 results; never through a shell.
func spotlight(ctx context.Context, name string) []string {
	if name == "" || strings.ContainsAny(name, "\"\\\n\r") {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "mdfind", "-name", name)
	var out bytes.Buffer
	cmd.Stdout = &limitedWriter{w: &out, n: 1 << 20}
	if cmd.Run() != nil && out.Len() == 0 {
		return nil
	}
	var paths []string
	for _, line := range strings.Split(out.String(), "\n") {
		if filepath.Base(line) == name && filepath.IsAbs(line) {
			paths = append(paths, line)
		}
		if len(paths) == 50 {
			break
		}
	}
	// Prefer files already on this Mac over cloud folders that may have to
	// download them first.
	sort.SliceStable(paths, func(i, j int) bool {
		return !strings.Contains(paths[i], "/Library/CloudStorage/") && strings.Contains(paths[j], "/Library/CloudStorage/")
	})
	return paths
}

type limitedWriter struct {
	w io.Writer
	n int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.n <= 0 {
		return len(p), nil
	}
	keep := p
	if len(keep) > l.n {
		keep = keep[:l.n]
	}
	l.n -= len(keep)
	if _, err := l.w.Write(keep); err != nil {
		return 0, err
	}
	return len(p), nil
}
