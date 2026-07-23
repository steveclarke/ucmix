package proto

import (
	"errors"
	"fmt"
	"math"
)

// sep is the 3-byte separator written between a key and its value: a single
// NUL key-terminator followed by a 2-byte "partA". On writes partA is always
// 0x00 0x00; incoming filter-group deltas may carry 0x00 0x01, so decoders must
// skip exactly two bytes after the key terminator rather than assume the value.
var sep = []byte{0x00, 0x00, 0x00}

// splitKeyValue finds the first 0x00 (key terminator) and returns the key, the
// 2-byte partA that follows it, and the value bytes after partA. It mirrors the
// featherbear reference decoders (key\x00 + partA(2) + value).
func splitKeyValue(payload []byte) (key string, partA []byte, value []byte, err error) {
	idx := indexZero(payload)
	if idx < 0 {
		return "", nil, nil, errors.New("proto: missing key terminator (0x00)")
	}
	// Need the terminator plus 2 partA bytes.
	if len(payload) < idx+3 {
		return "", nil, nil, fmt.Errorf("proto: truncated payload after key %q", payload[:idx])
	}
	key = string(payload[:idx])
	partA = payload[idx+1 : idx+3]
	value = payload[idx+3:]
	return key, partA, value, nil
}

func indexZero(b []byte) int {
	for i, c := range b {
		if c == 0x00 {
			return i
		}
	}
	return -1
}

// MarshalPV encodes a parameter-value (float) write: key\x00\x00\x00 followed by
// a 4-byte little-endian float32. Booleans are encoded by the caller as 1.0/0.0.
func MarshalPV(key string, val float32) []byte {
	out := make([]byte, 0, len(key)+3+4)
	out = append(out, key...)
	out = append(out, sep...)
	bits := math.Float32bits(val)
	out = append(out, byte(bits), byte(bits>>8), byte(bits>>16), byte(bits>>24))
	return out
}

// UnmarshalPV decodes a PV payload into its key and float32 value.
func UnmarshalPV(payload []byte) (key string, val float32, err error) {
	key, _, value, err := splitKeyValue(payload)
	if err != nil {
		return "", 0, err
	}
	if len(value) < 4 {
		return "", 0, fmt.Errorf("proto: PV value for %q is %d bytes, need 4", key, len(value))
	}
	bits := uint32(value[0]) | uint32(value[1])<<8 | uint32(value[2])<<16 | uint32(value[3])<<24
	return key, math.Float32frombits(bits), nil
}

// MarshalPS encodes a parameter-string (name/icon) write: key\x00\x00\x00 followed
// by the UTF-8 value and a trailing 0x00.
func MarshalPS(key, val string) []byte {
	out := make([]byte, 0, len(key)+3+len(val)+1)
	out = append(out, key...)
	out = append(out, sep...)
	out = append(out, val...)
	out = append(out, 0x00)
	return out
}

// UnmarshalPS decodes a PS payload into its key and string value, stripping the
// single trailing 0x00 that MarshalPS appends. Decode is asymmetric with encode
// here: encode adds the trailing NUL, decode removes it.
func UnmarshalPS(payload []byte) (key string, val string, err error) {
	key, _, value, err := splitKeyValue(payload)
	if err != nil {
		return "", "", err
	}
	if len(value) == 0 || value[len(value)-1] != 0x00 {
		return "", "", fmt.Errorf("proto: PS value for %q missing trailing NUL", key)
	}
	return key, string(value[:len(value)-1]), nil
}

// MarshalPC encodes a parameter-chars (color/raw) write: key\x00\x00\x00 followed
// by raw bytes verbatim (color = hex bytes plus an optional alpha byte).
func MarshalPC(key string, raw []byte) []byte {
	out := make([]byte, 0, len(key)+3+len(raw))
	out = append(out, key...)
	out = append(out, sep...)
	out = append(out, raw...)
	return out
}

// UnmarshalPC decodes a PC payload into its key and raw value bytes. Unlike PS
// there is no trailing NUL to strip; the value is returned verbatim.
func UnmarshalPC(payload []byte) (key string, raw []byte, err error) {
	key, _, value, err := splitKeyValue(payload)
	if err != nil {
		return "", nil, err
	}
	return key, append([]byte(nil), value...), nil
}
