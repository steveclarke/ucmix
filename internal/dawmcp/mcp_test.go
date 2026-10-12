package dawmcp

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/steveclarke/ucmix/internal/daw"
)

type fakeEngine struct {
	mu  sync.Mutex
	got []daw.Request
	res daw.Result
	err error
	// do, when set, runs inside Do (after the request is recorded).
	do func(daw.Request)
}

func (f *fakeEngine) Do(_ context.Context, req daw.Request) (daw.Result, error) {
	f.mu.Lock()
	f.got = append(f.got, req)
	do := f.do
	f.mu.Unlock()
	if do != nil {
		do(req)
	}
	return f.res, f.err
}

func connect(t *testing.T, engine *fakeEngine) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	server := NewServer(func() (daw.Engine, error) { return engine, nil }, "test")
	st, ct := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func text(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) != 1 {
		t.Fatalf("content = %+v", res.Content)
	}
	return res.Content[0].(*mcp.TextContent).Text
}

func TestToolsMapToRequests(t *testing.T) {
	tests := []struct {
		tool string
		args map[string]any
		want daw.Request
	}{
		{"transport", map[string]any{"action": "play", "bars": 4}, daw.Request{Op: "transport", Action: "play", Bars: 4}},
		{"transport", map[string]any{"action": "loop_on"}, daw.Request{Op: "transport", Action: "loop", Value: "on"}},
		{"click", map[string]any{"state": "on"}, daw.Request{Op: "click", Action: "on"}},
		{"click", map[string]any{}, daw.Request{Op: "click", Action: "status"}},
		{"tempo", map[string]any{"bpm": 96}, daw.Request{Op: "tempo", BPM: 96}},
		{"new_song", map[string]any{}, daw.Request{Op: "new_song"}},
		{"new_song", map[string]any{"tempo": 72, "click": "on"}, daw.Request{Op: "new_song", BPM: 72, Click: "on"}},
		{"get_song", map[string]any{}, daw.Request{Op: "song"}},
		{"get_values", map[string]any{"prefix": "transport"}, daw.Request{Op: "dump", Filter: "transport"}},
		{"get_param", map[string]any{"path": "transport/tempo"}, daw.Request{Op: "get", Path: "transport/tempo"}},
		{"set_param", map[string]any{"path": "mixer/channel/ch1/mute", "value": "on"}, daw.Request{Op: "set", Path: "mixer/channel/ch1/mute", Value: "on"}},
		{"list_params", map[string]any{"filter": "mute"}, daw.Request{Op: "params", Filter: "mute"}},
		{"list_commands", map[string]any{"filter": "undo"}, daw.Request{Op: "commands", Filter: "undo"}},
		{"add_track", map[string]any{"name": "Guitar"}, daw.Request{Op: "add_track", Name: "Guitar"}},
		{"add_insert", map[string]any{"channel": "Guitar", "plugin": "Guitar Rig 7", "preset": "Clean"}, daw.Request{Op: "insert", Path: "Guitar", Name: "Guitar Rig 7", Value: "Clean"}},
		{"list_plugins", map[string]any{"filter": "rig"}, daw.Request{Op: "plugins", Filter: "rig"}},
		{"run_command", map[string]any{"category": "Edit", "name": "Undo"}, daw.Request{Op: "run", Category: "Edit", Name: "Undo"}},
		{"run_command", map[string]any{"category": "Transport", "name": "Forward Bar", "args": map[string]any{"Bars": "4"}}, daw.Request{Op: "run", Category: "Transport", Name: "Forward Bar", Args: map[string]string{"Bars": "4"}}},
		{"add_track", map[string]any{"name": "Guitar", "plugin": "my clean guitar"}, daw.Request{Op: "add_track", Name: "Guitar", Plugin: "my clean guitar"}},
		{"add_instrument", map[string]any{"instrument": "Syntronik 2", "name": "Pad", "preset": "Warm"}, daw.Request{Op: "add_instrument", Plugin: "Syntronik 2", Name: "Pad", Preset: "Warm"}},
		{"fader", map[string]any{"track": "Pad", "change": -3}, daw.Request{Op: "fader", Path: "Pad", Action: "down", Value: "3"}},
		{"fader", map[string]any{"track": "Pad", "db": 0}, daw.Request{Op: "fader", Path: "Pad", Action: "set", Value: "0"}},
		{"fader", map[string]any{"track": "Pad"}, daw.Request{Op: "fader", Path: "Pad"}},
		{"audio_edit", map[string]any{"edit": "split", "track": "Guitar", "bar": 9}, daw.Request{Op: "audio", Action: "split", Path: "Guitar", Value: "9"}},
		{"import_audio", map[string]any{"paths": []string{"/stems"}}, daw.Request{Op: "import", Files: []string{"/stems"}}},
		{"import_audio", map[string]any{"paths": []string{"/v.wav"}, "onto": "Vox", "bar": 17}, daw.Request{Op: "import", Files: []string{"/v.wav"}, Onto: "Vox", At: 17}},
		{"section", map[string]any{"action": "repeat", "section": "Chorus 1", "target": "Bridge"}, daw.Request{Op: "section", Action: "repeat", Section: "Chorus 1", Target: "Bridge"}},
		{"section", map[string]any{"action": "add", "name": "Verse, Chorus", "bars": 4}, daw.Request{Op: "section", Action: "add", Name: "Verse, Chorus", Bars: 4}},
		{"trim", map[string]any{"track": "Bass", "db": 20}, daw.Request{Op: "trim", Path: "Bass", Action: "set", Value: "20"}},
		{"trim", map[string]any{"track": "Bass", "db": -6}, daw.Request{Op: "trim", Path: "Bass", Action: "set", Value: "-6"}},
		{"trim", map[string]any{"track": "Bass", "change": -2}, daw.Request{Op: "trim", Path: "Bass", Action: "down", Value: "2"}},
		{"trim", map[string]any{"track": "Bass"}, daw.Request{Op: "trim", Path: "Bass"}},
		{"gain_stage", map[string]any{}, daw.Request{Op: "gainstage"}},
		{"gain_stage", map[string]any{"tracks": []string{"Bass"}, "target": -20}, daw.Request{Op: "gainstage", Tracks: []string{"Bass"}, TargetDBFS: -20}},
		{"analyze_audio", map[string]any{"tracks": []string{"Bass"}}, daw.Request{Op: "analyze", Tracks: []string{"Bass"}}},
		{"analyze_audio", map[string]any{}, daw.Request{Op: "analyze"}},
		{"analyze_audio", map[string]any{"paths": []string{"/stems"}, "tracks": []string{"Bass"}}, daw.Request{Op: "analyze", Files: []string{"/stems"}, Tracks: []string{"Bass"}}},
	}
	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			engine := &fakeEngine{res: daw.Result{Message: "Done."}}
			cs := connect(t, engine)
			res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tt.tool, Arguments: tt.args})
			if err != nil {
				t.Fatal(err)
			}
			if res.IsError || text(t, res) != "Done." {
				t.Errorf("result = %q (error %v)", text(t, res), res.IsError)
			}
			if len(engine.got) != 1 || !reflect.DeepEqual(engine.got[0], tt.want) {
				t.Errorf("request = %+v, want %+v", engine.got, tt.want)
			}
		})
	}
}

