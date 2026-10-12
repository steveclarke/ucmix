package daw

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// Stem file names carry export clutter a musician never wants on a track:
// a leading track number, version and format tags, words like "final" or
// "stem". trackName keeps what names the part ("Bass DI", "OH L").

var (
	// A track number is one or two digits, or more with a leading zero
	// ("001"); "808 Sub" keeps its 808.
	leadingNumber = regexp.MustCompile(`^\s*(0\d{1,3}|\d{1,2})\s*[.)\]]?\s+`)
	datePattern   = regexp.MustCompile(`\b(\d{4}[-.]\d{1,2}[-.]\d{1,2}|\d{1,2}[-.]\d{1,2}[-.]\d{4})\b`)
	// A hyphen is a separator next to a digit, a space or an end ("03-Pad"),
	// and part of the name between letters ("Hi-Hat").
	separatorHyphen = regexp.MustCompile(`(^|[\s\d])-+|-+($|[\s\d])`)
)

// noiseWords are dropped wherever they stand as whole words (case ignored).
var noiseWords = map[string]bool{
	"final": true, "finale": true, "stem": true, "stems": true, "bounce": true, "bounced": true,
	"export": true, "exported": true, "print": true, "printed": true, "master": true, "mixdown": true,
	"stereo": true, "mono": true, "st": true, "wav": true, "aif": true, "aiff": true, "copy": true,
	"new": true, "edit": true, "consolidated": true,
}

// noisePattern matches whole-word tags: versions, bit depths, sample rates,
// tempos, dates.
var noisePattern = regexp.MustCompile(`(?i)^(v\d+|ver\d+|version\d*|r\d+|\d+bits?|\d+b|\d+(\.\d+)?k(hz)?|\d+hz|\d+bpm|bpm\d+|\d{4,8})$`)

// cleanStemName turns a file name into a track name: "01_Kick_Final.wav" →
// "Kick", "03-Pad (Stereo) v2.wav" → "Pad", "02 Bass DI.wav" → "Bass DI".
func cleanStemName(file string) string {
	base := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
	raw := stripControl(base)
	s := strings.NewReplacer("_", " ", "(", " ", ")", " ", "[", " ", "]", " ", "{", " ", "}", " ").Replace(raw)
	s = datePattern.ReplaceAllString(s, " ")
	s = separatorHyphen.ReplaceAllString(s, "$1 $2")
	s = leadingNumber.ReplaceAllString(s+" ", "")
	var kept []string
	for _, w := range strings.Fields(s) {
		var parts []string
		for _, part := range strings.Split(w, "-") {
			if part != "" && !noiseWords[strings.ToLower(part)] && !noisePattern.MatchString(part) {
				parts = append(parts, part)
			}
		}
		if len(parts) > 0 {
			kept = append(kept, strings.Join(parts, "-"))
		}
	}
	// A lone leading number left over ("01 Kick" after other cleanup).
	if len(kept) > 1 && isDigits(kept[0]) && (len(kept[0]) <= 2 || kept[0][0] == '0') {
		kept = kept[1:]
	}
	name := strings.Join(kept, " ")
	if name == "" {
		name = strings.Join(strings.Fields(strings.NewReplacer("_", " ").Replace(raw)), " ")
	}
	if name == "" {
		name = "Audio"
	}
	if strings.ToLower(name) == name {
		r := []rune(name)
		r[0] = unicode.ToUpper(r[0])
		name = string(r)
	}
	if r := []rune(name); len(r) > 48 {
		name = strings.TrimSpace(string(r[:48]))
	}
	return name
}

// trackNames names a batch of stems: each cleaned, a prefix every name shares
// ("Song Name Kick", "Song Name Snare") dropped, and repeats numbered.
func trackNames(files []string) []string {
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = cleanStemName(f)
	}
	if len(names) > 1 {
		prefix := strings.Fields(names[0])
		for _, n := range names[1:] {
			w := strings.Fields(n)
			k := 0
			for k < len(prefix) && k < len(w) && strings.EqualFold(prefix[k], w[k]) {
				k++
			}
			prefix = prefix[:k]
		}
		if len(prefix) > 0 {
			ok := true
			for _, n := range names {
				if len(strings.Fields(n)) <= len(prefix) {
					ok = false
				}
			}
			if ok {
				for i, n := range names {
					names[i] = strings.Join(strings.Fields(n)[len(prefix):], " ")
				}
			}
		}
	}
	seen := map[string]int{}
	for i, n := range names {
		key := strings.ToLower(n)
		seen[key]++
		if seen[key] > 1 {
			names[i] = fmt.Sprintf("%s %d", n, seen[key])
		}
	}
	return names
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// stripControl removes control characters, so a file name cannot carry an
// escape sequence into a track name or a terminal.
func stripControl(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || (r >= 0x80 && r <= 0x9f) {
			return -1
		}
		return r
	}, s)
}
