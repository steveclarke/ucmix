package daw

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// encodeAudio writes interleaved samples (-1..1, or beyond for float) as a
// WAV, AIFF or AIFC file in the given encoding: "int" (8, 16, 24 or 32 bits),
// "float" (32 or 64 bits). AIFC takes a compression type: "NONE", "sowt",
// "fl32".
func encodeAudio(t *testing.T, container, encoding string, bits, channels int, rate float64, samples []float64, aifcType string) []byte {
	t.Helper()
	bytesPer := bits / 8
	little := container == "WAV" || aifcType == "sowt"
	var data bytes.Buffer
	for _, x := range samples {
		var b [8]byte
		switch {
		case encoding == "float" && bits == 32:
			binOrder(little).PutUint32(b[:], math.Float32bits(float32(x)))
		case encoding == "float":
			binOrder(little).PutUint64(b[:], math.Float64bits(x))
		case bits == 8 && container == "WAV":
			b[0] = byte(int(math.Round(clamp1(x)*127)) + 128)
		default:
			full := math.Ldexp(1, bits-1)
			v := int64(math.Round(clamp1(x) * full))
			if v >= int64(full) {
				v = int64(full) - 1
			}
			u := uint64(v)
			for i := 0; i < bytesPer; i++ {
				shift := uint(8 * i)
				if !little {
					shift = uint(8 * (bytesPer - 1 - i))
				}
				b[i] = byte(u >> shift)
			}
		}
		data.Write(b[:bytesPer])
	}
	frames := len(samples) / channels
	var out bytes.Buffer
	switch container {
	case "WAV":
		tag := uint16(1)
		if encoding == "float" {
			tag = 3
		}
		var body bytes.Buffer
		body.WriteString("WAVE")
		body.WriteString("fmt ")
		_ = binary.Write(&body, binary.LittleEndian, uint32(16))
		_ = binary.Write(&body, binary.LittleEndian, tag)
		_ = binary.Write(&body, binary.LittleEndian, uint16(channels))
		_ = binary.Write(&body, binary.LittleEndian, uint32(rate))
		_ = binary.Write(&body, binary.LittleEndian, uint32(int(rate)*channels*bytesPer))
		_ = binary.Write(&body, binary.LittleEndian, uint16(channels*bytesPer))
		_ = binary.Write(&body, binary.LittleEndian, uint16(bits))
		body.WriteString("data")
		_ = binary.Write(&body, binary.LittleEndian, uint32(data.Len()))
		body.Write(data.Bytes())
		out.WriteString("RIFF")
		_ = binary.Write(&out, binary.LittleEndian, uint32(body.Len()))
		out.Write(body.Bytes())
	default:
		var body bytes.Buffer
		kind := "AIFF"
		if aifcType != "" {
			kind = "AIFC"
		}
		body.WriteString(kind)
		comm := make([]byte, 18, 22)
		binary.BigEndian.PutUint16(comm[0:], uint16(channels))
		binary.BigEndian.PutUint32(comm[2:], uint32(frames))
		binary.BigEndian.PutUint16(comm[6:], uint16(bits))
		exp, m := 0, rate
		for m >= 2 {
			m /= 2
			exp++
		}
		binary.BigEndian.PutUint16(comm[8:], uint16(16383+exp))
		binary.BigEndian.PutUint64(comm[10:], uint64(m*(1<<63)))
		if aifcType != "" {
			comm = append(comm, aifcType...)
		}
		body.WriteString("COMM")
		_ = binary.Write(&body, binary.BigEndian, uint32(len(comm)))
		body.Write(comm)
		body.WriteString("SSND")
		_ = binary.Write(&body, binary.BigEndian, uint32(8+data.Len()))
		body.Write(make([]byte, 8))
		body.Write(data.Bytes())
		out.WriteString("FORM")
		_ = binary.Write(&out, binary.BigEndian, uint32(body.Len()))
		out.Write(body.Bytes())
	}
	return out.Bytes()
}

func binOrder(little bool) binary.ByteOrder {
	if little {
		return binary.LittleEndian
	}
	return binary.BigEndian
}

func clamp1(x float64) float64 { return math.Max(-1, math.Min(1, x)) }

// sine is a tone at amplitude amp (1 = full scale) on every channel.
func sine(channels int, rate, freq, amp, seconds float64) []float64 {
	n := int(rate * seconds)
	out := make([]float64, 0, n*channels)
	for i := 0; i < n; i++ {
		v := amp * math.Sin(2*math.Pi*freq*float64(i)/rate)
		for c := 0; c < channels; c++ {
			out = append(out, v)
		}
	}
	return out
}

