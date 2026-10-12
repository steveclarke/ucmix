package daw

import (
	"encoding/binary"
	"io"
	"sync"
	"time"

	"github.com/steveclarke/ucmix/internal/proto"
)

// Recorder writes inbound frames to w in ucdaw's capture format, one record
// per frame:
//
//	[ms since start u32 LE][length u32 LE][code 2][connID 4][payload]
//
// where length covers code, connID and payload. Captures hold license and
// song data: keep them out of public repositories.
type Recorder struct {
	mu     sync.Mutex
	w      io.Writer
	start  time.Time
	Frames int
	err    error
}

// NewRecorder starts a capture on w.
func NewRecorder(w io.Writer) *Recorder {
	return &Recorder{w: w, start: time.Now()}
}

// Tap records one frame; pass it to DialTap.
func (r *Recorder) Tap(f proto.Frame) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return
	}
	var head [8]byte
	binary.LittleEndian.PutUint32(head[:4], uint32(time.Since(r.start).Milliseconds()))
	binary.LittleEndian.PutUint32(head[4:], uint32(6+len(f.Payload)))
	record := append(head[:], f.Code[:]...)
	record = append(record, f.ConnID[:]...)
	record = append(record, f.Payload...)
	if _, err := r.w.Write(record); err != nil {
		r.err = err
		return
	}
	r.Frames++
}

// Err reports the first write error.
func (r *Recorder) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

// ReadCapture decodes a capture back into frames.
func ReadCapture(rd io.Reader) ([]proto.Frame, error) {
	var out []proto.Frame
	for {
		var head [8]byte
		if _, err := io.ReadFull(rd, head[:]); err != nil {
			if err == io.EOF {
				return out, nil
			}
			return out, err
		}
		n := binary.LittleEndian.Uint32(head[4:])
		if n < 6 || n > 1<<20 {
			return out, io.ErrUnexpectedEOF
		}
		body := make([]byte, n)
		if _, err := io.ReadFull(rd, body); err != nil {
			return out, err
		}
		var f proto.Frame
		copy(f.Code[:], body[:2])
		copy(f.ConnID[:], body[2:6])
		f.Payload = body[6:]
		out = append(out, f)
	}
}
