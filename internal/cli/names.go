package cli

import "strings"

// colorNames maps a handful of common color words to the hex the mixer's color
// key accepts. It is a convenience for the noun commands only; a hex value like
// 4ed2ff still works, and any unknown word falls through to the hex parser,
// which reports it. Only well-known, unambiguous colors are listed — the mixer
// accepts any RGB value, so the table need not be exhaustive.
var colorNames = map[string]string{
	"blue":  "4ed2ff",
	"red":   "ff0000",
	"green": "52bc4d",
	"white": "ffffff",
	"black": "000000",
}

// iconNames maps a few common instrument words to StudioLive icon ids. Only the
// ids documented in the path reference are listed; an id like drums/drumset
// still works verbatim, and an unknown word falls through unchanged.
var iconNames = map[string]string{
	"drums": "drums/drumset",
	"bass":  "guitars/bass",
	"vocal": "vocals/leadvocals",
}

// resolveColor turns a color word into its hex; a value not in the table (a hex
// literal or an unknown word) passes through for the set parser to validate.
func resolveColor(v string) string {
	if hex, ok := colorNames[strings.ToLower(strings.TrimSpace(v))]; ok {
		return hex
	}
	return v
}

// resolveIcon turns an instrument word into its icon id; anything not in the
// table passes through unchanged.
func resolveIcon(v string) string {
	if id, ok := iconNames[strings.ToLower(strings.TrimSpace(v))]; ok {
		return id
	}
	return v
}