func silence(channels int, rate, seconds float64) []float64 {
	return make([]float64, int(rate*seconds)*channels)
}

func analyzeBytes(t *testing.T, name string, data []byte) Levels {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	lv, err := AnalyzeFile(context.Background(), p, DefaultTarget)
	if err != nil {
		t.Fatalf("AnalyzeFile(%s): %v", name, err)
	}
	return lv
}

func nearDB(t *testing.T, what string, got *float64, want, tol float64) {
	t.Helper()
	if got == nil {
		t.Errorf("%s = nil, want %.2f", what, want)
		return
	}
	if math.Abs(*got-want) > tol {
		t.Errorf("%s = %.3f, want %.3f ± %.3f", what, *got, want, tol)
	}
}

// A half-scale sine peaks at -6.02 dBFS and averages 3.01 dB lower, in every
// format a stem arrives in.
func TestAnalyzeEveryFormat(t *testing.T) {
	cases := []struct {
		name, container, encoding string
		bits                      int
		aifc                      string
	}{
		{"int16.wav", "WAV", "int", 16, ""},
		{"int24.wav", "WAV", "int", 24, ""},
		{"int32.wav", "WAV", "int", 32, ""},
		{"float32.wav", "WAV", "float", 32, ""},
		{"float64.wav", "WAV", "float", 64, ""},
		{"uint8.wav", "WAV", "int", 8, ""},
		{"int24.aif", "AIFF", "int", 24, ""},
		{"int16.aif", "AIFF", "int", 16, ""},
		{"sowt.aifc", "AIFF", "int", 16, "sowt"},
		{"fl32.aifc", "AIFF", "float", 32, "fl32"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			samples := sine(2, 48000, 1000, 0.5, 2)
			lv := analyzeBytes(t, c.name, encodeAudio(t, c.container, c.encoding, c.bits, 2, 48000, samples, c.aifc))
			tol := 0.02
			if c.bits == 8 {
				tol = 0.2
			}
			nearDB(t, "peak", lv.PeakDBFS, -6.02, tol)
			nearDB(t, "average", lv.AverageDBFS, -9.03, tol)
			if math.Abs(lv.ActiveSeconds-2) > 0.11 || math.Abs(lv.Seconds-2) > 0.001 {
				t.Errorf("active %.2f of %.2f s", lv.ActiveSeconds, lv.Seconds)
			}
			if lv.Silent || lv.Clipping {
				t.Errorf("silent %v clipping %v", lv.Silent, lv.Clipping)
			}
		})
	}
}

// ITU-R BS.1770 reference: a 997 Hz sine at -20 dBFS on both channels of a
// stereo file reads -20.0 LUFS (the -0.691 constant cancels the K-filter's
// gain at 1 kHz); the same tone in a mono file reads -23.0 LUFS. Checked at
// 48 kHz (the standard's own coefficients) and at 44.1 kHz (recomputed).
func TestIntegratedLoudnessMatchesBS1770(t *testing.T) {
	for _, rate := range []float64{48000, 44100, 96000} {
		stereo := analyzeBytes(t, "s.wav", encodeAudio(t, "WAV", "float", 32, 2, rate, sine(2, rate, 997, 0.1, 5), ""))
		nearDB(t, "stereo LUFS", stereo.LUFS, -20.0, 0.05)
		mono := analyzeBytes(t, "m.wav", encodeAudio(t, "WAV", "float", 32, 1, rate, sine(1, rate, 997, 0.1, 5), ""))
		nearDB(t, "mono LUFS", mono.LUFS, -23.0, 0.05)
	}
}

// The K-filter's shape, against ffmpeg's ebur128 on the same tones (48 kHz,
// stereo): 997 Hz -21.1, 10 kHz -17.7, 30 Hz -30.1 LUFS.
func TestKWeightingShapesLoudness(t *testing.T) {
	at := func(freq float64) float64 {
		lv := analyzeBytes(t, "k.wav", encodeAudio(t, "WAV", "float", 32, 2, 48000, sine(2, 48000, freq, 0.1, 3), ""))
		return *lv.LUFS
	}
	if d := at(10000) - at(997); math.Abs(d-3.4) > 0.1 {
		t.Errorf("10 kHz is %.2f LU above 1 kHz, want +3.4", d)
	}
	if d := at(30) - at(997); math.Abs(d+9.0) > 0.15 {
		t.Errorf("30 Hz is %.2f LU from 1 kHz, want -9.0", d)
	}
}

