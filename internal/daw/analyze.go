package daw

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// Measuring stems. A mix engineer measures each stem before touching a level:
// how loud it is on average while it plays, how high it peaks, whether it is
// an empty export, whether it clips, and how much input trim brings it to a
// common level so the faders start from a sensible place.
//
//   - Average is the RMS level over the stem's active audio: 100 ms windows
//     whose RMS is below SilenceDBFS are left out, so the gaps between
//     phrases do not drag a sparse part down. RMS is plain (a full-scale
//     sine reads -3.01 dBFS), not the AES17 convention that adds 3 dB.
//   - LUFS is integrated loudness per ITU-R BS.1770-4: K-weighting (a high
//     shelf and a high-pass, recomputed for the file's sample rate as
//     libebur128 does), 400 ms blocks every 100 ms, an absolute gate at
//     -70 LUFS and a relative gate 10 LU under the gated mean.
//   - A stem whose peak is below SilenceDBFS is silent: an empty export.
//   - Clipping is three or more consecutive samples at full scale in an
//     integer file, or any sample over full scale in a float file.
//
// Files are streamed through a fixed buffer; memory does not grow with the
// file except for one float per 100 ms block, capped at maxBlocks.

// DefaultTarget is the average level gain staging aims for: -18 dBFS RMS,
// the level digital consoles and hardware-modeled plug-ins treat as 0 VU,
// which leaves about 18 dB of headroom above the average for peaks.
const DefaultTarget = -18.0

// SilenceDBFS is the level below which a 100 ms window is a gap, and below
// which a whole stem's peak means nothing was exported.
const SilenceDBFS = -60.0

// TrimMin and TrimMax are Studio Pro's input gain range.
const (
	TrimMin = -24.0
	TrimMax = 24.0
)

// maxBlocks bounds the loudness blocks kept per stem: 100 ms each, so ten
// hours. A test seam.
var maxBlocks = 360_000

// MaxAnalyzeFiles bounds one analysis.
const MaxAnalyzeFiles = 64

// analyzeBuffer is the read buffer per stem.
const analyzeBuffer = 1 << 20

// Levels is one stem's measurement.
type Levels struct {
	Name       string  `json:"name"` // the stem's name, or the track it is on
	File       string  `json:"file"`
	Path       string  `json:"path"`
	Channels   int     `json:"channels"`
	SampleRate float64 `json:"sampleRate"`
	Bits       int     `json:"bits"`
	Float      bool    `json:"float,omitempty"`
	Seconds    float64 `json:"seconds"`
	// ActiveSeconds is how long the stem plays above SilenceDBFS.
	ActiveSeconds float64 `json:"activeSeconds"`
	// PeakDBFS, AverageDBFS and LUFS are null when there is nothing to
	// measure (digital silence, or too short for a loudness block).
	PeakDBFS    *float64 `json:"peakDBFS"`
	AverageDBFS *float64 `json:"averageDBFS"`
	LUFS        *float64 `json:"lufs"`
	Silent      bool     `json:"silent"`
	Clipping    bool     `json:"clipping"`
	// ClippedSamples counts samples in clipped runs (integer files) or over
	// full scale (float files).
	ClippedSamples int64 `json:"clippedSamples"`
	// TargetDBFS is the average the trim aims for; TrimDB the input trim
	// that gets there within Studio Pro's range; NeededDB what it would take
	// without the range limit. Null for a silent stem.
	TargetDBFS  float64  `json:"targetDBFS"`
	TrimDB      *float64 `json:"suggestedTrimDB"`
	NeededDB    *float64 `json:"neededDB,omitempty"`
	TrimLimited bool     `json:"trimLimited,omitempty"`
	// Partial is set when only the first maxBlocks of a very long file were
	// measured.
	Partial bool `json:"partial,omitempty"`
	// Line is the one-line report for the musician.
	Line string `json:"line"`
}

