package dawcli

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/steveclarke/ucmix/internal/daw"
)

type fakeEngine struct {
	got []daw.Request
	res daw.Result
	err error
}

func (f *fakeEngine) Do(_ context.Context, req daw.Request) (daw.Result, error) {
	f.got = append(f.got, req)
	return f.res, f.err
}

func execute(t *testing.T, engine *fakeEngine, args ...string) (string, error) {
	t.Helper()
	orig := newEngine
	t.Cleanup(func() { newEngine = orig })
	newEngine = func(*options) (daw.Engine, error) { return engine, nil }
	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs(args)
	root.SetContext(context.Background())
	err := root.Execute()
	return out.String(), err
}

func TestCommandsBuildRequests(t *testing.T) {
	tests := []struct {
		args []string
		want daw.Request
	}{
		{[]string{"transport", "play", "--bars", "4"}, daw.Request{Op: "transport", Action: "play", Bars: 4}},
		{[]string{"transport", "play", "--for", "2s"}, daw.Request{Op: "transport", Action: "play", Seconds: 2}},
		{[]string{"transport", "stop"}, daw.Request{Op: "transport", Action: "stop"}},
		{[]string{"transport", "rtz"}, daw.Request{Op: "transport", Action: "rtz"}},
		{[]string{"transport", "loop", "on"}, daw.Request{Op: "transport", Action: "loop", Value: "on"}},
		{[]string{"click", "on"}, daw.Request{Op: "click", Action: "on"}},
		{[]string{"click"}, daw.Request{Op: "click", Action: "status"}},
		{[]string{"tempo", "96"}, daw.Request{Op: "tempo", BPM: 96}},
		{[]string{"tempo"}, daw.Request{Op: "tempo"}},
		{[]string{"song", "new"}, daw.Request{Op: "new_song"}},
		{[]string{"song", "new", "--tempo", "72", "--click", "on"}, daw.Request{Op: "new_song", BPM: 72, Click: "on"}},
		{[]string{"param", "get", "transport/tempo"}, daw.Request{Op: "get", Path: "transport/tempo"}},
		{[]string{"param", "set", "mixer/channel/ch1/label", "Lead", "Vox"}, daw.Request{Op: "set", Path: "mixer/channel/ch1/label", Value: "Lead Vox"}},
		{[]string{"state"}, daw.Request{Op: "song"}},
		{[]string{"state", "transport"}, daw.Request{Op: "dump", Filter: "transport"}},
		{[]string{"command", "list", "zoom"}, daw.Request{Op: "commands", Filter: "zoom"}},
		{[]string{"command", "run", "Track", "Add Audio Track (mono)"}, daw.Request{Op: "run", Category: "Track", Name: "Add Audio Track (mono)"}},
		{[]string{"track", "add", "--name", "Guitar"}, daw.Request{Op: "add_track", Name: "Guitar"}},
		{[]string{"track", "add", "stereo"}, daw.Request{Op: "add_track", Action: "stereo"}},
		{[]string{"insert", "Guitar", "Guitar Rig 7", "--preset", "Clean"}, daw.Request{Op: "insert", Path: "Guitar", Name: "Guitar Rig 7", Value: "Clean"}},
		{[]string{"command", "run", "Edit > Undo"}, daw.Request{Op: "run", Category: "Edit > Undo"}},
		{[]string{"import", "/stems"}, daw.Request{Op: "import", Files: []string{"/stems"}, At: 1}},
		{[]string{"import", "/a.wav", "/b/../b.aif", "--onto", "Vox", "--at", "17"}, daw.Request{Op: "import", Files: []string{"/a.wav", "/b.aif"}, Onto: "Vox", At: 17}},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			engine := &fakeEngine{res: daw.Result{Message: "ok"}}
			out, err := execute(t, engine, tt.args...)
			if err != nil {
				t.Fatal(err)
			}
			if len(engine.got) != 1 || !reflect.DeepEqual(engine.got[0], tt.want) {
				t.Errorf("request = %+v, want %+v", engine.got, tt.want)
			}
			if out != "ok\n" {
				t.Errorf("out = %q", out)
			}
		})
	}
}

