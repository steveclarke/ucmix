package proto

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
)

// TODO(phase1-hw): full-board ZB golden — capture a raw ZB blob from a live 32R,
// assert the flattened key count is roughly 19,800. Until then ZB decoding is
// covered only by hand-constructed UBJSON fixtures (see zb_test.go).

// ParseZB zlib-inflates a ZB payload and decodes the resulting UBJSON document
// into a flat map of "/"-delimited paths to leaf values. Nested objects become
// path segments; arrays and scalars are stored as leaf values verbatim.
func ParseZB(payload []byte) (map[string]any, error) {
	zr, err := zlib.NewReader(bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("proto: ZB zlib header: %w", err)
	}
	defer zr.Close()

	raw, err := io.ReadAll(zr)
	if err != nil {
		return nil, fmt.Errorf("proto: ZB zlib inflate: %w", err)
	}

	tree, err := decodeUBJSON(raw)
	if err != nil {
		return nil, err
	}

	flat := make(map[string]any)
	flatten("", tree, flat)
	return flat, nil
}

// flatten walks a nested UBJSON object, writing scalar/array leaves into out
// keyed by their "/"-joined path.
func flatten(prefix string, node map[string]any, out map[string]any) {
	for k, v := range node {
		path := k
		if prefix != "" {
			path = prefix + "/" + k
		}
		if child, ok := v.(map[string]any); ok {
			flatten(path, child, out)
			continue
		}
		out[path] = v
	}
}

// UBJSON type markers (the subset the board emits; mirrors the featherbear
// reference decoder ubjson.ts).
const (
	ubObjBegin  = 0x7b // {
	ubObjEnd    = 0x7d // }
	ubArrBegin  = 0x5b // [
	ubArrEnd    = 0x5d // ]
	ubKeyMarker = 0x69 // i — also the int8 value marker
	ubStr       = 0x53 // S
	ubFloat32   = 0x64 // d
	ubInt8      = 0x69 // i
	ubUint8     = 0x55 // U
	ubInt32     = 0x6c // l
	ubInt64     = 0x4c // L
)

var errTruncatedUBJSON = errors.New("proto: truncated UBJSON")

// decodeUBJSON parses a UBJSON document whose root is an object into a nested
// map[string]any. It implements the same marker subset as the reference decoder
// and returns a clear error (never a panic) on truncated or malformed input.
func decodeUBJSON(buf []byte) (map[string]any, error) {
	if len(buf) == 0 || buf[0] != ubObjBegin {
		return nil, errors.New("proto: UBJSON root is not an object")
	}
	idx := 1
	m, err := parseObjectBody(buf, &idx)
	if err != nil {
		return nil, err
	}
	return m, nil
}

// parseObjectBody parses key/value pairs until a matching '}'. The opening '{'
// must already be consumed.
func parseObjectBody(buf []byte, idx *int) (map[string]any, error) {
	m := make(map[string]any)
	for {
		if *idx >= len(buf) {
			return nil, fmt.Errorf("%w: object not closed", errTruncatedUBJSON)
		}
		c := buf[*idx]
		*idx++
		if c == ubObjEnd {
			return m, nil
		}
		if c != ubKeyMarker {
			return nil, fmt.Errorf("proto: expected key marker 'i', got 0x%02x at %d", c, *idx-1)
		}
		key, err := readCountedBytes(buf, idx)
		if err != nil {
			return nil, err
		}
		v, err := parseValue(buf, idx)
		if err != nil {
			return nil, err
		}
		m[string(key)] = v
	}
}

// parseArrayBody parses values until a matching ']'. The opening '[' must
// already be consumed.
func parseArrayBody(buf []byte, idx *int) ([]any, error) {
	a := []any{}
	for {
		if *idx >= len(buf) {
			return nil, fmt.Errorf("%w: array not closed", errTruncatedUBJSON)
		}
		if buf[*idx] == ubArrEnd {
			*idx++
			return a, nil
		}
		v, err := parseValue(buf, idx)
		if err != nil {
			return nil, err
		}
		a = append(a, v)
	}
}

// parseValue reads a type marker and the value that follows it.
func parseValue(buf []byte, idx *int) (any, error) {
	if *idx >= len(buf) {
		return nil, fmt.Errorf("%w: expected value", errTruncatedUBJSON)
	}
	t := buf[*idx]
	*idx++
	switch t {
	case ubObjBegin:
		return parseObjectBody(buf, idx)
	case ubArrBegin:
		return parseArrayBody(buf, idx)
	case ubStr:
		if *idx >= len(buf) || buf[*idx] != ubKeyMarker {
			return nil, errors.New("proto: UBJSON string length must use 'i' marker")
		}
		*idx++
		b, err := readCountedBytes(buf, idx)
		if err != nil {
			return nil, err
		}
		return string(b), nil
	case ubFloat32:
		b, err := readN(buf, idx, 4)
		if err != nil {
			return nil, err
		}
		return float64(math.Float32frombits(binary.BigEndian.Uint32(b))), nil
	case ubInt8:
		b, err := readN(buf, idx, 1)
		if err != nil {
			return nil, err
		}
		return int(int8(b[0])), nil
	case ubUint8:
		b, err := readN(buf, idx, 1)
		if err != nil {
			return nil, err
		}
		return int(b[0]), nil
	case ubInt32:
		b, err := readN(buf, idx, 4)
		if err != nil {
			return nil, err
		}
		return int(int32(binary.BigEndian.Uint32(b))), nil
	case ubInt64:
		b, err := readN(buf, idx, 8)
		if err != nil {
			return nil, err
		}
		return int64(binary.BigEndian.Uint64(b)), nil
	default:
		return nil, fmt.Errorf("proto: unknown UBJSON type 0x%02x at %d", t, *idx-1)
	}
}

// readCountedBytes reads a 1-byte length prefix and that many bytes.
func readCountedBytes(buf []byte, idx *int) ([]byte, error) {
	if *idx >= len(buf) {
		return nil, fmt.Errorf("%w: expected length byte", errTruncatedUBJSON)
	}
	n := int(buf[*idx])
	*idx++
	return readN(buf, idx, n)
}

// readN reads n bytes, advancing idx, or returns a truncation error.
func readN(buf []byte, idx *int, n int) ([]byte, error) {
	if *idx+n > len(buf) {
		return nil, fmt.Errorf("%w: need %d bytes at %d, have %d", errTruncatedUBJSON, n, *idx, len(buf)-*idx)
	}
	b := buf[*idx : *idx+n]
	*idx += n
	return b, nil
}
