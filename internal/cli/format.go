package cli

import (
	"encoding/hex"
	"fmt"

	"github.com/steveclarke/ucmix/internal/schema"
)

// humanize rounds a humanized read for display. A wire position is a 32-bit
// float and the tapers are logarithms and interpolations, so inverting one lands
// a hair off the number that was dialed: 90 Hz reads back as 90.00000306 and
// -6 dB as -6.00000085, with the exact noise differing by architecture. A read
// should look like the value that was set, so tapered floats round to
// schema.HumanPlaces on the way out.
//
// Only presentation is rounded. Comparisons run on the exact value against a
// per-unit tolerance, and the library's Get still returns the exact inversion.
// Untapered floats are raw wire positions with no human unit and are left alone,
// as is everything that is not a float.
func humanize(path string, v any) any {
	spec, known := schema.Lookup(path)
	if !known || spec.Taper == nil {
		return v
	}
	f, ok := v.(float64)
	if !ok {
		return v
	}
	return schema.RoundHuman(f)
}

// displayValue renders a value for human (non-JSON) output. Byte slices — a raw
// chars payload on a key the schema does not know — become hex; everything else
// uses the default Go formatting. Colors reach here already canonical (8
// lowercase RGBA hex digits) and print verbatim.
func displayValue(v any) string {
	switch b := v.(type) {
	case []byte:
		return hex.EncodeToString(b)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// jsonValue normalizes a value for a JSON envelope so machine output agrees with
// the human/get views: byte slices become a hex string instead of Go's default
// base64 for []byte. Other values pass through unchanged.
func jsonValue(v any) any {
	if b, ok := v.([]byte); ok {
		return hex.EncodeToString(b)
	}
	return v
}
