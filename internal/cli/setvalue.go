package cli

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/steveclarke/ucmix/internal/schema"
)

// parseSetValue turns a command-line value string into the Go value the client's
// Set expects for a path. When the schema knows the path, the value is coerced
// to that key's kind; for unknown keys the type is inferred from the literal
// (bool keyword, number, else string).
//
// Supported literals:
//
//	on/off, true/false, yes/no, 1/0   → bool
//	-6dB, 100Hz, 400ms, 0.746         → float (unit suffix stripped, no scaling)
//	4ed2ff / #4ed2ff / 4ed2ffff       → color bytes (RGB gets a 0xff alpha)
//	"Kick Drum" / Kick                → string (surrounding quotes stripped)
func parseSetValue(spec schema.KeySpec, known bool, raw string) (any, error) {
	if !known {
		return inferValue(raw), nil
	}
	switch spec.Kind {
	case schema.KindBool:
		b, ok := parseBool(raw)
		if !ok {
			return nil, fmt.Errorf("expected a boolean (on/off, true/false), got %q", raw)
		}
		return b, nil
	case schema.KindFloat:
		f, ok := parseFloatWithUnit(raw)
		if !ok {
			return nil, fmt.Errorf("expected a number (e.g. -6dB, 100Hz, 0.5), got %q", raw)
		}
		return f, nil
	case schema.KindString:
		return unquote(raw), nil
	case schema.KindChars:
		b, err := parseColor(raw)
		if err != nil {
			return nil, err
		}
		return b, nil
	default:
		return nil, fmt.Errorf("unhandled key kind for value %q", raw)
	}
}

// inferValue guesses a type for an unknown-key value: bool keyword, then number,
// then string.
func inferValue(raw string) any {
	if b, ok := parseBool(raw); ok {
		return b
	}
	if f, ok := parseFloatWithUnit(raw); ok {
		return f
	}
	return unquote(raw)
}

// parseBool recognizes the on/off family. The second result is false for
// anything that is not a boolean literal.
func parseBool(raw string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "on", "true", "yes", "1":
		return true, true
	case "off", "false", "no", "0":
		return false, true
	default:
		return false, false
	}
}

// unitSuffixes are stripped (case-insensitively) before parsing a float. No
// scaling is applied — the numeric part is taken as-is (Hz/dB/ms are already the
// human unit the tapers speak).
var unitSuffixes = []string{"dB", "Hz", "ms", "s"}

// parseFloatWithUnit parses a float, tolerating one trailing unit suffix.
func parseFloatWithUnit(raw string) (float64, bool) {
	s := strings.TrimSpace(raw)
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f, true
	}
	for _, u := range unitSuffixes {
		if len(s) > len(u) && strings.EqualFold(s[len(s)-len(u):], u) {
			num := strings.TrimSpace(s[:len(s)-len(u)])
			if f, err := strconv.ParseFloat(num, 64); err == nil {
				return f, true
			}
		}
	}
	return 0, false
}

// parseColor decodes a hex color into wire bytes. A 3-byte RGB value gets a
// fully-opaque 0xff alpha appended (the board's RGBA format); a 4-byte value is
// taken as-is. A leading '#' is allowed.
func parseColor(raw string) ([]byte, error) {
	s := strings.TrimPrefix(strings.TrimSpace(raw), "#")
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("color %q is not hex: %w", raw, err)
	}
	switch len(b) {
	case 3:
		return append(b, 0xff), nil
	case 4:
		return b, nil
	default:
		return nil, fmt.Errorf("color %q must be 6 (RGB) or 8 (RGBA) hex digits", raw)
	}
}

// unquote strips one layer of matching surrounding single or double quotes.
func unquote(raw string) string {
	s := strings.TrimSpace(raw)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
