package daw

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/steveclarke/ucmix/internal/proto"
)

// useTrackPresets writes empty track preset files under a temp preset root,
// the way Studio Pro lays them out, and points the preset folders at it.
func useTrackPresets(t *testing.T, names ...string) {
	t.Helper()
	dir := useTempFavorites(t)
	for _, n := range names {
		file := filepath.Join(dir, "Presets", "User Presets", "Track Presets", filepath.FromSlash(n)+".trackpreset")
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTrackPresetsList(t *testing.T) {
	useTrackPresets(t)
	if res, err := TrackPresets(""); err != nil || !strings.HasPrefix(res.Message, "No track presets are saved yet") {
		t.Errorf("none = %q, %v", res.Message, err)
	}
	useTrackPresets(t, "Mine/Album/Bass", "Mine/Demo/Bass", "Mine/Drum Kit")
	res, err := TrackPresets("")
	if err != nil || res.Message != "3 track presets: Mine/Album/Bass, Mine/Demo/Bass, Mine/Drum Kit." {
		t.Errorf("all = %q, %v", res.Message, err)
	}
	if res, _ := TrackPresets("KIT"); res.Message != "1 track preset: Mine/Drum Kit." {
		t.Errorf("filter = %q", res.Message)
	}
	if res, _ := TrackPresets("strings"); res.Message != `No track presets match "strings".` {
		t.Errorf("no match = %q", res.Message)
	}
}

func TestPickTrackPreset(t *testing.T) {
	useTrackPresets(t, "Mine/Album/Bass", "Mine/Demo/Bass", "Mine/Drum Kit", "Mine/Kit")
	tests := []struct {
		said, want, problem, hint string
	}{
		{said: "Mine/Album/Bass", want: "Mine/Album/Bass"},
		{said: "album/bass", want: "Mine/Album/Bass"},
		{said: " Drum Kit.trackpreset ", want: "Mine/Drum Kit"},
		{said: "kit", want: "Mine/Kit"},
		{said: "Bass", problem: `2 track presets are called "Bass"`, hint: "name its folder too: Mine/Album/Bass, Mine/Demo/Bass"},
		{said: "Bas", problem: `no track preset called "Bas"`, hint: "close: Mine/Album/Bass, Mine/Demo/Bass"},
		{said: "Strings", problem: `no track preset called "Strings"`, hint: "list them with: ucdaw trackpreset list"},
		{said: "../Bass", problem: "name a track preset"},
		{said: "  ", problem: "name a track preset"},
	}
	for _, tt := range tests {
		got, err := pickTrackPreset(tt.said)
		if tt.problem == "" {
			if err != nil || got != tt.want {
				t.Errorf("%q = %q, %v; want %q", tt.said, got, err, tt.want)
			}
			continue
		}
		p, ok := err.(Problem)
		if !ok || p.Message != tt.problem || (tt.hint != "" && p.Hint != tt.hint) {
			t.Errorf("%q = %q, %+v; want problem %q (%s)", tt.said, got, err, tt.problem, tt.hint)
		}
	}
}

func quickSettle(t *testing.T) {
	t.Helper()
	origSettle, origWait, origQuiet := commandSettle, trackPresetWait, trackPresetQuiet
	t.Cleanup(func() { commandSettle, trackPresetWait, trackPresetQuiet = origSettle, origWait, origQuiet })
	commandSettle, trackPresetWait, trackPresetQuiet = 20*time.Millisecond, 300*time.Millisecond, 150*time.Millisecond
}

func TestLoadTrackPresetReportsAddedChannels(t *testing.T) {
	useTrackPresets(t, "Mine/Album/Bass", "Mine/Demo/Bass")
	quickSettle(t)
	st, fake := newStudio(t)
	var sent map[string]string
	fake.onInvoke = func(category, name string, args map[string]string) []proto.Frame {
		if category+"|"+name != "Track|Load Track Preset" {
			return nil
		}
		sent = args
		return []proto.Frame{
			pv("mixer/sub/count", 0.001),
			ps("mixer/sub/ch1/label", "Bass"),
			ps("mixer/sub/ch1/type", "AudioGroup"),
			pv("mixer/channel/count", 0.002),
			ps("mixer/channel/ch1/label", "Bass"),
			ps("mixer/channel/ch1/type", "AudioTrack"),
			ps("mixer/channel/ch2/label", "Bass DI"),
			ps("mixer/channel/ch2/type", "AudioTrack"),
		}
	}
	got := do(t, st, Request{Op: "load_track_preset", Name: "album/bass"})
	want := "Loaded track preset Mine/Album/Bass: added 3 channels: Bass (audio), Bass DI (audio), Bass (bus)."
	if got != want {
		t.Errorf("load = %q", got)
	}
	if sent["Name"] != "Mine/Album/Bass" {
		t.Errorf("args = %v", sent)
	}
}

func TestLoadTrackPresetProblems(t *testing.T) {
	useTrackPresets(t, "Mine/Album/Bass", "Mine/Demo/Bass")
	quickSettle(t)
	st, fake := newStudio(t)
	invoked := false
	fake.onInvoke = func(string, string, map[string]string) []proto.Frame {
		invoked = true
		return nil
	}
	_, err := st.Do(context.Background(), Request{Op: "load_track_preset", Name: "Bass"})
	if p, ok := err.(Problem); !ok || !strings.Contains(p.Hint, "Mine/Demo/Bass") {
		t.Errorf("ambiguous = %v", err)
	}
	if invoked {
		t.Error("an ambiguous name reached Studio Pro")
	}
	_, err = st.Do(context.Background(), Request{Op: "load_track_preset", Name: "Demo/Bass"})
	if err == nil || !strings.Contains(err.Error(), "Track preset Mine/Demo/Bass was sent, but Studio Pro added no channels") {
		t.Errorf("silent = %v", err)
	}
	if !invoked {
		t.Error("the preset was never sent")
	}
}

// Studio Pro adds a preset's channels one at a time as their plug-ins load,
// so the answer waits for the late ones.
func TestLoadTrackPresetWaitsForLateChannels(t *testing.T) {
	useTrackPresets(t, "Mine/Drum Kit")
	quickSettle(t)
	st, fake := newStudio(t)
	fake.onInvoke = func(category, name string, _ map[string]string) []proto.Frame {
		if category+"|"+name != "Track|Load Track Preset" {
			return nil
		}
		go func() {
			time.Sleep(60 * time.Millisecond)
			fake.frames <- pv("mixer/channel/count", 0.002)
			fake.frames <- ps("mixer/channel/ch2/label", "Snare")
			fake.frames <- ps("mixer/channel/ch2/type", "AudioTrack")
		}()
		return []proto.Frame{
			pv("mixer/channel/count", 0.001),
			ps("mixer/channel/ch1/label", "Kick"),
			ps("mixer/channel/ch1/type", "AudioTrack"),
		}
	}
	got := do(t, st, Request{Op: "load_track_preset", Name: "drum kit"})
	if got != "Loaded track preset Mine/Drum Kit: added 2 channels: Kick (audio), Snare (audio)." {
		t.Errorf("load = %q", got)
	}
}
