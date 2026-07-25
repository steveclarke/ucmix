package cli

import (
	"encoding/hex"
	"fmt"
)

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
