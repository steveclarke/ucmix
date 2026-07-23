// Package proto is a pure, no-I/O codec for the PreSonus UCNET protocol.
//
// It implements packet framing and the payload codecs (PV/PS/PC/ZB/JM) used to
// talk to StudioLive Series III mixers. Nothing here does network I/O beyond
// reading framed bytes from a supplied *bufio.Reader; there are no goroutines
// and no shared state.
//
// Wire framing:
//
//	[ header 4 ][ length 2 LE ][ code 2 ][ connIdentity 4 ][ payload ]
//
// header = "UC\x00\x01" (55 43 00 01). length = len(code)+len(connID)+len(payload)
// = 6 + len(payload). So length+6 == total packet length. Payload begins at
// byte 12.
package proto

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Header is the fixed 4-byte packet magic: ASCII "UC" followed by 0x00 0x01.
var Header = [4]byte{0x55, 0x43, 0x00, 0x01}

// DefaultConnID is the connIdentity used for frames we originate. UC Surface
// uses a different value (e.g. [0x6b,0x00,0x65,0x00]); connIdentity varies by
// client and must never be asserted on decode — only stored as received.
var DefaultConnID = [4]byte{0x68, 0x00, 0x65, 0x00}

// Message codes (two ASCII chars each).
var (
	CodePV = [2]byte{'P', 'V'}
	CodePS = [2]byte{'P', 'S'}
	CodePC = [2]byte{'P', 'C'}
	CodeZB = [2]byte{'Z', 'B'}
	CodeJM = [2]byte{'J', 'M'}
	CodeKA = [2]byte{'K', 'A'}
	CodeMS = [2]byte{'M', 'S'}
	CodeFD = [2]byte{'F', 'D'}
	CodeCK = [2]byte{'C', 'K'}
)

// Frame is one UCNET packet, minus the fixed header and length field.
type Frame struct {
	Code    [2]byte
	ConnID  [4]byte
	Payload []byte
}

// zeroConnID reports whether c is the zero value (no ConnID set).
func zeroConnID(c [4]byte) bool { return c == [4]byte{} }

// Encode returns the full wire bytes for f, including the header and length.
// A zero-value ConnID is replaced with DefaultConnID.
func Encode(f Frame) []byte {
	conn := f.ConnID
	if zeroConnID(conn) {
		conn = DefaultConnID
	}

	// length = code(2) + connID(4) + payload
	length := 6 + len(f.Payload)

	out := make([]byte, 0, 4+2+length)
	out = append(out, Header[:]...)
	var lenBuf [2]byte
	binary.LittleEndian.PutUint16(lenBuf[:], uint16(length))
	out = append(out, lenBuf[:]...)
	out = append(out, f.Code[:]...)
	out = append(out, conn[:]...)
	out = append(out, f.Payload...)
	return out
}

// ErrBadMagic is returned by ReadFrame when the 4-byte header is not "UC\x00\x01".
var ErrBadMagic = errors.New("proto: bad packet header magic")

// ReadFrame blocking-reads exactly one frame from r. It returns a clear error
// (never a panic) on a short header, bad magic, an under-length length field, or
// a truncated payload.
func ReadFrame(r *bufio.Reader) (Frame, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return Frame{}, fmt.Errorf("proto: reading header: %w", err)
	}
	if hdr != Header {
		return Frame{}, fmt.Errorf("%w: got % x", ErrBadMagic, hdr[:])
	}

	var lenBuf [2]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return Frame{}, fmt.Errorf("proto: reading length: %w", err)
	}
	length := int(binary.LittleEndian.Uint16(lenBuf[:]))
	if length < 6 {
		return Frame{}, fmt.Errorf("proto: length %d shorter than code+connID (6)", length)
	}

	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return Frame{}, fmt.Errorf("proto: reading frame body (%d bytes): %w", length, err)
	}

	var f Frame
	copy(f.Code[:], body[0:2])
	copy(f.ConnID[:], body[2:6])
	// Copy the payload into its own slice so it does not alias the read buffer.
	f.Payload = append([]byte(nil), body[6:]...)
	return f, nil
}
