package daw

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveclarke/ucmix/internal/proto"
)

// useTempFavorites points favorites and preset folders at a temp dir.
func useTempFavorites(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	origFav, origRoots := favoritesPath, presetRoots
	t.Cleanup(func() { favoritesPath, presetRoots = origFav, origRoots })
	favoritesPath = func() (string, error) { return filepath.Join(dir, "ucdaw", "favorites.json"), nil }
	presetRoots = func() []string { return []string{filepath.Join(dir, "Presets")} }
	return dir
}

func TestFavoritesSetListRemove(t *testing.T) {
	useFixturePlugins(t)
	useTempFavorites(t)
	if res, _ := Favorites(Request{Action: "list"}); res.Message != "No favorites yet." {
		t.Errorf("empty = %q", res.Message)
	}
	res, err := Favorites(Request{Action: "set", Name: "clean guitar", Plugin: "amp thing", Preset: "Clean"})
	if err != nil || !strings.HasPrefix(res.Message, "Remembered clean guitar: Amp Thing 7 with preset Clean; Studio Pro has no preset file") {
		t.Errorf("set = %q, %v", res.Message, err)
	}
	res, _ = Favorites(Request{Action: "set", Name: "worship synth", Plugin: "Mai Tai"})
	if res.Message != "Remembered worship synth: Mai Tai (instrument)." {
		t.Errorf("set instrument = %q", res.Message)
	}
	res, _ = Favorites(Request{Action: "list"})
	if res.Message != "2 favorites: clean guitar: Amp Thing 7 with preset Clean; worship synth: Mai Tai (instrument)." {
		t.Errorf("list = %q", res.Message)
	}
	if res, err := Favorites(Request{Action: "remove", Name: "my worship synth"}); err != nil || res.Message != "Forgot my worship synth." {
		t.Errorf("remove = %q, %v", res.Message, err)
	}
	if _, err := Favorites(Request{Action: "remove", Name: "bass"}); err == nil {
		t.Error("removing a missing favorite should fail")
	}
	if _, err := Favorites(Request{Action: "set", Name: "x", Plugin: "No Such Thing"}); err == nil {
		t.Error("unknown plug-in accepted")
	}
}

func TestFavoritePresetFileFound(t *testing.T) {
	useFixturePlugins(t)
	dir := useTempFavorites(t)
	presetDir := filepath.Join(dir, "Presets", "Example Audio", "Amp Thing 7", "Mine")
	if err := os.MkdirAll(presetDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(presetDir, "Clean.preset"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := Favorites(Request{Action: "set", Name: "clean", Plugin: "Amp Thing 7", Preset: "Mine/Clean"})
	if err != nil || res.Message != "Remembered clean: Amp Thing 7 with preset Mine/Clean." {
		t.Errorf("set = %q, %v", res.Message, err)
	}
}

func TestInsertAndTrackAddUseFavorite(t *testing.T) {
	useFixturePlugins(t)
	useTempFavorites(t)
	if _, err := Favorites(Request{Action: "set", Name: "clean guitar", Plugin: "Amp Thing 7", Preset: "Clean"}); err != nil {
		t.Fatal(err)
	}
	st, fake := newStudio(t)
	fake.echoStrings = true
	fake.onCommand["Track|Add Insert to Selected Channels"] = []proto.Frame{
		pv("mixer/channel/ch1/inserts/count", 0.001),
		ps("mixer/channel/ch1/inserts/slot1/deviceName", "Amp Thing 7"),
		ps("surface/focusPresetName", "Clean"),
	}
	got := do(t, st, Request{Op: "add_track", Action: "mono", Name: "Guitar", Plugin: "my clean guitar"})
	want := `Added mono audio track Guitar; added Amp Thing 7 to Guitar (insert 1) with preset Clean, from your favorite "clean guitar".`
	if got != want {
		t.Errorf("add_track = %q", got)
	}
	if body := fake.sentJM()[len(fake.sentJM())-1]; !strings.Contains(body, `"preset": "Clean"`) {
		t.Errorf("body = %s", body)
	}
}

func instrumentFake(t *testing.T, preset string) (*Studio, *fakeStudio) {
	t.Helper()
	st, fake := newStudio(t)
	fake.echoStrings = true
	open := false
	fake.onInvoke = func(category, name string, args map[string]string) []proto.Frame {
		switch category + "|" + name {
		case "Track|Add Instrument and Track":
			if args["openEditor"] != "0" {
				t.Errorf("openEditor = %q", args["openEditor"])
			}
			return []proto.Frame{
				pv("mixer/channel/count", 0.001),
				pv("mixer/channel/ch1/selected", 1),
				ps("mixer/channel/ch1/label", "Mai Tai"),
				ps("mixer/channel/ch1/type", "AudioSynth"),
			}
		case "Console|Show Instrument Editor":
			open = !open
			if open {
				return []proto.Frame{ps("surface/focusDeviceName", "1 - Mai Tai"), ps("surface/focusPresetName", preset)}
			}
			return []proto.Frame{ps("surface/focusDeviceName", ""), ps("surface/focusPresetName", "")}
		}
		return nil
	}
	return st, fake
}

func TestAddInstrumentNamesAndReadsPreset(t *testing.T) {
	useFixturePlugins(t)
	useTempFavorites(t)
	st, _ := instrumentFake(t, "Warm Pad")
	got := do(t, st, Request{Op: "add_instrument", Plugin: "mai tai", Preset: "Pads/Warm Pad", Name: "Pad"})
	if got != "Added instrument track Pad with Mai Tai (preset Warm Pad)." {
		t.Errorf("add_instrument = %q", got)
	}
	if s, _ := st.model().Str("surface/focusDeviceName"); s != "" {
		t.Error("instrument editor left open")
	}
	if s := do(t, st, Request{Op: "song"}); !strings.Contains(s, "Pad (instrument)") {
		t.Errorf("song = %q", s)
	}
}

func TestAddInstrumentFromFavoriteAndWrongPreset(t *testing.T) {
	useFixturePlugins(t)
	useTempFavorites(t)
	if _, err := Favorites(Request{Action: "set", Name: "worship synth", Plugin: "Mai Tai", Preset: "Pads/Warm Pad"}); err != nil {
		t.Fatal(err)
	}
	st, _ := instrumentFake(t, "default")
	got := do(t, st, Request{Op: "add_instrument", Plugin: "my worship synth", Name: "Keys"})
	if got != `Added instrument track Keys with Mai Tai; Studio Pro loaded "default", not "Pads/Warm Pad", from your favorite "worship synth".` {
		t.Errorf("add_instrument = %q", got)
	}
}

func TestAddInstrumentRefusesEffects(t *testing.T) {
	useFixturePlugins(t)
	useTempFavorites(t)
	st, _ := newStudio(t)
	if _, err := st.Do(context.Background(), Request{Op: "add_instrument", Plugin: "Pro EQ"}); err == nil || !strings.Contains(err.Error(), "effect") {
		t.Errorf("err = %v", err)
	}
}
