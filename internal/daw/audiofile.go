package daw

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
)

// AudioFile is what ucdaw reads from an audio file's header before importing
// it: enough to pick a mono or stereo track and to describe the file.
type AudioFile struct {
	Path       string  `json:"path"`
	Container  string  `json:"container"` // WAV or AIFF
	Channels   int     `json:"channels"`
	SampleRate float64 `json:"sampleRate"`
	Bits       int     `json:"bits"`
	Frames     uint64  `json:"frames"`

	// Where the samples are and how they are stored, for analysis. Not
	// part of the JSON: a musician never needs them.
	dataOffset int64  // first byte of the first frame
	dataBytes  int64  // bytes of sample data the file really holds
	blockAlign int    // bytes per frame
	encoding   string // "int", "uint8" (8-bit WAV), "float", or "" (unknown)
	bigEndian  bool
}

// Format is "mono" or "stereo", the audio track the file belongs on.
func (a AudioFile) Format() string {
	if a.Channels == 1 {
		return "mono"
	}
	return "stereo"
}

// errNotAudio is a file ucdaw does not import: not WAV or AIFF.
var errNotAudio = errors.New("not a WAV or AIFF file")

// audioExtensions are the extensions ucdaw looks at in a folder.
var audioExtensions = map[string]bool{".wav": true, ".wave": true, ".bwf": true, ".aif": true, ".aiff": true, ".aifc": true}

// otherAudio are audio files ucdaw does not read yet.
var otherAudio = map[string]bool{".mp3": true, ".flac": true, ".m4a": true, ".aac": true, ".ogg": true, ".caf": true, ".wma": true, ".opus": true}

// Header walking is bounded: at most maxChunks chunks, and only chunk headers
// and the small format chunks are read, never audio data.
const (
	maxChunks    = 256
	maxFmtChunk  = 4096
	maxAudioFile = 1 << 40 // 1 TB: anything larger is not a real stem
)

// ReadAudioFile reads a WAV (RIFF, RF64, BWF, WAVE_FORMAT_EXTENSIBLE) or AIFF
// (AIFF, AIFC) header. It never reads the audio itself and never trusts a
// size it has not bounded.
func ReadAudioFile(path string) (AudioFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return AudioFile{}, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return AudioFile{}, err
	}
	if !info.Mode().IsRegular() {
		return AudioFile{}, errNotAudio
	}
	a, err := parseAudioHeader(f, info.Size())
	if err != nil {
		return AudioFile{}, err
	}
	a.Path = path
	return a, nil
}

func parseAudioHeader(r io.ReadSeeker, size int64) (AudioFile, error) {
	if size < 12 || size > maxAudioFile {
		return AudioFile{}, errNotAudio
	}
	var head [12]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return AudioFile{}, errNotAudio
	}
	switch {
	case (string(head[:4]) == "RIFF" || string(head[:4]) == "RF64") && string(head[8:12]) == "WAVE":
		return parseWAV(r, size, string(head[:4]) == "RF64")
	case string(head[:4]) == "FORM" && (string(head[8:12]) == "AIFF" || string(head[8:12]) == "AIFC"):
		return parseAIFF(r, size)
	}
	return AudioFile{}, errNotAudio
}

// walkChunks walks chunk headers from just after the 12-byte file header,
// calling fn with each chunk's id, its declared size, the offset of its
// body, and a reader limited to the chunk's first maxFmtChunk bytes. Chunks are padded to an even length in
// both formats. The walk never trusts a size: at most maxChunks headers are
// read, every step moves forward by at least 8 bytes, and a chunk that claims
// to run past the end of the file is malformed, except the audio itself
// (data, SSND), which a file still being written may cut short.
func walkChunks(r io.ReadSeeker, size int64, order binary.ByteOrder, fn func(id string, n uint32, at int64, body io.Reader) (stop bool, err error)) error {
	pos := int64(12)
	for i := 0; i < maxChunks && pos+8 <= size; i++ {
		if _, err := r.Seek(pos, io.SeekStart); err != nil {
			return err
		}
		var h [8]byte
		if _, err := io.ReadFull(r, h[:]); err != nil {
			return err
		}
		id, n := string(h[:4]), order.Uint32(h[4:])
		end := pos + 8 + int64(n) + int64(n&1)
		if end > size+1 && id != "data" && id != "SSND" {
			return errNotAudio
		}
		stop, err := fn(id, n, pos+8, io.LimitReader(r, min(int64(n), maxFmtChunk)))
		if err != nil || stop {
			return err
		}
		if end <= pos {
			return errNotAudio
		}
		pos = end
	}
	return errNotAudio
}

