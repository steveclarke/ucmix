package dawmcp

import (
	"context"
	"reflect"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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
		{"audio_edit", map[string]any{"edit": "split", "track": "Guitar", "bar": 9}, daw.Request{Op: "audio", Action: "split", Path: "Guitar", Value: "9"}},
		{"section", map[string]any{"action": "repeat", "section": "Chorus 1", "target": "Bridge"}, daw.Request{Op: "section", Action: "repeat", Section: "Chorus 1", Target: "Bridge"}},
		{"section", map[string]any{"action": "add", "name": "Verse, Chorus", "bars": 4}, daw.Request{Op: "section", Action: "add", Name: "Verse, Chorus", Bars: 4}},
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
	if len(tools.Tools) != 15 {
		t.Errorf("tools = %d", len(tools.Tools))
	}
	for _, tool := range tools.Tools {
		if len(tool.Description) < 40 {
			t.Errorf("%s description too thin", tool.Name)
		}
	}
}