// stats accumulates one stem's measurement as samples stream past.
type stats struct {
	channels  int
	rate      float64
	frames    int64
	peak      float64
	fullScale float64 // integer files: the magnitude that counts as full scale
	isFloat   bool
	clipped   int64
	runs      []int // per channel: current run of full-scale samples

	// 100 ms windows.
	winFrames   int
	winPos      int
	winSquares  float64   // plain sum of squares, all channels
	winK        []float64 // K-weighted sum of squares, per channel
	activeWins  int64
	activePower float64 // sum of active windows' mean squares
	blocks      []float64
	partial     bool

	k []kFilter
}

// kFilter is the BS.1770 K-weighting: a high shelf then a high-pass, as two
// biquads (direct form I) for one channel.
type kFilter struct {
	b1, a1, b2, a2 [3]float64
	x1, y1, x2, y2 [2]float64
}

func newKFilter(rate float64) kFilter {
	var f kFilter
	// Stage 1: high shelf (+4 dB above about 1.7 kHz). Constants from
	// libebur128, which derives them from the BS.1770 48 kHz coefficients.
	f0, gain, q := 1681.974450955533, 3.999843853973347, 0.7071752369554196
	k := math.Tan(math.Pi * f0 / rate)
	vh := math.Pow(10, gain/20)
	vb := math.Pow(vh, 0.4996667741545416)
	a0 := 1 + k/q + k*k
	f.b1 = [3]float64{(vh + vb*k/q + k*k) / a0, 2 * (k*k - vh) / a0, (vh - vb*k/q + k*k) / a0}
	f.a1 = [3]float64{1, 2 * (k*k - 1) / a0, (1 - k/q + k*k) / a0}
	// Stage 2: high-pass at about 38 Hz (the RLB curve).
	f0, q = 38.13547087602444, 0.5003270373238773
	k = math.Tan(math.Pi * f0 / rate)
	a0 = 1 + k/q + k*k
	f.b2 = [3]float64{1, -2, 1}
	f.a2 = [3]float64{1, 2 * (k*k - 1) / a0, (1 - k/q + k*k) / a0}
	return f
}

func (f *kFilter) step(x float64) float64 {
	y := f.b1[0]*x + f.b1[1]*f.x1[0] + f.b1[2]*f.x1[1] - f.a1[1]*f.y1[0] - f.a1[2]*f.y1[1]
	f.x1[1], f.x1[0] = f.x1[0], x
	f.y1[1], f.y1[0] = f.y1[0], y
	z := f.b2[0]*y + f.b2[1]*f.x2[0] + f.b2[2]*f.x2[1] - f.a2[1]*f.y2[0] - f.a2[2]*f.y2[1]
	f.x2[1], f.x2[0] = f.x2[0], y
	f.y2[1], f.y2[0] = f.y2[0], z
	return z
}

func newStats(a AudioFile) *stats {
	s := &stats{
		channels:  a.Channels,
		rate:      a.SampleRate,
		isFloat:   a.encoding == "float",
		runs:      make([]int, a.Channels),
		winFrames: max(1, int(math.Round(a.SampleRate/10))),
		winK:      make([]float64, a.Channels),
		k:         make([]kFilter, a.Channels),
	}
	if !s.isFloat {
		full := math.Ldexp(1, (a.blockAlign/a.Channels)*8-1)
		if a.encoding == "uint8" {
			full = 128
		}
		s.fullScale = (full - 1) / full
	}
	for c := range s.k {
		s.k[c] = newKFilter(a.SampleRate)
	}
	return s
}

// frame takes one frame of samples; it returns false once the block cap is
// reached.
func (s *stats) frame(x []float64) bool {
	for c, v := range x {
		m := math.Abs(v)
		if m > s.peak {
			s.peak = m
		}
		if s.isFloat {
			if m > 1 {
				s.clipped++
			}
		} else if m >= s.fullScale {
			s.runs[c]++
			switch {
			case s.runs[c] == 3:
				s.clipped += 3
			case s.runs[c] > 3:
				s.clipped++
			}
		} else {
			s.runs[c] = 0
		}
		s.winSquares += v * v
		z := s.k[c].step(v)
		s.winK[c] += z * z
	}
	s.frames++
	s.winPos++
	if s.winPos == s.winFrames {
		return s.closeWindow()
	}
	return true
}

