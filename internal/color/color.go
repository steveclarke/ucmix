// Package color is the single canonical rendering for a StudioLive channel
// color, plus the codec that reaches it from every form a color arrives in.
//
// A color reaches ucmix in three different shapes:
//
//   - 4 raw RGBA bytes, from a live PC delta frame (the wire form).
//   - An ABGR-packed integer, from a ZB/CK snapshot — the little-endian read of
//     those same 4 wire bytes, which is what the board reports on a full read.
//   - A hex string, from a config file or a `set` argument, with or without a
//     leading "#" and with or without the trailing alpha byte.
//
// [Canonical] folds all three to one form: 8 lowercase hex digits in RGBA order
// (e.g. "8f96a3ff"). That string is what ucmix stores, prints, and compares
// everywhere, so the same color read two different ways always renders the same.
package color

import (
	"encoding/hex"
	"fmt"
	"math"
	"strings"
)

// Canonical converts any supported color form to the canonical 8-digit
// lowercase RGBA hex string. It reports false for a value that is not a color
// in any recognized form.
func Canonical(v any) (string, bool) {
	switch c := v.(type) {
	case string:
		return canonicalHex(c)
	case []byte:
		return canonicalBytes(c)
	case int:
		return packedToHex(uint32(c)), true
	case int32:
		return packedToHex(uint32(c)), true
	case int64:
		return packedToHex(uint32(c)), true
	case uint32:
		return packedToHex(c), true
	case uint64:
		return packedToHex(uint32(c)), true
	case float32:
		return canonicalFloat(float64(c))
	case float64:
		return canonicalFloat(c)
	default:
		return "", false
	}
}

// Parse converts a user-supplied hex color to its 4 RGBA wire bytes, appending
// the opaque alpha when the input is 6 digits. A leading "#" is accepted.
func Parse(s string) ([]byte, error) {
	h, ok := canonicalHex(s)
	if !ok {
		return nil, fmt.Errorf("color %q is not 6 or 8 hex digits", s)
	}
	b, err := hex.DecodeString(h)
	if err != nil {
		return nil, fmt.Errorf("color %q is not hex: %w", s, err)
	}
	return b, nil
}

// canonicalHex normalizes a hex string: strips "#", lowercases, and appends the
// opaque alpha to a 6-digit value.
func canonicalHex(s string) (string, bool) {
	h := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(s), "#"))
	if len(h) != 6 && len(h) != 8 {
		return "", false
	}
	for i := 0; i < len(h); i++ {
		if !isHexDigit(h[i]) {
			return "", false
		}
	}
	if len(h) == 6 {
		h += "ff"
	}
	return h, true
}

// canonicalBytes renders 3 or 4 RGB(A) bytes, appending the opaque alpha to a
// 3-byte value.
func canonicalBytes(b []byte) (string, bool) {
	switch len(b) {
	case 3:
		return hex.EncodeToString(b) + "ff", true
	case 4:
		return hex.EncodeToString(b), true
	default:
		return "", false
	}
}

// canonicalFloat accepts a packed color that survived a float round-trip (a
// JSON-decoded snapshot), rejecting anything not an exact uint32.
func canonicalFloat(f float64) (string, bool) {
	if f != math.Trunc(f) || f < 0 || f > math.MaxUint32 {
		return "", false
	}
	return packedToHex(uint32(f)), true
}

// packedToHex unpacks an ABGR-packed integer — the little-endian read of the
// R,G,B,A wire bytes — back to canonical RGBA hex.
func packedToHex(p uint32) string {
	return hex.EncodeToString([]byte{byte(p), byte(p >> 8), byte(p >> 16), byte(p >> 24)})
}

func isHexDigit(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f'
}