// Gaps between phrases are not averaged in: a tone with long silences
// measures the tone's own level over its own time.
func TestAverageIgnoresSilence(t *testing.T) {
	samples := append(sine(2, 44100, 220, 0.1, 2), silence(2, 44100, 6)...)
	lv := analyzeBytes(t, "sparse.wav", encodeAudio(t, "WAV", "int", 24, 2, 44100, samples, ""))
	nearDB(t, "average", lv.AverageDBFS, -23.01, 0.05)
	if math.Abs(lv.ActiveSeconds-2) > 0.11 {
		t.Errorf("active = %.2f s, want 2", lv.ActiveSeconds)
	}
	// LUFS gates the silence too (absolute gate at -70 LUFS).
	if lv.LUFS == nil || *lv.LUFS < -30 {
		t.Errorf("LUFS = %v", lv.LUFS)
	}
}

func TestSilentExportsAreFlagged(t *testing.T) {
	zero := analyzeBytes(t, "Loops.wav", encodeAudio(t, "WAV", "int", 16, 2, 44100, silence(2, 44100, 3), ""))
	if !zero.Silent || zero.PeakDBFS != nil || zero.AverageDBFS != nil || zero.TrimDB != nil {
		t.Errorf("digital silence = %+v", zero)
	}
	if !strings.Contains(zero.Line, "Loops is silent: an empty export") {
		t.Errorf("line = %q", zero.Line)
	}
	// Hiss at -62 dBFS peak: still an empty export.
	hiss := analyzeBytes(t, "Loops.wav", encodeAudio(t, "WAV", "int", 24, 2, 44100, sine(2, 44100, 5000, math.Pow(10, -62.0/20), 3), ""))
	if !hiss.Silent || hiss.TrimDB != nil {
		t.Errorf("hiss = %+v", hiss)
	}
	nearDB(t, "hiss peak", hiss.PeakDBFS, -62, 0.05)
}

func TestClippingIsCounted(t *testing.T) {
	// A square wave at full scale: runs of full-scale samples.
	n := 44100
	samples := make([]float64, 0, 2*n)
	for i := 0; i < n; i++ {
		v := 1.0
		if (i/50)%2 == 1 {
			v = -1
		}
		samples = append(samples, v, v)
	}
	lv := analyzeBytes(t, "hot.wav", encodeAudio(t, "WAV", "int", 16, 2, 44100, samples, ""))
	if !lv.Clipping || lv.ClippedSamples == 0 {
		t.Errorf("clipping %v, %d samples", lv.Clipping, lv.ClippedSamples)
	}
	if !strings.Contains(lv.Line, "clipped") {
		t.Errorf("line = %q", lv.Line)
	}
	// A float file can hold samples over full scale; they clip on export.
	over := analyzeBytes(t, "over.wav", encodeAudio(t, "WAV", "float", 32, 1, 44100, sine(1, 44100, 100, 1.5, 1), ""))
	if !over.Clipping {
		t.Errorf("float over full scale not flagged: %+v", over)
	}
	nearDB(t, "over peak", over.PeakDBFS, 3.52, 0.02)
	// A clean sine that just touches full scale once per cycle is not clipped.
	clean := analyzeBytes(t, "clean.wav", encodeAudio(t, "WAV", "int", 24, 2, 48000, sine(2, 48000, 1000, 1, 1), ""))
	if clean.Clipping {
		t.Errorf("a full-scale sine is not clipping: %d samples", clean.ClippedSamples)
	}
}

func TestSuggestedTrimReachesTheTarget(t *testing.T) {
	// Average -30 dBFS: +12 dB to reach -18.
	amp := math.Pow(10, -30.0/20) * math.Sqrt2
	lv := analyzeBytes(t, "Keys.wav", encodeAudio(t, "WAV", "int", 24, 2, 48000, sine(2, 48000, 440, amp, 2), ""))
	nearDB(t, "trim", lv.TrimDB, 12, 0.05)
	if lv.TrimLimited {
		t.Error("limited")
	}
	// Average -47 dBFS (Steve's bass export): needs +29, the trim stops at +24.
	amp = math.Pow(10, -47.0/20) * math.Sqrt2
	lv = analyzeBytes(t, "Bass.wav", encodeAudio(t, "WAV", "int", 24, 2, 48000, sine(2, 48000, 60, amp, 2), ""))
	nearDB(t, "trim", lv.TrimDB, 24, 0.001)
	nearDB(t, "needed", lv.NeededDB, 29, 0.05)
	if !lv.TrimLimited || !strings.Contains(lv.Line, "stops at +24 dB") {
		t.Errorf("limited %v, line %q", lv.TrimLimited, lv.Line)
	}
}