func (s *stats) closeWindow() bool {
	n := float64(s.winPos)
	mean := s.winSquares / (n * float64(s.channels))
	if mean > 0 && 10*math.Log10(mean) >= SilenceDBFS {
		s.activeWins++
		s.activePower += mean
	}
	var k float64
	for c := range s.winK {
		k += s.winK[c] / n // channel weight 1.0 for mono, left and right
		s.winK[c] = 0
	}
	s.blocks = append(s.blocks, k)
	s.winSquares, s.winPos = 0, 0
	if len(s.blocks) >= maxBlocks {
		s.partial = true
		return false
	}
	return true
}

// lufs is BS.1770 integrated loudness over the 100 ms windows: 400 ms blocks
// (four windows) every 100 ms, gated at -70 LUFS, then 10 LU below the mean
// of what passed.
func (s *stats) lufs() (float64, bool) {
	if len(s.blocks) < 4 {
		return 0, false
	}
	loud := func(p float64) float64 { return -0.691 + 10*math.Log10(p) }
	powers := make([]float64, 0, len(s.blocks)-3)
	for i := 0; i+4 <= len(s.blocks); i++ {
		p := (s.blocks[i] + s.blocks[i+1] + s.blocks[i+2] + s.blocks[i+3]) / 4
		if p > 0 && loud(p) > -70 {
			powers = append(powers, p)
		}
	}
	if len(powers) == 0 {
		return 0, false
	}
	var sum float64
	for _, p := range powers {
		sum += p
	}
	relative := loud(sum/float64(len(powers))) - 10
	var gated float64
	n := 0
	for _, p := range powers {
		if loud(p) > relative {
			gated += p
			n++
		}
	}
	if n == 0 {
		return 0, false
	}
	return loud(gated / float64(n)), true
}

// decoder turns raw sample bytes into floats, full scale = 1.
func decoder(a AudioFile) (func(b []byte) float64, int, error) {
	if a.Channels < 1 || a.blockAlign < a.Channels || a.blockAlign%a.Channels != 0 {
		return nil, 0, errNotAudio
	}
	width := a.blockAlign / a.Channels
	le := !a.bigEndian
	switch {
	case a.encoding == "float" && width == 4:
		return func(b []byte) float64 {
			var u uint32
			if le {
				u = uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
			} else {
				u = uint32(b[3]) | uint32(b[2])<<8 | uint32(b[1])<<16 | uint32(b[0])<<24
			}
			v := float64(math.Float32frombits(u))
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return 0
			}
			return v
		}, width, nil
	case a.encoding == "float" && width == 8:
		return func(b []byte) float64 {
			var u uint64
			for i := 0; i < 8; i++ {
				j := i
				if !le {
					j = 7 - i
				}
				u |= uint64(b[j]) << (8 * i)
			}
			v := math.Float64frombits(u)
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return 0
			}
			return v
		}, width, nil
	case a.encoding == "uint8" && width == 1:
		return func(b []byte) float64 { return (float64(b[0]) - 128) / 128 }, width, nil
	case a.encoding == "int" && width >= 1 && width <= 4:
		scale := 1 / math.Ldexp(1, 8*width-1)
		shift := uint(32 - 8*width)
		return func(b []byte) float64 {
			var u uint32
			for i := 0; i < width; i++ {
				j := i
				if !le {
					j = width - 1 - i
				}
				u |= uint32(b[j]) << (8 * i)
			}
			// Sign-extend from the sample's width.
			return float64(int32(u<<shift)>>shift) * scale
		}, width, nil
	}
	return nil, 0, errUnsupportedEncoding
}

