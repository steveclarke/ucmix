// Package dawmcp exposes ucdaw to Claude as an MCP server: tools a musician
// would name (transport, click, tempo, the song, parameters, Studio Pro's own
// commands), each answering in one plain line after Studio Pro confirms.
package dawmcp

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/steveclarke/ucmix/internal/daw"
)

// EngineFunc returns the engine for one call: the serve socket when it runs,
// otherwise a direct connection.
type EngineFunc func() (daw.Engine, error)

type transportIn struct {
	Action  string  `json:"action" jsonschema:"one of: play, stop, rtz (return to the start), loop_on, loop_off"`
	Seconds float64 `json:"seconds,omitempty" jsonschema:"play only: stop again after this many seconds (max 60)"`
	Bars    float64 `json:"bars,omitempty" jsonschema:"play only: stop again after this many bars at the song's tempo and meter"`
}

type clickIn struct {
	State string `json:"state,omitempty" jsonschema:"on, off or toggle; leave empty to read whether the click is on"`
}

type tempoIn struct {
	BPM float64 `json:"bpm,omitempty" jsonschema:"new tempo in beats per minute (10 to 400); leave empty to read the tempo"`
}

type pathIn struct {
	Path string `json:"path" jsonschema:"parameter path, e.g. transport/tempo or mixer/channel/ch2/volume"`
}

type setIn struct {
	Path  string `json:"path" jsonschema:"parameter path from list_params with <n> filled in, e.g. mixer/channel/ch2/mute"`
	Value string `json:"value" jsonschema:"value in the parameter's own units: on/off for switches, a number for ranges (volume is fader travel 0-100), a list entry, or text for names"`
}

type filterIn struct {
	Filter string `json:"filter,omitempty" jsonschema:"case-insensitive text to match; empty lists everything"`
}

type commandIn struct {
	Category string `json:"category" jsonschema:"command category exactly as list_commands shows it, e.g. Track"`
	Name     string `json:"name" jsonschema:"command name exactly as list_commands shows it, e.g. Add Audio Track (mono)"`
}

type trackIn struct {
	Format string `json:"format,omitempty" jsonschema:"mono (default) or stereo"`
	Name   string `json:"name,omitempty" jsonschema:"name for the new track, e.g. Guitar"`
}

type insertIn struct {
	Channel string `json:"channel" jsonschema:"track name (Guitar), number (2) or path (mixer/channel/ch2)"`
	Plugin  string `json:"plugin" jsonschema:"plug-in name as list_plugins shows it, e.g. Guitar Rig 7 or Pro EQ"`
	Preset  string `json:"preset,omitempty" jsonschema:"one of the plug-in's Studio Pro presets by name; a subfolder goes first (Misc/50 Hz Removal); empty for default settings"`
}

type valuesIn struct {
	Prefix string `json:"prefix,omitempty" jsonschema:"path prefix, e.g. mixer/channel/ch1 or transport; empty returns the whole song"`
}

