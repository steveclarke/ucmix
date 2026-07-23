package proto

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
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
	defer func() { _ = zr.Close() }()

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

// BuildZB is the inverse of ParseZB: it takes a flat map of "/"-delimited paths
// to leaf values, unflattens it into a nested UBJSON object, encodes that with
// the marker subset the decoder implements, and zlib-compresses the result.
// ParseZB(BuildZB(m)) reproduces m for every value kind the decoder emits.
//
// Supported leaf kinds and the markers they use (each decodes back to the same
// Go type ParseZB would have produced):
//
//	string           -> S   -> string
//	float32/float64  -> d   -> float64 (values must be float32-representable)
//	int in [-128,127]-> i   -> int
//	int in [128,255] -> U   -> int
//	int (int32 range)-> l   -> int
//	int64            -> L   -> int64
//	[]any            -> [ … ] with each element encoded by the same rules
//
// A leaf of any other kind (e.g. bool, or an int outside int32 range) returns an
// error rather than emitting a marker the decoder cannot read.
func BuildZB(paths map[string]any) ([]byte, error) {
	tree := unflatten(paths)
	var doc []byte
	doc, err := encodeObject(doc, tree)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	if _, err := zw.Write(doc); err != nil {
		return nil, fmt.Errorf("proto: ZB zlib write: %w", err)
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("proto: ZB zlib close: %w", err)
	}
	return buf.Bytes(), nil
}

// unflatten turns a flat "/"-path map into the nested map[string]any shape the
// UBJSON encoder walks. A key with no "/" is a top-level leaf.
func unflatten(paths map[string]any) map[string]any {
	root := make(map[string]any)
	for path, v := range paths {
		segs := strings.Split(path, "/")
		node := root
		for i := 0; i < len(segs)-1; i++ {
			child, ok := node[segs[i]].(map[string]any)
			if !ok {
				child = make(map[string]any)
				node[segs[i]] = child
			}
			node = child
		}
		node[segs[len(segs)-1]] = v
	}
	return root
}

// encodeObject appends a UBJSON object ({ … }) for m to dst.
func encodeObject(dst []byte, m map[string]any) ([]byte, error) {
	dst = append(dst, ubObjBegin)
	for k, v := range m {
		dst = appendKey(dst, k)
		var err error
		dst, err = encodeValue(dst, v)
		if err != nil {
			return nil, err
		}
	}
	dst = append(dst, ubObjEnd)
	return dst, nil
}

// appendKey writes a UBJSON key: the 'i' marker, a 1-byte length, then the key
// bytes (matching the decoder's readCountedBytes on parseObjectBody).
func appendKey(dst []byte, k string) []byte {
	dst = append(dst, ubKeyMarker, byte(len(k)))
	return append(dst, k...)
}

// encodeValue appends the type marker and value for v to dst.
func encodeValue(dst []byte, v any) ([]byte, error) {
	switch t := v.(type) {
	case map[string]any:
		return encodeObject(dst, t)
	case []any:
		dst = append(dst, ubArrBegin)
		for _, e := range t {
			var err error
			dst, err = encodeValue(dst, e)
			if err != nil {
				return nil, err
			}
		}
		return append(dst, ubArrEnd), nil
	case string:
		dst = append(dst, ubStr, ubKeyMarker, byte(len(t)))
		return append(dst, t...), nil
	case float32:
		return appendFloat32(dst, float32(t)), nil
	case float64:
		return appendFloat32(dst, float32(t)), nil
	case int64:
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(t))
		return append(append(dst, ubInt64), b[:]...), nil
	case int:
		switch {
		case t >= -128 && t <= 127:
			return append(dst, ubInt8, byte(int8(t))), nil
		case t >= 128 && t <= 255:
			return append(dst, ubUint8, byte(t)), nil
		case t >= math.MinInt32 && t <= math.MaxInt32:
			var b [4]byte
			binary.BigEndian.PutUint32(b[:], uint32(int32(t)))
			return append(append(dst, ubInt32), b[:]...), nil
		default:
			return nil, fmt.Errorf("proto: int %d out of range for UBJSON encode; use int64", t)
		}
	default:
		return nil, fmt.Errorf("proto: cannot encode value of type %T for ZB", v)
	}
}

// appendFloat32 writes a UBJSON float32 ('d' + 4 big-endian bytes).
func appendFloat32(dst []byte, f float32) []byte {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], math.Float32bits(f))
	return append(append(dst, ubFloat32), b[:]...)
}

// flatten walks a nested UBJSON object, writing scalar/array leaves into out
// keyed by their "/"-joined path.
//
// Real boards wrap the tree in structural keys: a node's child controls live
// under a "children" map and its leaf values under a "values" map, neither of
// which is part of the control's path — "children/line/children/ch1/values/mute"
// is the control "line/ch1/mute". So "children" and "values" are descended
// transparently (their own name is not added to the path), and the "strings"
// (enum options) and "ranges" (min/max/def/units) metadata maps are skipped.
// Plain nested maps (as the fake board emits) still flatten by their key, so
// both real and synthetic snapshots produce the same namespace.
func flatten(prefix string, node map[string]any, out map[string]any) {
	for k, v := range node {
		switch k {
		case "children", "values":
			if child, ok := v.(map[string]any); ok {
				flatten(prefix, child, out) // transparent wrapper: don't add k to the path
				continue
			}
		case "strings", "ranges":
			continue // metadata, not control state
		}
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