var errUnsupportedEncoding = errors.New("unsupported sample encoding")

// measure streams a file's sample data through the stats.
func measure(ctx context.Context, r io.ReadSeeker, a AudioFile) (*stats, error) {
	if a.Channels > 2 {
		return nil, errTooManyChannels
	}
	decode, width, err := decoder(a)
	if err != nil {
		return nil, err
	}
	if a.SampleRate < 1000 || a.SampleRate > 1_000_000 {
		return nil, errNotAudio
	}
	if _, err := r.Seek(a.dataOffset, io.SeekStart); err != nil {
		return nil, err
	}
	s := newStats(a)
	frameBytes := a.blockAlign
	buf := make([]byte, (analyzeBuffer/frameBytes)*frameBytes)
	if len(buf) == 0 {
		buf = make([]byte, frameBytes)
	}
	x := make([]float64, a.Channels)
	src := io.LimitReader(bufio.NewReaderSize(r, 64<<10), a.dataBytes)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, err := io.ReadFull(src, buf)
		n -= n % frameBytes // a cut-off last frame is dropped
		for off := 0; off < n; off += frameBytes {
			for c := 0; c < a.Channels; c++ {
				p := off + c*width
				x[c] = decode(buf[p : p+width])
			}
			if !s.frame(x) {
				return s, nil
			}
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	if s.winPos > 0 {
		s.closeWindow()
	}
	return s, nil
}

var errTooManyChannels = errors.New("more than two channels")

// AnalyzeFile measures one stem against an average target in dBFS.
func AnalyzeFile(ctx context.Context, path string, target float64) (Levels, error) {
	a, err := ReadAudioFile(path)
	if err != nil {
		return Levels{}, analyzeError(path, err)
	}
	if a.dataBytes == 0 && a.Frames > 0 {
		return Levels{}, Problem{Message: fmt.Sprintf("%s has no sound data ucdaw can find", filepath.Base(path))}
	}
	f, err := os.Open(path)
	if err != nil {
		return Levels{}, analyzeError(path, err)
	}
	defer func() { _ = f.Close() }()
	s, err := measure(ctx, f, a)
	if err != nil {
		return Levels{}, analyzeError(path, err)
	}
	lv := s.levels(a, target)
	lv.Name = cleanStemName(path)
	lv.Line = levelsLine(lv)
	return lv, nil
}

func analyzeError(path string, err error) error {
	name := stripControl(filepath.Base(path))
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, os.ErrNotExist):
		return Problem{Message: fmt.Sprintf("%s not found", name)}
	case errors.Is(err, errTooManyChannels):
		return Problem{Message: fmt.Sprintf("%s has more than two channels; ucdaw measures mono and stereo files", name)}
	case errors.Is(err, errUnsupportedEncoding):
		return Problem{Message: fmt.Sprintf("%s uses a sample encoding ucdaw does not read (compressed AIFC?)", name), Hint: "export it as 16-, 24- or 32-bit PCM or 32-bit float WAV"}
	case errors.Is(err, errNotAudio):
		return Problem{Message: fmt.Sprintf("%s is not a WAV or AIFF file ucdaw can measure", name)}
	}
	return Problem{Message: fmt.Sprintf("could not read %s: %v", name, err)}
}