func TestListToolsAppendJSON(t *testing.T) {
	engine := &fakeEngine{res: daw.Result{Message: "1 command.", Data: []daw.Command{{Category: "Edit", Name: "Undo"}}}}
	cs := connect(t, engine)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_commands", Arguments: map[string]any{"filter": "undo"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := text(t, res); got != "1 command.\n[{\"category\":\"Edit\",\"name\":\"Undo\"}]" {
		t.Errorf("text = %q", got)
	}
}

func TestProblemsBecomeToolErrorsWithNextStep(t *testing.T) {
	cs := connect(t, &fakeEngine{err: daw.NotFound})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_song", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || text(t, res) != daw.NotFound.Message+" Next: "+daw.NotFound.Hint+"." {
		t.Errorf("result = %q", text(t, res))
	}
}

func TestEveryToolHasADescription(t *testing.T) {
	cs := connect(t, &fakeEngine{})
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 24 {
		t.Errorf("tools = %d", len(tools.Tools))
	}
	for _, tool := range tools.Tools {
		if len(tool.Description) < 40 {
			t.Errorf("%s description too thin", tool.Name)
		}
	}
}

func TestFavoriteToolWorksWithoutStudioPro(t *testing.T) {
	t.Setenv("UCDAW_FAVORITES", t.TempDir()+"/favorites.json")
	engine := &fakeEngine{}
	cs := connect(t, engine)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "favorite", Arguments: map[string]any{"action": "list"}})
	if err != nil {
		t.Fatal(err)
	}
	if text(t, res) != "No favorites yet." || len(engine.got) != 0 {
		t.Errorf("favorite list = %q, engine calls %d", text(t, res), len(engine.got))
	}
}

