package daw

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveclarke/ucmix/internal/dawplugins"
	"github.com/steveclarke/ucmix/internal/proto"
)

func useFixturePlugins(t *testing.T) {
	t.Helper()
	orig := loadPlugins
	t.Cleanup(func() { loadPlugins = orig })
	loadPlugins = func() ([]dawplugins.Plugin, error) {
		f, err := os.Open(filepath.Join("..", "dawplugins", "testdata", "plugins.settings"))
		if err != nil {
			return nil, err
		}
		defer func() { _ = f.Close() }()
		return dawplugins.Parse(f)
	}
}

func studioWithTrack(t *testing.T) (*Studio, *fakeStudio) {
	t.Helper()
	st, fake := newStudio(t)
	fake.echoStrings = true
	if got := do(t, st, Request{Op: "add_track", Action: "mono", Name: "Guitar"}); got != "Added mono audio track Guitar." {
		t.Fatalf("add_track = %q", got)
	}
	return st, fake
}

func TestAddTrackNamesIt(t *testing.T) {
	st, _ := studioWithTrack(t)
	if s, _ := st.model().Str("mixer/channel/ch1/label"); s != "Guitar" {
		t.Errorf("label = %q", s)
	}
}

func TestInsertWithPresetReadsBack(t *testing.T) {
	useFixturePlugins(t)
	st, fake := studioWithTrack(t)
	fake.onCommand["Track|Add Insert to Selected Channels"] = []proto.Frame{
		pv("mixer/channel/ch1/inserts/count", 0.001),
		ps("mixer/channel/ch1/inserts/slot1/deviceName", "Amp Thing 7"),
		ps("surface/focusPresetName", "Clean"),
	}
	got := do(t, st, Request{Op: "insert", Path: "guitar", Name: "amp thing", Value: "Clean"})
	if got != "Added Amp Thing 7 to Guitar (insert 1) with preset Clean." {
		t.Errorf("insert = %q", got)
	}
	body := fake.sentJM()[len(fake.sentJM())-1]
	if !strings.Contains(body, `"cid": "{11111111-1111-1111-1111-111111111111}"`) || !strings.Contains(body, `"preset": "Clean"`) {
		t.Errorf("body = %s", body)
	}
	if v, _ := st.model().Num("mixer/channel/ch1/selected"); v != 1 {
		t.Error("channel not selected before insert")
	}
}

func TestInsertReportsMissingPreset(t *testing.T) {
	useFixturePlugins(t)
	st, fake := studioWithTrack(t)
	fake.onCommand["Track|Add Insert to Selected Channels"] = []proto.Frame{
		pv("mixer/channel/ch1/inserts/count", 0.001),
		ps("mixer/channel/ch1/inserts/slot1/deviceName", "Pro EQ"),
		ps("surface/focusPresetName", "default"),
	}
	got := do(t, st, Request{Op: "insert", Path: "ch1", Name: "Pro EQ", Value: "Nope"})
	if !strings.Contains(got, `no preset "Nope"`) {
		t.Errorf("insert = %q", got)
	}
}

func TestInsertRefusesInstrumentsAndUnknownChannels(t *testing.T) {
	useFixturePlugins(t)
	st, _ := studioWithTrack(t)
	var p Problem
	if _, err := st.Do(context.Background(), Request{Op: "insert", Path: "Guitar", Name: "Mai Tai"}); !errors.As(err, &p) || !strings.Contains(p.Message, "instrument") {
		t.Errorf("instrument = %v", err)
	}
	if _, err := st.Do(context.Background(), Request{Op: "insert", Path: "Bass", Name: "Pro EQ"}); !errors.As(err, &p) || p.Hint != "channels: Guitar" {
		t.Errorf("unknown channel = %+v", err)
	}
}

func TestUndoReportNamesRemovedPlugin(t *testing.T) {
	before := []Channel{{Path: "mixer/channel/ch1", Name: "Guitar", Kind: "AudioTrack", Inserts: []string{"Pro EQ", "Pro EQ"}}}
	added, removed := diffLists(before[0].Inserts, []string{"Pro EQ"})
	if len(added) != 0 || len(removed) != 1 || removed[0] != "Pro EQ" {
		t.Errorf("diff = %v %v", added, removed)
	}
}

func TestPluginsFilter(t *testing.T) {
	useFixturePlugins(t)
	res, err := Plugins("example")
	if err != nil || res.Message != `1 plug-in match "example".` {
		t.Errorf("Plugins = %q, %v", res.Message, err)
	}
}

func TestFaderInDB(t *testing.T) {
	st, fake := studioWithTrack(t)
	fake.mu.Lock()
	fake.frames <- pv("mixer/channel/ch1/volume", 0.76)
	fake.mu.Unlock()
	_ = st.session.WaitFor(context.Background(), NumIs("mixer/channel/ch1/volume", 0.76))
	if got := do(t, st, Request{Op: "fader", Path: "Guitar"}); got != "Guitar is at 0 dB." {
		t.Errorf("read = %q", got)
	}
	if got := do(t, st, Request{Op: "fader", Path: "Guitar", Action: "down", Value: "3"}); got != "Guitar at -3 dB (was 0 dB)." {
		t.Errorf("down = %q", got)
	}
	if got := do(t, st, Request{Op: "fader", Path: "Guitar", Action: "set", Value: "-10"}); got != "Guitar at -10 dB (was -3 dB)." {
		t.Errorf("set = %q", got)
	}
	if got := do(t, st, Request{Op: "song"}); !strings.Contains(got, "Guitar (audio, mono, -10 dB)") {
		t.Errorf("song = %q", got)
	}
	for _, bad := range []Request{
		{Op: "fader", Path: "Guitar", Action: "up", Value: "30"},
		{Op: "fader", Path: "Guitar", Action: "up", Value: "lots"},
		{Op: "fader", Path: "Guitar", Action: "set", Value: "+11"},
	} {
		if _, err := st.Do(context.Background(), bad); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
}
