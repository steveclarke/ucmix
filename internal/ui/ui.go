// Package ui holds the CLI's terminal styling: a small lipgloss palette and
// helpers for key/value lines, tables, and success/error output. Styling is
// gated by Init, which honors NO_COLOR and the global --no-color flag; when
// color is off every helper returns plain, unstyled text so piped and
// machine-read output stays clean.
package ui

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
)

// enabled reports whether styled output is on. It honors NO_COLOR at package
// init so error rendering before Init (e.g. a flag-parse failure) still respects
// it; Init refines it further with the --no-color flag.
var enabled = os.Getenv("NO_COLOR") == ""

// Palette. Kept deliberately small: a key color, a muted/hint color, and
// success/error accents.
var (
	keyStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true) // cyan
	hintStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Faint(true)
	successStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("2")) // green
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
	headerStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("4")).Bold(true)
)

// Init turns styling on or off for the process. Color is enabled only when
// noColor is false AND the NO_COLOR environment variable is unset (per the
// no-color.org convention). Call once from the root command before rendering.
func Init(noColor bool) {
	enabled = !noColor && os.Getenv("NO_COLOR") == ""
}

// Enabled reports whether styled output is currently on.
func Enabled() bool { return enabled }

// render applies s to text when styling is on, otherwise returns text verbatim.
func render(s lipgloss.Style, text string) string {
	if !enabled {
		return text
	}
	return s.Render(text)
}

// Key styles a key/label.
func Key(s string) string { return render(keyStyle, s) }

// Hint styles a dimmed hint line.
func Hint(s string) string { return render(hintStyle, s) }

// Header styles a section header.
func Header(s string) string { return render(headerStyle, s) }

// KeyValue formats one "key: value" line with the key styled.
func KeyValue(key string, value any) string {
	return fmt.Sprintf("%s %v", Key(key+":"), value)
}

// Success returns a styled success line prefixed with a check mark.
func Success(s string) string { return render(successStyle, "✓ "+s) }

// ErrorLine returns a styled error line prefixed with a cross.
func ErrorLine(s string) string { return render(errorStyle, "✗ "+s) }

// Table renders aligned two-column rows (key left, value right of a gutter).
// Keys are left-padded to the widest key so values line up. Row order is
// preserved; callers sort beforehand if they want it sorted.
func Table(rows [][2]string) string {
	width := 0
	for _, r := range rows {
		if len(r[0]) > width {
			width = len(r[0])
		}
	}
	var b strings.Builder
	for i, r := range rows {
		if i > 0 {
			b.WriteByte('\n')
		}
		pad := strings.Repeat(" ", width-len(r[0]))
		fmt.Fprintf(&b, "%s%s  %s", Key(r[0]), pad, r[1])
	}
	return b.String()
}

// SortedTable is Table over a path→value map with keys sorted ascending. Values
// are formatted with %v.
func SortedTable(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	rows := make([][2]string, len(keys))
	for i, k := range keys {
		rows[i] = [2]string{k, m[k]}
	}
	return Table(rows)
}
