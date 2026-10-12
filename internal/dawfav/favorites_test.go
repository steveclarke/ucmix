package dawfav

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKey(t *testing.T) {
	for in, want := range map[string]string{
		"My clean preset":   "clean",
		"  Worship   Synth": "worship synth",
		"my usual pad!":     "pad",
		"the":               "",
	} {
		if got := Key(in); got != want {
			t.Errorf("Key(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSetFindRemove(t *testing.T) {
	var favs []Favorite
	favs, replaced, err := Set(favs, Favorite{Name: "Clean Guitar", Plugin: "Guitar Rig 7", Preset: "Clean", Kind: "effect"})
	if err != nil || replaced != nil {
		t.Fatal(err)
	}
	favs, _, _ = Set(favs, Favorite{Name: "worship synth", Plugin: "Syntronik 2", Kind: "instrument"})
	favs, replaced, _ = Set(favs, Favorite{Name: "my clean guitar", Plugin: "Guitar Rig 7", Preset: "Clean 2", Kind: "effect"})
	if replaced == nil || replaced.Preset != "Clean" || len(favs) != 2 {
		t.Fatalf("replace = %+v, %d favs", replaced, len(favs))
	}
	for said, want := range map[string]string{"my clean guitar": "Clean 2", "clean": "Clean 2", "Worship Synth": ""} {
		f, ok := Find(favs, said)
		if !ok || f.Preset != want {
			t.Errorf("Find(%q) = %+v, %v", said, f, ok)
		}
	}
	if _, ok := Find(favs, "bass"); ok {
		t.Error("found a favorite that does not exist")
	}
	favs, found := Remove(favs, "THE clean guitar")
	if !found || len(favs) != 1 {
		t.Errorf("remove = %v, %d", found, len(favs))
	}
}

func TestCleanRejectsAndStrips(t *testing.T) {
	if _, _, err := Set(nil, Favorite{Name: "my", Plugin: "X", Kind: "effect"}); err == nil {
		t.Error("empty name accepted")
	}
	if _, _, err := Set(nil, Favorite{Name: "a", Plugin: "", Kind: "effect"}); err == nil {
		t.Error("empty plug-in accepted")
	}
	if _, _, err := Set(nil, Favorite{Name: "a", Plugin: "X", Kind: "drum"}); err == nil {
		t.Error("bad kind accepted")
	}
	if _, _, err := Set(nil, Favorite{Name: strings.Repeat("a", 300), Plugin: "X", Kind: "effect"}); err == nil {
		t.Error("long name accepted")
	}
	favs, _, err := Set(nil, Favorite{Name: "pad\x1b[31m", Plugin: "Syn\ttronik", Kind: "instrument"})
	if err != nil || favs[0].Name != "pad[31m" || favs[0].Plugin != "Syntronik" {
		t.Errorf("clean = %+v, %v", favs, err)
	}
}

func TestSaveLoadPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ucdaw")
	path := filepath.Join(dir, "favorites.json")
	if favs, err := Load(path); err != nil || favs != nil {
		t.Fatalf("missing file = %v, %v", favs, err)
	}
	want := []Favorite{{Name: "pad", Plugin: "Syntronik 2", Preset: "Pads/Warm", Kind: "instrument"}}
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	dinfo, _ := os.Stat(dir)
	if info.Mode().Perm() != 0o600 || dinfo.Mode().Perm() != 0o700 {
		t.Errorf("modes = %v, %v", info.Mode().Perm(), dinfo.Mode().Perm())
	}
	got, err := Load(path)
	if err != nil || len(got) != 1 || got[0] != want[0] {
		t.Errorf("load = %+v, %v", got, err)
	}
	if err := os.WriteFile(path, []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("bad JSON accepted")
	}
	big := make([]byte, MaxFileSize+1)
	if err := os.WriteFile(path, big, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("oversized file accepted")
	}
}
