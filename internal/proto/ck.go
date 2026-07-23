package proto

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Real StudioLive mixers deliver the initial state snapshot not as a single ZB
// frame but as a sequence of CK ("chunk") frames that must be reassembled into
// the zlib blob before ParseZB can decode it. Each CK payload is:
//
//	[ 4 bytes tag ][ offset u32 LE ][ total u32 LE ][ size u32 LE ][ chunkData ]
//
// The tag observed on a 32R is 00 00 5a 42 ("\x00\x00ZB") — the wrapped payload
// is a ZB. Concatenating every chunk's chunkData in arrival order yields the
// exact byte stream a single ZB frame would have carried, so the assembled
// result is fed straight to ParseZB. A blob is complete when a chunk's
// offset+size equals its total. Mirrors the featherbear reference (CK.ts).

// CKChunk is one parsed CK frame.
type CKChunk struct {
	Offset uint32 // byte offset of this chunk within the full blob
	Total  uint32 // total size of the full blob
	Size   uint32 // size of this chunk's data
	Data   []byte // this chunk's bytes
}

// errShortCK is returned when a CK payload is too small to hold its 16-byte
// header.
var errShortCK = errors.New("proto: CK payload shorter than 16-byte header")

// ParseCK parses a CK frame payload into a CKChunk. It validates that the
// declared chunk size matches the bytes present.
func ParseCK(payload []byte) (CKChunk, error) {
	if len(payload) < 16 {
		return CKChunk{}, errShortCK
	}
	c := CKChunk{
		Offset: binary.LittleEndian.Uint32(payload[4:8]),
		Total:  binary.LittleEndian.Uint32(payload[8:12]),
		Size:   binary.LittleEndian.Uint32(payload[12:16]),
		Data:   payload[16:],
	}
	if int(c.Size) != len(c.Data) {
		return CKChunk{}, fmt.Errorf("proto: CK chunk size %d != data length %d", c.Size, len(c.Data))
	}
	return c, nil
}

// ChunkAssembler reassembles a blob split across CK frames. The zero value is
// ready to use. It is not safe for concurrent use; the client feeds it from a
// single goroutine.
type ChunkAssembler struct {
	buf []byte
}

// Add appends a chunk. When the chunk completes the blob (offset+size == total),
// it returns the fully assembled bytes and resets for the next blob. Otherwise
// it returns nil, false. A chunk at offset 0 starts a fresh blob, so a truncated
// prior blob cannot corrupt the next one.
func (a *ChunkAssembler) Add(c CKChunk) (assembled []byte, complete bool) {
	if c.Offset == 0 {
		a.buf = nil
	}
	a.buf = append(a.buf, c.Data...)
	if c.Offset+c.Size == c.Total {
		out := a.buf
		a.buf = nil
		return out, true
	}
	return nil, false
}
