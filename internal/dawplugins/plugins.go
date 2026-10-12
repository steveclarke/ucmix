// Package dawplugins reads the plug-ins Studio Pro has scanned on this Mac
// (its Plugins-en.settings file) so ucdaw can insert one by name. Studio Pro
// writes the file itself; ucdaw only reads it.
package dawplugins

import (
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Plugin is one effect or instrument Studio Pro can insert.
type Plugin struct {
	Name     string `json:"name"`
	ClassID  string `json:"classId"`
	Category string `json:"category"`
	Vendor   string `json:"vendor,omitempty"`
	Format   string `json:"format,omitempty"`
}

// Instrument reports whether the plug-in is an instrument rather than an effect.
func (p Plugin) Instrument() bool { return p.Category == "AudioSynth" }

var insertable = map[string]bool{"AudioEffect": true, "AudioSynth": true, "MusicEffect": true}

// DefaultPath is the plug-in list Studio Pro 8 keeps for this Mac's architecture.
func DefaultPath() string {
	home, _ := os.UserHomeDir()
	arch := "Arm64"
	if runtime.GOARCH == "amd64" {
		arch = "x64"
	}
	return filepath.Join(home, "Library", "Application Support", "Fender", "Studio Pro 8", arch, "Plugins-en.settings")
}

// Load reads the plug-in list at path (DefaultPath when empty).
func Load(path string) ([]Plugin, error) {
	if path == "" {
		path = DefaultPath()
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("dawplugins: opening %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	return Parse(f)
}

type settings struct {
	Sections []struct {
		Version struct {
			Vendor string `xml:"vendor,attr"`
		} `xml:"Attributes>Version"`
		Classes []struct {
			ClassID     string `xml:"classID,attr"`
			Category    string `xml:"category,attr"`
			Name        string `xml:"name,attr"`
			SubCategory string `xml:"subCategory,attr"`
		} `xml:"Attributes>List>ClassDescription"`
	} `xml:"Section"`
}

// Parse decodes a Plugins-en.settings document, keeping effects and
// instruments, sorted by name.
func Parse(r io.Reader) ([]Plugin, error) {
	var s settings
	if err := xml.NewDecoder(r).Decode(&s); err != nil {
		return nil, fmt.Errorf("dawplugins: parsing: %w", err)
	}
	var out []Plugin
	for _, section := range s.Sections {
		for _, c := range section.Classes {
			if !insertable[c.Category] || c.ClassID == "" || c.Name == "" {
				continue
			}
			format := c.SubCategory
			if i := strings.Index(format, "/"); i >= 0 {
				format = format[:i]
			}
			out = append(out, Plugin{
				Name: c.Name, ClassID: c.ClassID, Category: c.Category,
				Vendor: section.Version.Vendor, Format: format,
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

// Find resolves a plug-in by name: an exact match (ignoring case) wins, then a
// unique match on the start of the name, then a unique match anywhere in it.
func Find(all []Plugin, name string) (Plugin, error) {
	want := strings.ToLower(strings.TrimSpace(name))
	if want == "" {
		return Plugin{}, fmt.Errorf("name a plug-in")
	}
	var exact, prefix, contains []Plugin
	for _, p := range all {
		n := strings.ToLower(p.Name)
		switch {
		case n == want:
			exact = append(exact, p)
		case strings.HasPrefix(n, want):
			prefix = append(prefix, p)
		case strings.Contains(n, want):
			contains = append(contains, p)
		}
	}
	for _, set := range [][]Plugin{exact, prefix, contains} {
		switch len(set) {
		case 0:
			continue
		case 1:
			return set[0], nil
		}
		if len(set) == len(exact) && sameVendor(set) {
			return set[0], nil // same plug-in in several formats: Studio Pro's first
		}
		names := make([]string, len(set))
		for i, p := range set {
			names[i] = p.Name
			if p.Vendor != "" {
				names[i] += " (" + p.Vendor + ")"
			}
		}
		return Plugin{}, fmt.Errorf("%q matches %s; say which", name, strings.Join(names, ", "))
	}
	return Plugin{}, fmt.Errorf("no plug-in called %q is installed", name)
}

func sameVendor(set []Plugin) bool {
	for _, p := range set[1:] {
		if p.Vendor != set[0].Vendor {
			return false
		}
	}
	return true
}