// NewServer builds the MCP server.
func NewServer(engine EngineFunc, version string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "studio-pro-ucnet", Title: "Studio Pro (UCNET)", Version: version}, &mcp.ServerOptions{
		Instructions: "Drives the Fender Studio Pro song that is open on this network, the way the Studio Pro Remote app does. " +
			"Every change is read back from Studio Pro before the tool answers, and each answer is one line to pass on to the musician as is. " +
			"Start with get_song to see tracks and tempo. add_track and add_insert build a track with its plug-ins. For anything without its own tool (undo, zoom, editing), " +
			"find the command with list_commands and run it with run_command; most song changes undo with run_command Edit / Undo.",
	})
	call := func(ctx context.Context, req daw.Request, withData bool) (*mcp.CallToolResult, any, error) {
		return run(ctx, engine, req, withData)
	}

	mcp.AddTool(s, &mcp.Tool{
		Name: "transport",
		Description: "Start or stop playback, return to the start of the song, or turn the loop on or off. " +
			"Use play with seconds or bars to audition a short stretch: it stops by itself afterwards. " +
			"Plain play keeps playing until you call stop. Returns one line such as \"Played 4 bars at 120 bpm, then stopped.\"",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in transportIn) (*mcp.CallToolResult, any, error) {
		req := daw.Request{Op: "transport", Action: in.Action, Seconds: in.Seconds, Bars: in.Bars}
		switch in.Action {
		case "loop_on":
			req.Action, req.Value = "loop", "on"
		case "loop_off":
			req.Action, req.Value = "loop", "off"
		}
		return call(ctx, req, false)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "click",
		Description: "Turn the metronome click on or off, or read it. Returns one line such as \"Click on.\" or \"Click is already on.\"",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in clickIn) (*mcp.CallToolResult, any, error) {
		action := in.State
		if action == "" {
			action = "status"
		}
		return call(ctx, daw.Request{Op: "click", Action: action}, false)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "tempo",
		Description: "Read the song tempo, or set it in bpm. Returns one line such as \"Tempo 96 bpm (was 120).\"",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in tempoIn) (*mcp.CallToolResult, any, error) {
		return call(ctx, daw.Request{Op: "tempo", BPM: in.BPM}, false)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "get_song",
		Description: "Read the live song: tempo, time signature, click, loop, whether it is playing, and every channel " +
			"(name, kind, mono/stereo, fader, mute/solo, plug-ins). Returns a one-line summary followed by JSON detail; " +
			"channel paths in the JSON (mixer/channel/ch1) are what get_param and set_param take.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		return call(ctx, daw.Request{Op: "song"}, true)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_values",
		Description: "List every live value Studio Pro reports under a path prefix, formatted for reading. Use it to inspect one channel in depth (inserts, sends, pan) after get_song.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in valuesIn) (*mcp.CallToolResult, any, error) {
		return call(ctx, daw.Request{Op: "dump", Filter: in.Prefix}, true)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_param",
		Description: "Read one parameter by path. Returns one line such as \"Guitar volume: 76%.\"",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in pathIn) (*mcp.CallToolResult, any, error) {
		return call(ctx, daw.Request{Op: "get", Path: in.Path}, false)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "set_param",
		Description: "Change one parameter by path (mute, solo, fader, pan, track name, record mode and so on) and confirm " +
			"Studio Pro took it. Paths come from get_song and list_params. Returns one line such as \"Guitar mute: on (was off).\"",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in setIn) (*mcp.CallToolResult, any, error) {
		return call(ctx, daw.Request{Op: "set", Path: in.Path, Value: in.Value}, false)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "list_params",
		Description: "Search the parameters Studio Pro exposes to remote control, one entry per pattern " +
			"(mixer/channel/ch<n>/volume) with type, range and whether it is read-only. Use it to find a path for set_param.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in filterIn) (*mcp.CallToolResult, any, error) {
		return call(ctx, daw.Request{Op: "params", Filter: in.Filter}, true)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "list_commands",
		Description: "Search Studio Pro's own command list (every menu item and key command: about 1,450 across 52 categories, " +
			"e.g. Track / Add Audio Track (mono), Edit / Undo, Zoom / Zoom Full). Always filter, e.g. \"add audio\" or \"undo\". " +
			"Returns matching category and name pairs for run_command.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in filterIn) (*mcp.CallToolResult, any, error) {
		return call(ctx, daw.Request{Op: "commands", Filter: in.Filter}, true)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "add_track",
		Description: "Add a mono or stereo audio track and name it. The new track ends up selected. " +
			"Returns one line such as \"Added mono audio track Guitar.\" Undo with run_command Edit / Undo.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in trackIn) (*mcp.CallToolResult, any, error) {
		return call(ctx, daw.Request{Op: "add_track", Action: in.Format, Name: in.Name}, false)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "add_insert",
		Description: "Put an effect plug-in on a channel, after its existing inserts, optionally loading one of its Studio Pro presets, " +
			"and confirm both. Returns one line such as \"Added Guitar Rig 7 to Guitar (insert 1) with preset Clean.\" " +
			"If the preset is not found the plug-in still goes in with default settings and the answer says so. Undo with run_command Edit / Undo.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in insertIn) (*mcp.CallToolResult, any, error) {
		return call(ctx, daw.Request{Op: "insert", Path: in.Channel, Name: in.Plugin, Value: in.Preset}, false)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_plugins",
		Description: "Search the effects and instruments Studio Pro has scanned on this Mac by name or vendor. Use it to find the exact name for add_insert.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in filterIn) (*mcp.CallToolResult, any, error) {
		return call(ctx, daw.Request{Op: "plugins", Filter: in.Filter}, true)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "run_command",
		Description: "Run one Studio Pro command by category and name, as the Remote app's Commands page does, then report " +
			"what changed in the song (\"added Track 1 (audio)\"). View and editing commands give no report back; the answer says so. " +
			"Commands act on the current selection, like the menu items they are. Undo with category Edit, name Undo.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in commandIn) (*mcp.CallToolResult, any, error) {
		return call(ctx, daw.Request{Op: "run", Category: in.Category, Name: in.Name}, false)
	})
	return s
}

func run(ctx context.Context, engine EngineFunc, req daw.Request, withData bool) (*mcp.CallToolResult, any, error) {
	e, err := engine()
	if err != nil {
		return failure(err), nil, nil
	}
	res, err := e.Do(ctx, req)
	if err != nil {
		return failure(err), nil, nil
	}
	text := res.Message
	if withData && res.Data != nil {
		if data, err := json.Marshal(res.Data); err == nil {
			text += "\n" + string(data)
		}
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil, nil
}

func failure(err error) *mcp.CallToolResult {
	text := err.Error()
	var p daw.Problem
	if errors.As(err, &p) {
		text = p.Sentence()
		if p.Hint != "" {
			text += " Next: " + p.Hint + "."
		}
	}
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}