func (s *stats) levels(a AudioFile, target float64) Levels {
	lv := Levels{
		File:           stripControl(filepath.Base(a.Path)),
		Path:           a.Path,
		Channels:       a.Channels,
		SampleRate:     a.SampleRate,
		Bits:           a.Bits,
		Float:          s.isFloat,
		Seconds:        round3(float64(s.frames) / s.rate),
		ActiveSeconds:  round3(float64(s.activeWins) * float64(s.winFrames) / s.rate),
		ClippedSamples: s.clipped,
		Clipping:       s.clipped > 0,
		TargetDBFS:     target,
		Partial:        s.partial,
	}
	if lv.ActiveSeconds > lv.Seconds {
		lv.ActiveSeconds = lv.Seconds
	}
	if s.peak > 0 {
		lv.PeakDBFS = ptr(round2(20 * math.Log10(s.peak)))
	}
	lv.Silent = s.peak == 0 || 20*math.Log10(s.peak) < SilenceDBFS
	if !lv.Silent && s.activeWins > 0 {
		avg := 10 * math.Log10(s.activePower/float64(s.activeWins))
		lv.AverageDBFS = ptr(round2(avg))
		trim, needed, limited := suggestTrim(avg, target)
		lv.TrimDB, lv.TrimLimited = ptr(trim), limited
		if limited {
			lv.NeededDB = ptr(needed)
		}
	}
	if l, ok := s.lufs(); ok && !lv.Silent {
		lv.LUFS = ptr(round2(l))
	}
	return lv
}

// suggestTrim is the input trim, to 0.1 dB, that brings an average level to
// the target, within Studio Pro's -24..+24 dB; needed is the trim without
// that limit.
func suggestTrim(average, target float64) (trim, needed float64, limited bool) {
	needed = math.Round((target-average)*10) / 10
	if needed == 0 {
		needed = 0 // no "-0"
	}
	trim = math.Max(TrimMin, math.Min(TrimMax, needed))
	return trim, needed, trim != needed
}

func ptr(v float64) *float64 { return &v }

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