func parseWAV(r io.ReadSeeker, size int64, rf64 bool) (AudioFile, error) {
	a := AudioFile{Container: "WAV"}
	var blockAlign uint16
	var formatTag uint16
	var dataSize uint64
	var ds64Data uint64
	haveFmt, haveData := false, false
	err := walkChunks(r, size, binary.LittleEndian, func(id string, n uint32, at int64, body io.Reader) (bool, error) {
		switch id {
		case "ds64":
			if n < 16 || n > maxFmtChunk {
				return false, errNotAudio
			}
			var b [16]byte
			if _, err := io.ReadFull(body, b[:]); err != nil {
				return false, err
			}
			ds64Data = binary.LittleEndian.Uint64(b[8:16])
		case "fmt ":
			if n < 16 || n > maxFmtChunk {
				return false, errNotAudio
			}
			b := make([]byte, min(int(n), 26))
			if _, err := io.ReadFull(body, b); err != nil {
				return false, err
			}
			formatTag = binary.LittleEndian.Uint16(b[0:2])
			a.Channels = int(binary.LittleEndian.Uint16(b[2:4]))
			a.SampleRate = float64(binary.LittleEndian.Uint32(b[4:8]))
			blockAlign = binary.LittleEndian.Uint16(b[12:14])
			a.Bits = int(binary.LittleEndian.Uint16(b[14:16]))
			// WAVE_FORMAT_EXTENSIBLE: the real format is the first two
			// bytes of the SubFormat GUID, 24 bytes into the chunk.
			if formatTag == 0xFFFE && len(b) >= 26 {
				formatTag = binary.LittleEndian.Uint16(b[24:26])
			}
			haveFmt = true
		case "data":
			dataSize = uint64(n)
			if rf64 && n == math.MaxUint32 {
				dataSize = ds64Data
			}
			a.dataOffset = at
			haveData = true
		}
		return haveFmt && haveData, nil
	})
	if err != nil || !haveFmt || !haveData {
		return AudioFile{}, errNotAudio
	}
	if blockAlign == 0 {
		return AudioFile{}, errNotAudio
	}
	a.Frames = dataSize / uint64(blockAlign)
	a.blockAlign = int(blockAlign)
	a.dataBytes = dataBytes(a.dataOffset, dataSize, size)
	switch {
	case formatTag == 1 && a.Bits <= 8:
		a.encoding = "uint8"
	case formatTag == 1:
		a.encoding = "int"
	case formatTag == 3:
		a.encoding = "float"
	}
	return a, checkAudio(a)
}

// dataBytes is how much sample data a file really holds: what its header
// declares, cut to the end of the file (a file still being written, or cut
// short, declares more than it has).
func dataBytes(offset int64, declared uint64, size int64) int64 {
	avail := size - offset
	if avail < 0 {
		return 0
	}
	if declared > uint64(avail) {
		return avail
	}
	return int64(declared)
}

func parseAIFF(r io.ReadSeeker, size int64) (AudioFile, error) {
	a := AudioFile{Container: "AIFF", bigEndian: true, encoding: "int"}
	haveComm, haveData := false, false
	var ssndSize uint64
	_ = walkChunks(r, size, binary.BigEndian, func(id string, n uint32, at int64, body io.Reader) (bool, error) {
		switch id {
		case "COMM":
			if n < 18 || n > maxFmtChunk {
				return false, errNotAudio
			}
			b := make([]byte, min(int(n), 22))
			if _, err := io.ReadFull(body, b); err != nil {
				return false, err
			}
			a.Channels = int(binary.BigEndian.Uint16(b[0:2]))
			a.Frames = uint64(binary.BigEndian.Uint32(b[2:6]))
			a.Bits = int(binary.BigEndian.Uint16(b[6:8]))
			a.SampleRate = extended80(b[8:18])
			// AIFC names its encoding after the sample rate.
			if len(b) >= 22 {
				switch string(b[18:22]) {
				case "NONE", "twos":
				case "sowt":
					a.bigEndian = false
				case "fl32", "FL32", "fl64", "FL64":
					a.encoding = "float"
				default:
					a.encoding = ""
				}
			}
			haveComm = true
		case "SSND":
			// SSND starts with an offset to the first frame and a block size.
			var b [8]byte
			if n < 8 {
				return false, errNotAudio
			}
			if _, err := io.ReadFull(body, b[:]); err != nil {
				return false, err
			}
			skip := int64(binary.BigEndian.Uint32(b[0:4]))
			if skip > int64(n)-8 {
				return false, errNotAudio
			}
			a.dataOffset = at + 8 + skip
			ssndSize = uint64(int64(n) - 8 - skip)
			haveData = true
		}
		return haveComm && haveData, nil
	})
	// The walk ends in an error when it runs out of chunks before finding
	// both; an AIFF whose COMM was read still describes itself (only
	// analysis needs the sound data).
	if !haveComm {
		return AudioFile{}, errNotAudio
	}
	bytesPer := (a.Bits + 7) / 8
	a.blockAlign = a.Channels * bytesPer
	if haveData {
		a.dataBytes = dataBytes(a.dataOffset, ssndSize, size)
	}
	return a, checkAudio(a)
}

