package dawcli

import (
	"strings"
	"unicode"
)

// safe makes text from Studio Pro (track names, command names, titles) safe to
// print on a terminal: C0 and C1 control characters, ESC and DEL become "?",
// so a name cannot carry an escape sequence. Tabs stay; JSON output is not
// passed through here because the encoder escapes control characters itself.
func safe(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' {
			return r
		}
		if unicode.IsControl(r) || (r >= 0x80 && r <= 0x9f) {
			return '?'
		}
		return r
	}, s)
}
