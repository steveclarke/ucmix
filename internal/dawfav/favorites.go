// Package dawfav keeps a musician's favorites: the words they use ("my clean
// guitar", "worship synth") mapped to a plug-in and one of its Studio Pro
// presets. The file is private to the user (0600 in a 0700 directory).
package dawfav

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// Favorite is one remembered sound.
type Favorite struct {
	Name   string `json:"name"`
	Plugin string `json:"plugin"`
	Preset string `json:"preset,omitempty"`
	// Kind is "effect" or "instrument".
	Kind string `json:"kind"`
}

// Limits on the file, so a damaged or hostile file cannot exhaust memory.
const (
	MaxFavorites = 500
	MaxFileSize  = 1 << 20
	MaxField     = 200
)

// DefaultPath is favorites.json in the user's config directory
// (~/Library/Application Support/ucdaw on macOS), or $UCDAW_FAVORITES.
func DefaultPath() (string, error) {
	if p := os.Getenv("UCDAW_FAVORITES"); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("dawfav: finding the config directory: %w", err)
	}
	return filepath.Join(dir, "ucdaw", "favorites.json"), nil
}

// Load reads favorites; a missing file is an empty list.
func Load(path string) ([]Favorite, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("dawfav: opening %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > MaxFileSize {
		return nil, fmt.Errorf("dawfav: %s is larger than %d bytes", path, MaxFileSize)
	}
	var file struct {
		Favorites []Favorite `json:"favorites"`
	}
	if err := json.NewDecoder(f).Decode(&file); err != nil {
		return nil, fmt.Errorf("dawfav: reading %s: %w", path, err)
	}
	if len(file.Favorites) > MaxFavorites {
		file.Favorites = file.Favorites[:MaxFavorites]
	}
	return file.Favorites, nil
}

// Save writes favorites atomically, 0600 in a 0700 directory.
func Save(path string, favs []Favorite) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("dawfav: creating %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	sort.Slice(favs, func(i, j int) bool { return Key(favs[i].Name) < Key(favs[j].Name) })
	body, err := json.MarshalIndent(struct {
		Favorites []Favorite `json:"favorites"`
	}{favs}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".favorites-*.json")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(body, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Key normalizes what a musician says: case, spacing, punctuation, and the
// words "my", "the", "preset" and "sound" ("My clean preset" = "clean").
func Key(name string) string {
	var words []string
	for _, w := range strings.FieldsFunc(strings.ToLower(name), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		switch w {
		case "my", "the", "preset", "sound", "usual":
			continue
		}
		words = append(words, w)
	}
	return strings.Join(words, " ")
}

// Clean checks a favorite's fields and strips control characters.
func Clean(f Favorite) (Favorite, error) {
	clean := func(s string) string {
		return strings.TrimSpace(strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return -1
			}
			return r
		}, s))
	}
	f.Name, f.Plugin, f.Preset = clean(f.Name), clean(f.Plugin), clean(f.Preset)
	switch {
	case Key(f.Name) == "":
		return f, errors.New("give the favorite a name, e.g. clean guitar")
	case f.Plugin == "":
		return f, errors.New("name the plug-in or instrument")
	case len(f.Name) > MaxField || len(f.Plugin) > MaxField || len(f.Preset) > MaxField:
		return f, fmt.Errorf("names are at most %d characters", MaxField)
	case f.Kind != "effect" && f.Kind != "instrument":
		return f, errors.New("a favorite is an effect or an instrument")
	}
	return f, nil
}

// Find looks a favorite up by what the musician said.
func Find(favs []Favorite, said string) (Favorite, bool) {
	k := Key(said)
	if k == "" {
		return Favorite{}, false
	}
	for _, f := range favs {
		if Key(f.Name) == k {
			return f, true
		}
	}
	// "clean" finds "clean guitar" when only one favorite has every word.
	var found []Favorite
	for _, f := range favs {
		have := " " + Key(f.Name) + " "
		all := true
		for _, w := range strings.Fields(k) {
			if !strings.Contains(have, " "+w+" ") {
				all = false
				break
			}
		}
		if all {
			found = append(found, f)
		}
	}
	if len(found) == 1 {
		return found[0], true
	}
	return Favorite{}, false
}

// Set adds or replaces a favorite; it reports the one it replaced.
func Set(favs []Favorite, f Favorite) ([]Favorite, *Favorite, error) {
	f, err := Clean(f)
	if err != nil {
		return favs, nil, err
	}
	out := make([]Favorite, 0, len(favs)+1)
	var replaced *Favorite
	for _, old := range favs {
		if Key(old.Name) == Key(f.Name) {
			o := old
			replaced = &o
			continue
		}
		out = append(out, old)
	}
	if len(out) >= MaxFavorites {
		return favs, nil, fmt.Errorf("at most %d favorites", MaxFavorites)
	}
	return append(out, f), replaced, nil
}

// Remove drops a favorite; it reports whether one was there.
func Remove(favs []Favorite, said string) ([]Favorite, bool) {
	k := Key(said)
	out := make([]Favorite, 0, len(favs))
	found := false
	for _, f := range favs {
		if Key(f.Name) == k {
			found = true
			continue
		}
		out = append(out, f)
	}
	return out, found
}
