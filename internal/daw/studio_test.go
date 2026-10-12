package daw

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/steveclarke/ucmix/internal/dawschema"
	"github.com/steveclarke/ucmix/internal/proto"
)

func miniSchema(t *testing.T) *dawschema.Catalog {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "dawschema", "testdata", "mini.xml"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	c, err := dawschema.Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func newStudio(t *testing.T) (*Studio, *fakeStudio) {
	t.Helper()
	commands := `{"id": "AdviseCommands","categories": [` +
		`{"displayName": "Transport","commands": [{"displayName": "Click","name": "Click","category": "Transport","englishName": "Click"}]},` +
		`{"displayName": "View","commands": [{"displayName": "Zoom Full","name": "Zoom Full","category": "Zoom","englishName": "Zoom Full"},` +
		`{"displayName": "Mute","name": "Mute","category": "View","englishName": "Mute"}]},` +
		`{"displayName": "Track","commands": [{"displayName": "Add Audio Track (mono)","name": "Add Audio Track (mono)","category": "Track","englishName": "Add Audio Track (mono)"},` +
		`{"displayName": "Mute","name": "Mute","category": "Track","englishName": "Mute"}]}]}`
	sync := `{"id": "Synchronize","children": {` +
		`"transport": {"values": {"start": 0.0,"stop": 1.0,"loop": 0.0,"tempo": 0.28205128205128205,"timeSignatureNum": 0.0967741935483871,"timeSignatureDenom": 0.5}},` +
		`"metronome": {"values": {"clickOn": 1.0}},` +
		`"mixer": {"children": {"channel": {"values": {"count": 0.0}}}}}}`
	fake := &fakeStudio{
		frames:   make(chan proto.Frame, 256),
		echo:     true,
		snapshot: append(ckFrames(t, commands, 64), zm(t, sync)),
		onCommand: map[string][]proto.Frame{
			"Track|Add Audio Track (mono)": {
				pv("mixer/channel/count", 0.001),
				ps("mixer/channel/ch1/label", "Track 1"),
				ps("mixer/channel/ch1/type", "AudioTrack"),
				ps("mixer/channel/ch1/speakerFormat", "Mono"),
			},
		},
	}
	orig := commandSettle
	commandSettle = 20 * time.Millisecond
	t.Cleanup(func() { commandSettle = orig })
	s := dialFake(t, fake)
	return NewStudio(s, miniSchema(t)), fake
}

func do(t *testing.T, st *Studio, req Request) string {
	t.Helper()
	res, err := st.Do(context.Background(), req)
	if err != nil {
		t.Fatalf("%+v: %v", req, err)
	}
	return res.Message
}

func TestClickReadsBackAndIsIdempotent(t *testing.T) {
	st, _ := newStudio(t)
	if got := do(t, st, Request{Op: "click", Action: "on"}); got != "Click is already on." {
		t.Errorf("click on = %q", got)
	}
	if got := do(t, st, Request{Op: "click", Action: "off"}); got != "Click off." {
		t.Errorf("click off = %q", got)
	}
	if v, _ := st.model().Num("metronome/clickOn"); v != 0 {
		t.Errorf("clickOn = %v", v)
	}
}

func TestTempoConvertsAndReportsPrevious(t *testing.T) {
	st, _ := newStudio(t)
	if got := do(t, st, Request{Op: "tempo"}); got != "Tempo is 120 bpm." {
		t.Errorf("tempo = %q", got)
	}
	if got := do(t, st, Request{Op: "tempo", BPM: 96}); got != "Tempo 96 bpm (was 120)." {
		t.Errorf("tempo 96 = %q", got)
	}
	if _, err := st.Do(context.Background(), Request{Op: "tempo", BPM: 900}); err == nil {
		t.Error("tempo 900 should fail")
	}
}

func TestPlayForBarsStopsAfterward(t *testing.T) {
	st, fake := newStudio(t)
	got := do(t, st, Request{Op: "transport", Action: "play", Bars: 4})
	if got != "Played 4 bars at 120 bpm, then stopped." {
		t.Errorf("play = %q", got)
	}
	if st.playing() {
		t.Error("still playing")
	}
	if len(fake.batches) != 2 {
		t.Errorf("gestures = %d, want play and stop", len(fake.batches))
	}
}

func TestPlayRefusesLongTimedBursts(t *testing.T) {
	st, _ := newStudio(t)
	if _, err := st.Do(context.Background(), Request{Op: "transport", Action: "play", Seconds: 600}); err == nil {
		t.Error("want error")
	}
}

func TestLoopNeedsALoopRange(t *testing.T) {
	st, _ := newStudio(t)
	_, err := st.Do(context.Background(), Request{Op: "transport", Action: "loop", Value: "on"})
	var p Problem
	if !errors.As(err, &p) || !strings.Contains(p.Message, "no loop range") {
		t.Errorf("err = %v", err)
	}
}

func TestStopPressesAgainWhenFirstStopIsIgnored(t *testing.T) {
	st, fake := newStudio(t)
	orig := stopWait
	stopWait = 50 * time.Millisecond
	t.Cleanup(func() { stopWait = orig })
	fake.stubbornStops = 1
	if got := do(t, st, Request{Op: "transport", Action: "play", Seconds: 0.01}); !strings.HasPrefix(got, "Played") {
		t.Errorf("play = %q", got)
	}
	if st.playing() {
		t.Error("still playing")
	}
}

func TestLoopTogglesToTarget(t *testing.T) {
	st, fake := newStudio(t)
	fake.loopRange = true
	if got := do(t, st, Request{Op: "transport", Action: "loop", Value: "on"}); got != "Loop on." {
		t.Errorf("loop on = %q", got)
	}
	if got := do(t, st, Request{Op: "transport", Action: "loop", Value: "on"}); got != "Loop is already on." {
		t.Errorf("loop on again = %q", got)
	}
	if got := do(t, st, Request{Op: "transport", Action: "loop", Value: "off"}); got != "Loop off." {
		t.Errorf("loop off = %q", got)
	}
}

func TestRunCommandReportsAddedTrack(t *testing.T) {
	st, _ := newStudio(t)
	got := do(t, st, Request{Op: "run", Category: "track", Name: "add audio track (mono)"})
	if got != "Ran Track > Add Audio Track (mono): added Track 1 (audio)." {
		t.Errorf("run = %q", got)
	}
	if song := do(t, st, Request{Op: "song"}); !strings.Contains(song, "1 channel: Track 1 (audio, mono)") {
		t.Errorf("song = %q", song)
	}
}

func TestRunCommandWithoutFeedbackSaysSo(t *testing.T) {
	st, _ := newStudio(t)
	got := do(t, st, Request{Op: "run", Name: "Zoom Full"})
	if !strings.Contains(got, "reported no change") {
		t.Errorf("run = %q", got)
	}
}

func TestFindCommandNeedsCategoryWhenAmbiguous(t *testing.T) {
	st, _ := newStudio(t)
	_, err := FindCommand(st.model().Commands(), "", "Mute")
	var p Problem
	if !errors.As(err, &p) || !strings.Contains(p.Message, "more than one category") {
		t.Errorf("err = %v", err)
	}
	if c, err := FindCommand(st.model().Commands(), "Track|Mute", ""); err != nil || c.Category != "Track" {
		t.Errorf("Track|Mute = %+v, %v", c, err)
	}
}

func TestSetValidatesAgainstSchema(t *testing.T) {
	st, _ := newStudio(t)
	if _, err := st.Do(context.Background(), Request{Op: "set", Path: "mixer/channel/count", Value: "3"}); err == nil ||
		!strings.Contains(err.Error(), "read-only") {
		t.Errorf("read-only set = %v", err)
	}
	if _, err := st.Do(context.Background(), Request{Op: "set", Path: "no/such", Value: "1"}); err == nil {
		t.Error("unknown path should fail")
	}
	if got := do(t, st, Request{Op: "set", Path: "transport/timeSignatureDenom", Value: "8"}); got != "transport/timeSignatureDenom: 8 (was 4)." {
		t.Errorf("set denom = %q", got)
	}
}

func TestSongSummaryEmpty(t *testing.T) {
	st, _ := newStudio(t)
	if got := do(t, st, Request{Op: "song"}); got != "120 bpm, 4/4, click on, loop off, stopped; no tracks yet." {
		t.Errorf("song = %q", got)
	}
}

func TestDumpListsLiveValues(t *testing.T) {
	st, _ := newStudio(t)
	res, err := st.Do(context.Background(), Request{Op: "dump", Filter: "transport"})
	if err != nil {
		t.Fatal(err)
	}
	values := res.Data.([]Value)
	found := false
	for _, v := range values {
		if v.Path == "transport/tempo" && v.Value == "120 bpm" {
			found = true
		}
	}
	if !found || !strings.HasSuffix(res.Message, "under transport.") {
		t.Errorf("dump = %q %+v", res.Message, values)
	}
}