// extended80 decodes an IEEE 754 80-bit extended float (AIFF's sample rate).
func extended80(b []byte) float64 {
	exp := int(binary.BigEndian.Uint16(b[0:2]) & 0x7fff)
	mant := binary.BigEndian.Uint64(b[2:10])
	if exp == 0 && mant == 0 {
		return 0
	}
	if exp == 0x7fff {
		return math.NaN()
	}
	v := math.Ldexp(float64(mant), exp-16383-63)
	if b[0]&0x80 != 0 {
		v = -v
	}
	return v
}

func checkAudio(a AudioFile) error {
	if a.Channels < 1 || a.Channels > 64 || math.IsNaN(a.SampleRate) || a.SampleRate < 1000 || a.SampleRate > 1_000_000 || a.Bits < 1 || a.Bits > 64 {
		return errNotAudio
	}
	return nil
}

// MaxImportFiles bounds one import.
const MaxImportFiles = 64

// skipped is a file ucdaw did not import, and why.
type skipped struct {
	File   string `json:"file"`
	Reason string `json:"reason"`
}

// collectAudio expands folders (one level, in name order) and reads every
// file's header. Non-audio files are skipped with a reason; hidden files in
// a folder are passed over silently.
func collectAudio(paths []string) ([]AudioFile, []skipped, error) {
	var files []AudioFile
	var skips []skipped
	add := func(p string, fromFolder bool) {
		a, err := ReadAudioFile(p)
		switch {
		case err == nil && a.Channels > 2:
			skips = append(skips, skipped{filepath.Base(p), fmt.Sprintf("%d channels; ucdaw imports mono and stereo files", a.Channels)})
		case err == nil:
			files = append(files, a)
		case errors.Is(err, os.ErrNotExist):
			skips = append(skips, skipped{filepath.Base(p), "not found"})
		case otherAudio[strings.ToLower(filepath.Ext(p))]:
			skips = append(skips, skipped{filepath.Base(p), "ucdaw imports WAV and AIFF files; convert it first"})
		case fromFolder && !audioExtensions[strings.ToLower(filepath.Ext(p))]:
			skips = append(skips, skipped{filepath.Base(p), "not audio"})
		default:
			skips = append(skips, skipped{filepath.Base(p), "not a WAV or AIFF file"})
		}
	}
	for _, p := range paths {
		if !filepath.IsAbs(p) {
			return nil, nil, Problem{Message: fmt.Sprintf("%q is not a full path", p)}
		}
		p = filepath.Clean(p)
		info, err := os.Stat(p)
		if err != nil {
			skips = append(skips, skipped{filepath.Base(p), "not found"})
			continue
		}
		if !info.IsDir() {
			add(p, false)
			continue
		}
		entries, err := os.ReadDir(p) // sorted by name
		if err != nil {
			skips = append(skips, skipped{filepath.Base(p), "folder could not be read"})
			continue
		}
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".") || e.IsDir() {
				continue
			}
			add(filepath.Join(p, e.Name()), true)
		}
	}
	if len(files) > MaxImportFiles {
		return nil, skips, Problem{Message: fmt.Sprintf("that is %d audio files; ucdaw imports at most %d at a time", len(files), MaxImportFiles)}
	}
	return files, skips, nil
}