func TestJSONOutput(t *testing.T) {
	engine := &fakeEngine{res: daw.Result{Message: "Click on.", Data: map[string]bool{"on": true}}}
	out, err := execute(t, engine, "click", "on", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"message": "Click on."`) || !strings.Contains(out, `"on": true`) {
		t.Errorf("out = %s", out)
	}
}

func TestListRowsPrint(t *testing.T) {
	engine := &fakeEngine{res: daw.Result{
		Message: "2 Studio Pro commands match \"undo\".",
		Data:    []daw.Command{{Category: "Edit", Name: "Undo"}, {Category: "Zoom", Name: "Undo Zoom"}},
	}}
	out, err := execute(t, engine, "command", "list", "undo")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "  Edit  Undo\n") || !strings.Contains(out, "Zoom  Undo Zoom") {
		t.Errorf("out = %q", out)
	}
}

func TestSongNewRejectsBadFlags(t *testing.T) {
	for _, args := range [][]string{{"song", "new", "--click", "loud"}, {"song", "new", "--tempo", "-5"}} {
		engine := &fakeEngine{}
		if _, err := execute(t, engine, args...); err == nil || len(engine.got) != 0 {
			t.Errorf("%v: err = %v, sent %v", args, err, engine.got)
		}
	}
}

func TestBadTempoIsPlain(t *testing.T) {
	_, err := execute(t, &fakeEngine{}, "tempo", "fast")
	if err == nil || err.Error() != `tempo needs a number of bpm, not "fast"` {
		t.Errorf("err = %v", err)
	}
}

func TestProblemPrintsHint(t *testing.T) {
	var buf bytes.Buffer
	printError(&buf, daw.NotFound)
	if !strings.HasPrefix(buf.String(), daw.NotFound.Message+"\n  ") {
		t.Errorf("printed %q", buf.String())
	}
}

func TestCommandArgs(t *testing.T) {
	got, err := commandArgs([]string{"Name=Chorus", "Number="})
	if err != nil || got["Name"] != "Chorus" || got["Number"] != "" || len(got) != 2 {
		t.Fatalf("commandArgs = %v, %v", got, err)
	}
	if _, err := commandArgs([]string{"nonsense"}); err == nil {
		t.Fatal("want an error for an argument without =")
	}
	if got, err := commandArgs(nil); got != nil || err != nil {
		t.Fatalf("no flags = %v, %v", got, err)
	}
}

func TestSplitPlacement(t *testing.T) {
	s, w, tg, err := splitPlacement(strings.Fields("chorus one after the bridge"), "after")
	if err != nil || s != "chorus one" || w != "after" || tg != "the bridge" {
		t.Errorf("got %q %q %q %v", s, w, tg, err)
	}
	s, w, tg, err = splitPlacement([]string{"Bridge", "before", "Chorus 2"}, "")
	if err != nil || s != "Bridge" || w != "before" || tg != "Chorus 2" {
		t.Errorf("got %q %q %q %v", s, w, tg, err)
	}
	if _, _, _, err := splitPlacement([]string{"Bridge", "Chorus"}, ""); err == nil {
		t.Error("missing before/after accepted")
	}
}

func TestFaderRequest(t *testing.T) {
	o := &options{}
	cases := map[string]daw.Request{
		"Pad":             {Op: "fader", Path: "Pad"},
		"Pad -6":          {Op: "fader", Path: "Pad", Action: "set", Value: "-6"},
		"Pad --down 3":    {Op: "fader", Path: "Pad", Action: "down", Value: "3"},
		"Pad --up=1.5":    {Op: "fader", Path: "Pad", Action: "up", Value: "1.5"},
		"Pad -inf --json": {Op: "fader", Path: "Pad", Action: "set", Value: "-inf"},
	}
	for in, want := range cases {
		got, help, err := faderRequest(strings.Fields(in), o)
		if err != nil || help || !reflect.DeepEqual(got, want) {
			t.Errorf("%q = %+v, %v, %v", in, got, help, err)
		}
	}
	if !o.json {
		t.Error("--json not applied")
	}
	for _, bad := range []string{"", "Pad -6 --down 3", "Pad --up", "a b c"} {
		if _, _, err := faderRequest(strings.Fields(bad), &options{}); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
