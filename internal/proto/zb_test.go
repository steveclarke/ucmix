package proto

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"math"
	"reflect"
	"testing"
)

// ub builds UBJSON building blocks for hand-constructed fixtures.
func ubKey(k string) []byte { return append([]byte{ubKeyMarker, byte(len(k))}, k...) }
func ubString(s string) []byte {
	return append([]byte{ubStr, ubKeyMarker, byte(len(s))}, s...)
}
func ubFloat(f float32) []byte {
	b := make([]byte, 5)
	b[0] = ubFloat32
	binary.BigEndian.PutUint32(b[1:], math.Float32bits(f))
	return b
}
func ubI8(v int8) []byte  { return []byte{ubInt8, byte(v)} }
func ubU8(v uint8) []byte { return []byte{ubUint8, v} }
func ubL32(v int32) []byte {
	b := make([]byte, 5)
	b[0] = ubInt32
	binary.BigEndian.PutUint32(b[1:], uint32(v))
	return b
}

func TestDecodeUBJSONNested(t *testing.T) {
	// { "global": { "mute": 0.0, "name": "Main" }, "count": 5 }
	var doc []byte
	doc = append(doc, ubObjBegin)
	doc = append(doc, ubKey("global")...)
	doc = append(doc, ubObjBegin)
	doc = append(doc, ubKey("mute")...)
	doc = append(doc, ubFloat(0.0)...)
	doc = append(doc, ubKey("name")...)
	doc = append(doc, ubString("Main")...)
	doc = append(doc, ubObjEnd)
	doc = append(doc, ubKey("count")...)
	doc = append(doc, ubI8(5)...)
	doc = append(doc, ubObjEnd)

	m, err := decodeUBJSON(doc)
	if err != nil {
		t.Fatal(err)
	}
	flat := make(map[string]any)
	flatten("", m, flat)

	want := map[string]any{
		"global/mute": float64(0.0),
		"global/name": "Main",
		"count":       5,
	}
	if !reflect.DeepEqual(flat, want) {
		t.Fatalf("flatten =\n%#v\nwant\n%#v", flat, want)
	}
}

func TestDecodeUBJSONScalarTypes(t *testing.T) {
	// { "f": 0.5, "i": -3, "u": 200, "l": 70000 }
	var doc []byte
	doc = append(doc, ubObjBegin)
	doc = append(doc, ubKey("f")...)
	doc = append(doc, ubFloat(0.5)...)
	doc = append(doc, ubKey("i")...)
	doc = append(doc, ubI8(-3)...)
	doc = append(doc, ubKey("u")...)
	doc = append(doc, ubU8(200)...)
	doc = append(doc, ubKey("l")...)
	doc = append(doc, ubL32(70000)...)
	doc = append(doc, ubObjEnd)

	m, err := decodeUBJSON(doc)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"f": float64(0.5), "i": -3, "u": 200, "l": 70000}
	if !reflect.DeepEqual(m, want) {
		t.Fatalf("decode =\n%#v\nwant\n%#v", m, want)
	}
}

func TestDecodeUBJSONArrayLeaf(t *testing.T) {
	// { "arr": [ 1, 2 ] } — an array is stored as a leaf value.
	var doc []byte
	doc = append(doc, ubObjBegin)
	doc = append(doc, ubKey("arr")...)
	doc = append(doc, ubArrBegin)
	doc = append(doc, ubI8(1)...)
	doc = append(doc, ubI8(2)...)
	doc = append(doc, ubArrEnd)
	doc = append(doc, ubObjEnd)

	m, err := decodeUBJSON(doc)
	if err != nil {
		t.Fatal(err)
	}
	flat := make(map[string]any)
	flatten("", m, flat)
	arr, ok := flat["arr"].([]any)
	if !ok {
		t.Fatalf("arr is %T, want []any", flat["arr"])
	}
	if !reflect.DeepEqual(arr, []any{1, 2}) {
		t.Fatalf("arr = %#v, want [1 2]", arr)
	}
}

func zlibCompress(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	if _, err := w.Write(b); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestParseZBRoundTrip(t *testing.T) {
	// { "line": { "ch1": { "mute": 1.0 } } }
	var doc []byte
	doc = append(doc, ubObjBegin)
	doc = append(doc, ubKey("line")...)
	doc = append(doc, ubObjBegin)
	doc = append(doc, ubKey("ch1")...)
	doc = append(doc, ubObjBegin)
	doc = append(doc, ubKey("mute")...)
	doc = append(doc, ubFloat(1.0)...)
	doc = append(doc, ubObjEnd)
	doc = append(doc, ubObjEnd)
	doc = append(doc, ubObjEnd)

	flat, err := ParseZB(zlibCompress(t, doc))
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := flat["line/ch1/mute"]; !ok || v != float64(1.0) {
		t.Fatalf("line/ch1/mute = %v (ok=%v), want 1", v, ok)
	}
}

func TestParseZBMalformedZlib(t *testing.T) {
	if _, err := ParseZB([]byte{0x00, 0x01, 0x02, 0x03}); err == nil {
		t.Fatal("expected zlib error")
	}
}

func TestDecodeUBJSONMalformed(t *testing.T) {
	tests := []struct {
		name string
		doc  []byte
	}{
		{"empty", nil},
		{"root not object", []byte{ubStr}},
		{"object not closed", []byte{ubObjBegin, ubKeyMarker, 0x01, 'a', ubInt8, 0x05}},
		{"truncated key bytes", []byte{ubObjBegin, ubKeyMarker, 0x05, 'a', 'b'}},
		{"truncated float value", []byte{ubObjBegin, ubKeyMarker, 0x01, 'a', ubFloat32, 0x00, 0x00}},
		{"unknown type marker", []byte{ubObjBegin, ubKeyMarker, 0x01, 'a', 0x7e}},
		{"string bad length marker", []byte{ubObjBegin, ubKeyMarker, 0x01, 'a', ubStr, 0x55, 0x02, 'x', 'y'}},
		{"array not closed", []byte{ubObjBegin, ubKeyMarker, 0x01, 'a', ubArrBegin, ubInt8, 0x01}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := decodeUBJSON(tc.doc); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

// TestParseZBTruncatedUBJSON confirms a valid zlib stream wrapping malformed
// UBJSON surfaces a decode error (not a panic).
func TestParseZBTruncatedUBJSON(t *testing.T) {
	bad := []byte{ubObjBegin, ubKeyMarker, 0x01, 'a', ubFloat32, 0x00} // truncated float
	if _, err := ParseZB(zlibCompress(t, bad)); err == nil {
		t.Fatal("expected UBJSON decode error")
	}
}