func TestSuggestTrimRounding(t *testing.T) {
	cases := []struct{ avg, target, trim float64 }{
		{-23.7, -18, 5.7},
		{-18, -18, 0},
		{-6.04, -18, -12},
		{-60, -18, 24},
		{0, -18, -18},
		{5, -18, -23},
		{10, -18, -24},
	}
	for _, c := range cases {
		trim, _, _ := suggestTrim(c.avg, c.target)
		if math.Abs(trim-c.trim) > 1e-9 {
			t.Errorf("suggestTrim(%v, %v) = %v, want %v", c.avg, c.target, trim, c.trim)
		}
	}
}

// A header that claims more audio than the file holds is measured as far as
// the file goes; nothing panics.
func TestAnalyzeTruncatedFile(t *testing.T) {
	data := encodeAudio(t, "WAV", "int", 16, 2, 44100, sine(2, 44100, 440, 0.5, 1), "")
	data = data[:len(data)/2+1] // an odd cut, mid-frame
	lv := analyzeBytes(t, "cut.wav", data)
	if lv.Seconds > 0.51 || lv.Seconds < 0.49 {
		t.Errorf("seconds = %v", lv.Seconds)
	}
}

func TestAnalyzeRejects(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cases := map[string]string{
		write("notes.txt", []byte("not audio")): "not a WAV or AIFF",
		write("ulaw.aifc", encodeAudio(t, "AIFF", "int", 16, 1, 8000, sine(1, 8000, 440, 0.5, 0.1), "ulaw")): "encoding",
		write("six.wav", encodeAudio(t, "WAV", "int", 16, 6, 48000, make([]float64, 6*4800), "")):            "mono and stereo",
		filepath.Join(dir, "missing.wav"): "not found",
	}
	for p, want := range cases {
		_, err := AnalyzeFile(context.Background(), p, DefaultTarget)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", filepath.Base(p), err, want)
		}
	}
}

// Loudness blocks are bounded: a very long file keeps at most maxBlocks
// 100 ms blocks and says it measured only the start.
func TestAnalyzeBoundsLongFiles(t *testing.T) {
	orig := maxBlocks
	maxBlocks = 20 // two seconds
	t.Cleanup(func() { maxBlocks = orig })
	lv := analyzeBytes(t, "long.wav", encodeAudio(t, "WAV", "int", 16, 1, 8000, sine(1, 8000, 440, 0.5, 5), ""))
	if !lv.Partial || lv.Seconds > 2.01 {
		t.Errorf("partial %v, seconds %v", lv.Partial, lv.Seconds)
	}
}

func TestAnalyzeCancels(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.wav")
	if err := os.WriteFile(p, encodeAudio(t, "WAV", "int", 16, 2, 44100, sine(2, 44100, 440, 0.5, 3), ""), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := AnalyzeFile(ctx, p, DefaultTarget); err == nil {
		t.Error("canceled analysis returned no error")
	}
}

func TestAnalyzeFolderReportsEachStem(t *testing.T) {
	dir := t.TempDir()
	put := func(name string, samples []float64) {
		if err := os.WriteFile(filepath.Join(dir, name), encodeAudio(t, "WAV", "int", 16, 2, 44100, samples, ""), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	put("Song - Drums.wav", sine(2, 44100, 100, 0.5, 1))
	put("Song - Loops.wav", silence(2, 44100, 1))
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := Analyze(context.Background(), []string{dir}, DefaultTarget)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(res.Message, "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "Measured 2 stems") ||
		!strings.HasPrefix(lines[1], "Drums:") || !strings.HasPrefix(lines[2], "Loops is silent") {
		t.Errorf("message = %q", res.Message)
	}
	if !strings.Contains(lines[0], "notes.txt") {
		t.Errorf("skipped file not named: %q", lines[0])
	}
}

func FuzzAnalyzeReader(f *testing.F) {
	f.Add(encodeAudioF(f, "WAV", "int", 16), int64(0))
	f.Add(encodeAudioF(f, "AIFF", "int", 24), int64(3))
	f.Add(encodeAudioF(f, "WAV", "float", 32), int64(7))
	f.Fuzz(func(t *testing.T, data []byte, cut int64) {
		if cut >= 0 && cut < int64(len(data)) {
			data = data[:len(data)-int(cut)]
		}
		a, err := parseAudioHeader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return
		}
		_, _ = measure(context.Background(), bytes.NewReader(data), a)
	})
}

func encodeAudioF(f *testing.F, container, encoding string, bits int) []byte {
	t := &testing.T{}
	return encodeAudio(t, container, encoding, bits, 2, 8000, sine(2, 8000, 440, 0.5, 0.5), "")
}