func TestAnalyzeFilesWorksWithoutStudioPro(t *testing.T) {
	engine := &fakeEngine{}
	cs := connect(t, engine)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "analyze_audio", Arguments: map[string]any{"paths": []string{t.TempDir()}}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(text(t, res), "Nothing to measure") || len(engine.got) != 0 {
		t.Errorf("analyze = %q, engine calls %d", text(t, res), len(engine.got))
	}
}

func TestInstructionsCarryTheMixingWorkflow(t *testing.T) {
	cs := connect(t, &fakeEngine{})
	got := cs.InitializeResult().Instructions
	for _, want := range []string{"analyze_audio", "gain_stage", "never normalize", "-18", "ask"} {
		if !strings.Contains(got, want) {
			t.Errorf("instructions lack %q", want)
		}
	}
}

// MCP clients send tool calls in parallel and the SDK runs them at once.
// Selection-based edits (add a track, then rename the selected one) must not
// interleave, with or without ucdaw serve.
func TestToolCallsReachStudioProOneAtATime(t *testing.T) {
	var inside, most atomic.Int32
	engine := &fakeEngine{res: daw.Result{Message: "Done."}}
	engine.do = func(daw.Request) {
		n := inside.Add(1)
		for {
			m := most.Load()
			if n <= m || most.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		inside.Add(-1)
	}
	cs := connect(t, engine)
	calls := []*mcp.CallToolParams{
		{Name: "add_track", Arguments: map[string]any{"name": "Guitar"}},
		{Name: "add_insert", Arguments: map[string]any{"channel": "Guitar", "plugin": "Pro EQ"}},
		{Name: "add_instrument", Arguments: map[string]any{"instrument": "Mai Tai"}},
		{Name: "audio_edit", Arguments: map[string]any{"edit": "split", "track": "Guitar", "bar": 9}},
		{Name: "section", Arguments: map[string]any{"action": "delete", "section": "Verse"}},
		{Name: "get_song", Arguments: map[string]any{}},
	}
	var wg sync.WaitGroup
	for _, c := range calls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := cs.CallTool(context.Background(), c); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if got := most.Load(); got != 1 {
		t.Errorf("%d calls ran at once, want 1", got)
	}
	if len(engine.got) != len(calls) {
		t.Errorf("calls = %d, want %d", len(engine.got), len(calls))
	}
}

// Stop must never wait behind a timed play, the way ucdaw serve exempts it.
func TestStopDoesNotWaitBehindAPlay(t *testing.T) {
	release := make(chan struct{})
	playing := make(chan struct{})
	engine := &fakeEngine{res: daw.Result{Message: "Done."}}
	engine.do = func(req daw.Request) {
		if req.Action == "play" {
			close(playing)
			<-release
		}
	}
	cs := connect(t, engine)
	played := make(chan struct{})
	go func() {
		defer close(played)
		_, _ = cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "transport", Arguments: map[string]any{"action": "play", "seconds": 30}})
	}()
	<-playing
	stopped := make(chan error, 1)
	go func() {
		_, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "transport", Arguments: map[string]any{"action": "stop"}})
		stopped <- err
	}()
	select {
	case err := <-stopped:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(2 * time.Second):
		t.Error("stop waited behind the play")
	}
	close(release)
	<-played
}