// levelsLine is the musician's line for one stem: "Bass: average -45.2 dBFS
// over 4:30 of 4:45, peak -30.5 dBFS, -45.5 LUFS; trim +24 dB (needs +27.2;
// the input trim stops at +24, so raise the fader 3.2 dB)."
func levelsLine(lv Levels) string {
	name := lv.Name
	if name == "" {
		name = lv.File
	}
	if lv.Silent {
		peak := "nothing in it"
		if lv.PeakDBFS != nil {
			peak = "peak " + dbfs(*lv.PeakDBFS)
		}
		return fmt.Sprintf("%s is silent: an empty export (%s over %s). Re-export it before mixing.", name, peak, clock(lv.Seconds))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s: average %s", name, dbfs(derefOr(lv.AverageDBFS, math.Inf(-1))))
	fmt.Fprintf(&b, " over %s of %s", clock(lv.ActiveSeconds), clock(lv.Seconds))
	if lv.PeakDBFS != nil {
		fmt.Fprintf(&b, ", peak %s", dbfs(*lv.PeakDBFS))
	}
	if lv.LUFS != nil {
		fmt.Fprintf(&b, ", %s LUFS", signed(*lv.LUFS))
	}
	switch {
	case lv.TrimDB != nil && lv.TrimLimited && lv.NeededDB != nil:
		short := math.Abs(math.Round((*lv.NeededDB-*lv.TrimDB)*10) / 10)
		fmt.Fprintf(&b, "; trim %s, as far as the input trim goes (it stops at %s): reaching %s needs %s, so it stays %s dB short",
			signedDB(*lv.TrimDB), signedDB(*lv.TrimDB), dbfs(lv.TargetDBFS), signedDB(*lv.NeededDB), trim(short))
	case lv.TrimDB != nil:
		fmt.Fprintf(&b, "; trim %s to reach %s", signedDB(*lv.TrimDB), dbfs(lv.TargetDBFS))
	}
	if lv.Clipping {
		fmt.Fprintf(&b, "; clipped (%d samples at full scale): ask for a re-export with less level", lv.ClippedSamples)
	}
	if lv.Partial {
		b.WriteString("; measured the first 10 hours only")
	}
	return b.String() + "."
}

func derefOr(p *float64, def float64) float64 {
	if p == nil {
		return def
	}
	return *p
}

func dbfs(v float64) string {
	if math.IsInf(v, -1) {
		return "-inf dBFS"
	}
	return signed(math.Round(v*10)/10) + " dBFS"
}

func signedDB(v float64) string { return signed(v) + " dB" }

func signed(v float64) string {
	r := math.Round(v*10) / 10
	if r == 0 {
		return "0"
	}
	s := trim(r)
	if r > 0 {
		s = "+" + s
	}
	return s
}

// Analyze measures audio files and folders of stems (in name order, at most
// MaxAnalyzeFiles), several at a time, and reports one line per stem.
func Analyze(ctx context.Context, paths []string, target float64) (Result, error) {
	files, skips, err := collectAnalyzable(paths)
	if err != nil {
		return Result{}, err
	}
	if len(files) == 0 {
		msg := "Nothing to measure"
		if len(skips) > 0 {
			msg += ": " + skipText(skips)
		}
		return Result{}, Problem{Message: msg, Hint: "ucdaw measures WAV and AIFF files"}
	}
	names := trackNames(files)
	all, err := analyzeAll(ctx, files, names, target)
	if err != nil {
		return Result{}, err
	}
	return levelsResult(all, skips, target), nil
}

// collectAnalyzable is collectAudio's walk without its import-only rules:
// every WAV or AIFF is kept by path; what cannot be read is reported later.
func collectAnalyzable(paths []string) ([]string, []skipped, error) {
	audio, skips, err := collectAudio(paths)
	if err != nil {
		return nil, nil, err
	}
	out := make([]string, len(audio))
	for i, a := range audio {
		out[i] = a.Path
	}
	if len(out) > MaxAnalyzeFiles {
		return nil, nil, Problem{Message: fmt.Sprintf("that is %d audio files; ucdaw measures at most %d at a time", len(out), MaxAnalyzeFiles)}
	}
	return out, skips, nil
}

// analyzeAll measures files in parallel (a few at a time: the disk, not the
// CPU, is usually the limit) and keeps their order.
func analyzeAll(ctx context.Context, files, names []string, target float64) ([]Levels, error) {
	out := make([]Levels, len(files))
	errs := make([]error, len(files))
	workers := min(len(files), max(1, min(runtime.NumCPU(), 4)))
	next := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				lv, err := AnalyzeFile(ctx, files[i], target)
				if err == nil && i < len(names) && names[i] != "" {
					lv.Name = names[i]
					lv.Line = levelsLine(lv)
				}
				out[i], errs[i] = lv, err
			}
		}()
	}
	for i := range files {
		next <- i
	}
	close(next)
	wg.Wait()
	for _, err := range errs {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
	}
	// A stem that cannot be read becomes its own line, not a failure of
	// the whole measurement.
	for i, err := range errs {
		if err != nil {
			name := cleanStemName(files[i])
			if i < len(names) && names[i] != "" {
				name = names[i]
			}
			out[i] = Levels{Name: name, File: stripControl(filepath.Base(files[i])), Path: files[i], TargetDBFS: target,
				Line: fmt.Sprintf("%s: %s.", name, problemText(err))}
		}
	}
	return out, nil
}

func levelsResult(all []Levels, skips []skipped, target float64) Result {
	lines := make([]string, 0, len(all)+1)
	head := fmt.Sprintf("Measured %d %s against a %s average target", len(all), stemWord(len(all)), dbfs(target))
	if len(skips) > 0 {
		head += "; skipped " + skipText(skips)
	}
	lines = append(lines, head+":")
	for _, lv := range all {
		lines = append(lines, lv.Line)
	}
	if skips == nil {
		skips = []skipped{}
	}
	return Result{Message: strings.Join(lines, "\n"), Data: map[string]any{"stems": all, "skipped": skips, "targetDBFS": target}}
}

// CheckTarget refuses targets no engineer would use.
func CheckTarget(target float64) error {
	if math.IsNaN(target) || target < -40 || target > -6 {
		return Problem{Message: fmt.Sprintf("a target of %s dBFS is outside -40 to -6", trim(target)), Hint: "the usual target is -18"}
	}
	return nil
}
