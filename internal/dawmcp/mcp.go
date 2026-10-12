// Package dawmcp exposes ucdaw to Claude as an MCP server: tools a musician
// would name (transport, click, tempo, the song, parameters, Studio Pro's own
// commands), each answering in one plain line after Studio Pro confirms.
package dawmcp

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

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

type newSongIn struct {
	Tempo float64 `json:"tempo,omitempty" jsonschema:"optional tempo for the new song in bpm (10 to 400); leave empty to keep the new song's own"`
	Click string  `json:"click,omitempty" jsonschema:"optional: on or off; leave empty to keep the new song's own"`
}

type pathIn struct {
	Path string `json:"path" jsonschema:"parameter path, e.g. transport/tempo or mixer/channel/ch2/volume"`
}

type setIn struct {
	Path  string `json:"path" jsonschema:"parameter path from list_params with <n> filled in, e.g. mixer/channel/ch2/mute"`
	Value string `json:"value" jsonschema:"value in the parameter's own units: on/off for switches, a number for ranges (volume is dB, e.g. -6), a list entry, or text for names"`
}

type filterIn struct {
	Filter string `json:"filter,omitempty" jsonschema:"case-insensitive text to match; empty lists everything"`
}

type commandIn struct {
	Category string            `json:"category" jsonschema:"command category exactly as list_commands shows it, e.g. Track"`
	Name     string            `json:"name" jsonschema:"command name exactly as list_commands shows it, e.g. Add Audio Track (mono)"`
	Args     map[string]string `json:"args,omitempty" jsonschema:"optional command arguments as a Studio Pro macro carries them, e.g. {\"Bars\": \"4\"} for Transport / Forward Bar; a command that needs arguments and gets none opens a dialog, which ucdaw cancels"`
}

type faderIn struct {
	Track  string   `json:"track" jsonschema:"track name (Pad), number (2) or path"`
	DB     *float64 `json:"db,omitempty" jsonschema:"set the fader to this level in dB (0 is unity, +10 the top); leave empty to read it"`
	Change float64  `json:"change,omitempty" jsonschema:"move the fader by this many dB instead: -3 to bring it down 3 dB, 2 to raise it 2 dB"`
}

type trimIn struct {
	Track  string   `json:"track" jsonschema:"track name (Bass), number (2) or path"`
	DB     *float64 `json:"db,omitempty" jsonschema:"set the input trim to this many dB, -24 to +24 (0 is no change to the stem's level); leave empty to read it"`
	Change float64  `json:"change,omitempty" jsonschema:"move the trim by this many dB instead: 20 to raise a stem exported 20 dB too quiet, -3 to lower it"`
}

type analyzeIn struct {
	Paths  []string `json:"paths,omitempty" jsonschema:"full paths of WAV or AIFF stems or of a folder of stems (~ works); measured on this Mac without Studio Pro"`
	Tracks []string `json:"tracks,omitempty" jsonschema:"track names: measures the stem imported onto each track; leave paths and tracks empty to measure every audio track in the song"`
	Target float64  `json:"target,omitempty" jsonschema:"average level to suggest trims for, in dBFS (default -18)"`
}

type gainStageIn struct {
	Tracks []string `json:"tracks,omitempty" jsonschema:"track names to gain-stage; empty for every audio track"`
	Target float64  `json:"target,omitempty" jsonschema:"average level to bring each stem to, in dBFS (default -18)"`
}

type audioIn struct {
	Edit    string  `json:"edit" jsonschema:"one of: fade-out, fade-in, split, trim-start, trim-end, crossfade, quantize"`
	Track   string  `json:"track" jsonschema:"track name (Guitar), number (2) or path"`
	Bar     int     `json:"bar,omitempty" jsonschema:"split, trim-start, trim-end: the bar number (bar 1 is the song start)"`
	Bars    float64 `json:"bars,omitempty" jsonschema:"fade-out, fade-in: fade length in whole bars (default 1)"`
	Seconds float64 `json:"seconds,omitempty" jsonschema:"crossfade: fade length in seconds (default 0.01)"`
}

type importIn struct {
	Paths []string `json:"paths" jsonschema:"full paths of WAV or AIFF files, or of a folder of stems (~ works); a folder imports its audio files in name order"`
	Onto  string   `json:"onto,omitempty" jsonschema:"optional existing audio track to place the files on (they overlap there); leave empty for one new track per file"`
	Bar   int      `json:"bar,omitempty" jsonschema:"bar to place the clips at (default 1, the song start)"`
}

type sectionIn struct {
	Action  string  `json:"action" jsonschema:"one of: list, add, duplicate, repeat, move, delete, rename, goto, play"`
	Section string  `json:"section,omitempty" jsonschema:"the section to act on: its name (Chorus), number (3), or name and occurrence (Chorus 2, second chorus, last chorus)"`
	Where   string  `json:"where,omitempty" jsonschema:"move only: before or after"`
	Target  string  `json:"target,omitempty" jsonschema:"repeat and move: the section it goes after (repeat) or before/after (move)"`
	Name    string  `json:"name,omitempty" jsonschema:"rename: the new name; add: the new sections' names, comma-separated (Verse, Chorus, Bridge)"`
	Bars    float64 `json:"bars,omitempty" jsonschema:"add: length of each new section in bars (default 8); play: stop after this many bars"`
	Seconds float64 `json:"seconds,omitempty" jsonschema:"play only: stop after this many seconds"`
}

type trackIn struct {
	Format string `json:"format,omitempty" jsonschema:"mono (default) or stereo"`
	Name   string `json:"name,omitempty" jsonschema:"name for the new track, e.g. Guitar"`
	Plugin string `json:"plugin,omitempty" jsonschema:"optional effect plug-in or favorite to put on the new track, e.g. Guitar Rig 7 or my clean guitar"`
	Preset string `json:"preset,omitempty" jsonschema:"optional Studio Pro preset for plugin; a favorite brings its own"`
}

type instrumentIn struct {
	Instrument string `json:"instrument" jsonschema:"virtual instrument name as list_plugins shows it (Syntronik 2, MODO BASS 2) or a favorite (my worship synth)"`
	Name       string `json:"name,omitempty" jsonschema:"name for the new track, e.g. Pad"`
	Preset     string `json:"preset,omitempty" jsonschema:"optional Studio Pro preset for the instrument; a favorite brings its own"`
}

type presetsIn struct {
	Plugin string `json:"plugin" jsonschema:"plug-in or instrument name as list_plugins shows it, or a favorite"`
	Filter string `json:"filter,omitempty" jsonschema:"optional text the preset name must contain"`
}

type favoriteIn struct {
	Action string `json:"action" jsonschema:"list, set or remove"`
	Name   string `json:"name,omitempty" jsonschema:"set and remove: the musician's words for the sound, e.g. clean guitar or worship synth"`
	Plugin string `json:"plugin,omitempty" jsonschema:"set: the effect or instrument, as list_plugins shows it"`
	Preset string `json:"preset,omitempty" jsonschema:"set: the plug-in's Studio Pro preset (a subfolder goes first: Misc/50 Hz Removal)"`
}

type insertIn struct {
	Channel string `json:"channel" jsonschema:"track name (Guitar), number (2) or path (mixer/channel/ch2)"`
	Plugin  string `json:"plugin" jsonschema:"plug-in name as list_plugins shows it, e.g. Guitar Rig 7 or Pro EQ"`
	Preset  string `json:"preset,omitempty" jsonschema:"one of the plug-in's Studio Pro presets by name; a subfolder goes first (Misc/50 Hz Removal); empty for default settings"`
}

type valuesIn struct {
	Prefix string `json:"prefix,omitempty" jsonschema:"path prefix, e.g. mixer/channel/ch1 or transport; empty returns the whole song"`
}

// instructions tell Claude how to use the tools, including the mix
// engineer's workflow (Claude Desktop has no skills, so it lives here too;
// skills/ucdaw/mixing.md is the full version).
const instructions = "Drives the Fender Studio Pro song that is open on this network, the way the Studio Pro Remote app does. " +
	"Every change is read back from Studio Pro before the tool answers, and each answer is one line to pass on to the musician as is. " +
	"Start with get_song to see tracks, tempo and arranger sections. add_track and add_insert build a track with its plug-ins; section edits the arrangement. For anything without its own tool (undo, zoom, editing), " +
	"find the command with list_commands and run it with run_command; most song changes undo with run_command Edit / Undo.\n\n" +
	"Mixing: you are the mix engineer for a musician who cannot measure. " +
	"Always measure before touching a level: analyze_audio gives each stem's average (RMS over active audio), peak, LUFS, silence and clipping, and the input trim it needs. " +
	"Never guess fader cuts, never normalize to peak, and never reset or rebalance the musician's faders without asking. " +
	"A stem exported far too quiet or loud is fixed with trim (input gain, -24..+24 dB), not the fader; gain_stage sets every trim so stems average about -18 dBFS (0 VU) and leaves faders alone, so ask before running it on a mix they have balanced. " +
	"Then balance with fader from the foundation: drums, bass against the kick, the vocal or lead, then the rest. " +
	"Turn ear notes into small dB moves: \"a touch\" 1-2 dB, \"a bit\" 3 dB, \"a lot\" 6 dB; one change at a time, then ask how it sounds. " +
	"Report every change with its exact numbers (pass the tool's line on). Flag a silent or clipped stem as a broken export to re-export. " +
	"Compare with the original recording at matched LUFS, and judge bass on headphones or real speakers, not laptop speakers."

// NewServer builds the MCP server.
func NewServer(engine EngineFunc, version string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "studio-pro-ucnet", Title: "Studio Pro (UCNET)", Version: version}, &mcp.ServerOptions{
		Instructions: instructions,
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
		Name: "new_song",
		Description: "Start a new, empty song in Studio Pro (File > New with its New window's current settings), optionally at a tempo and with the click on or off, " +
			"and read it back. The song that was open stays open in its own tab; nothing is saved or closed. Only when the musician asks for a new song. " +
			"Returns one line such as \"Started a new song (2026-10-12 Stephen Clarke) at 72 bpm, click on.\"",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in newSongIn) (*mcp.CallToolResult, any, error) {
		return call(ctx, daw.Request{Op: "new_song", BPM: in.Tempo, Click: in.Click}, false)
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
		return call(ctx, daw.Request{Op: "add_track", Action: in.Format, Name: in.Name, Plugin: in.Plugin, Preset: in.Preset}, false)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "import_audio",
		Description: "Import audio files (WAV, AIFF) or a folder of stems into the open song: each file gets its own new mono or stereo track " +
			"to match it, named for a musician (01_Kick_Final.wav becomes Kick), with the clip at bar 1, so the stems line up ready to mix. " +
			"Files go in one at a time; other files in a folder are skipped and named. Studio Pro does not report clips to remotes: " +
			"the answer confirms the tracks and says clip placement was not read back, so pass that on. Only for Studio Pro on this Mac. " +
			"Undo is about three run_command Edit / Undo presses per stem; check get_song until the tracks are gone.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in importIn) (*mcp.CallToolResult, any, error) {
		paths, err := daw.ImportPaths(in.Paths)
		if err != nil {
			return failure(err), nil, nil
		}
		return call(ctx, daw.Request{Op: "import", Files: paths, Onto: in.Onto, At: in.Bar}, false)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "add_instrument",
		Description: "Add an instrument track with a virtual instrument loaded (Syntronik 2, SampleTank 4, MODO BASS 2…) or one of the musician's favorites, " +
			"name it, and read the preset back (Studio Pro shows the instrument's window for a moment to report it). " +
			"For \"three backing tracks: pad, keys, bass\" call it three times. Undo with run_command Edit / Undo.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in instrumentIn) (*mcp.CallToolResult, any, error) {
		return call(ctx, daw.Request{Op: "add_instrument", Plugin: in.Instrument, Name: in.Name, Preset: in.Preset}, false)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "list_presets",
		Description: "List a plug-in's or instrument's Studio Pro presets, the names add_insert, add_track and add_instrument take as preset. " +
			"Sounds saved only inside a plug-in's own browser (Guitar Rig, Syntronik) are not Studio Pro presets until the musician saves them " +
			"from the preset menu in the plug-in's window in Studio Pro.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in presetsIn) (*mcp.CallToolResult, any, error) {
		res, err := daw.Presets(in.Plugin, in.Filter)
		if err != nil {
			return failure(err), nil, nil
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: res.Message}}}, nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "favorite",
		Description: "The musician's favorite sounds: their words (\"clean guitar\", \"worship synth\") mapped to a plug-in and preset. " +
			"list them; set one when they say \"remember this as my clean guitar\" (use the plug-in and preset just used; the preset must be a Studio Pro preset, see list_presets); remove one. " +
			"add_track, add_insert and add_instrument accept a favorite's name wherever they take a plug-in. Works without Studio Pro.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in favoriteIn) (*mcp.CallToolResult, any, error) {
		res, err := daw.Favorites(daw.Request{Op: "favorite", Action: in.Action, Name: in.Name, Plugin: in.Plugin, Preset: in.Preset})
		if err != nil {
			return failure(err), nil, nil
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: res.Message}}}, nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "add_insert",
		Description: "Put an effect plug-in (or a favorite) on a channel, after its existing inserts, optionally loading one of its Studio Pro presets, " +
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
		return call(ctx, daw.Request{Op: "run", Category: in.Category, Name: in.Name, Args: in.Args}, false)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "fader",
		Description: "Read or set a track's fader in dB, on Studio Pro's own fader curve (matches the console: 0 dB unity, +10 dB top). " +
			"\"Bring the pad down 3 dB\" is change -3. Read back from Studio Pro: \"Pad at -3 dB (was 0 dB).\" Mute and solo are set_param .../mute and .../solo. " +
			"The fader is for the balance and the musician's notes (\"a touch\" 1-2 dB, \"a bit\" 3, \"a lot\" 6); measure with analyze_audio first, and fix a stem exported far off with trim.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in faderIn) (*mcp.CallToolResult, any, error) {
		req := daw.Request{Op: "fader", Path: in.Track}
		switch {
		case in.Change > 0:
			req.Action, req.Value = "up", strconv.FormatFloat(in.Change, 'f', -1, 64)
		case in.Change < 0:
			req.Action, req.Value = "down", strconv.FormatFloat(-in.Change, 'f', -1, 64)
		case in.DB != nil:
			req.Action, req.Value = "set", strconv.FormatFloat(*in.DB, 'f', -1, 64)
		}
		return call(ctx, req, false)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "trim",
		Description: "Read or set a track's input trim (Studio Pro's Input Gain, before plug-ins and fader) in dB, -24 to +24. " +
			"Use it when analyze_audio shows a stem exported far too quiet or loud (\"bass averages -47 dBFS, drums -23.7\": raise the bass trim about 20 dB); " +
			"keep the fader for the balance. Read back: \"Bass trim +20 dB (was 0 dB).\"",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in trimIn) (*mcp.CallToolResult, any, error) {
		req := daw.Request{Op: "trim", Path: in.Track}
		switch {
		case in.Change > 0:
			req.Action, req.Value = "up", strconv.FormatFloat(in.Change, 'f', -1, 64)
		case in.Change < 0:
			req.Action, req.Value = "down", strconv.FormatFloat(-in.Change, 'f', -1, 64)
		case in.DB != nil:
			req.Action, req.Value = "set", strconv.FormatFloat(*in.DB, 'f', -1, 64)
		}
		return call(ctx, req, false)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "analyze_audio",
		Description: "Measure stems before touching any level: per stem the average level (RMS over its active audio, silence below -60 dBFS left out), peak, " +
			"integrated loudness (LUFS, ITU-R BS.1770), active time, whether it is an empty export or clips, and the input trim that brings it to the -18 dBFS target. " +
			"Give paths (files or a folder, measured on this Mac without Studio Pro), track names (the stem imported onto each track), or nothing (every audio track in the song). " +
			"Reading only. Returns one line per stem to pass on, e.g. \"Loops is silent: an empty export.\"",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in analyzeIn) (*mcp.CallToolResult, any, error) {
		var paths []string
		if len(in.Paths) > 0 {
			var err error
			if paths, err = daw.ImportPaths(in.Paths); err != nil {
				return failure(err), nil, nil
			}
		}
		if len(paths) > 0 && len(in.Tracks) == 0 {
			target := in.Target
			if target == 0 {
				target = daw.DefaultTarget
			}
			if err := daw.CheckTarget(target); err != nil {
				return failure(err), nil, nil
			}
			res, err := daw.Analyze(ctx, paths, target)
			if err != nil {
				return failure(err), nil, nil
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: res.Message}}}, nil, nil
		}
		return call(ctx, daw.Request{Op: "analyze", Files: paths, Tracks: in.Tracks, TargetDBFS: in.Target}, false)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "gain_stage",
		Description: "Gain-stage the song: measure the stem on each audio track (or the named ones) and set each track's input trim so it averages the target (default -18 dBFS), " +
			"the way an engineer starts a mix. Faders, mutes and pans are not touched, so the musician's balance stays; still ask before running it on a mix they have balanced. " +
			"Silent stems and tracks without a stem are left alone and named. Returns one line per track with the trim before and after.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in gainStageIn) (*mcp.CallToolResult, any, error) {
		return call(ctx, daw.Request{Op: "gainstage", Tracks: in.Tracks, TargetDBFS: in.Target}, false)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "audio_edit",
		Description: "Edit the audio on one track with Studio Pro's own editing commands, on every event of that track: " +
			"fade-out or fade-in (\"fade out the last bar\", \"crossfade the end of the guitar take\" is a fade-out), split at a bar, " +
			"trim the start or end to a bar, crossfade (joins overlapping events and puts short fades on every edit), quantize. " +
			"Studio Pro does not report audio events to remotes, so the answer says the edit was sent but not read back. Undo with run_command Edit / Undo.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in audioIn) (*mcp.CallToolResult, any, error) {
		req := daw.Request{Op: "audio", Action: in.Edit, Path: in.Track, Bars: in.Bars, Seconds: in.Seconds}
		if in.Bar > 0 {
			req.Value = strconv.Itoa(in.Bar)
		}
		return call(ctx, req, false)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "section",
		Description: "Edit the song's arrangement on Studio Pro's arranger track, like a session engineer: " +
			"list the sections; add new ones at the end (\"add sections Verse, Chorus, Bridge\"); duplicate one (copy right after itself); " +
			"repeat one after another (\"repeat chorus one after the bridge\": action repeat, section \"Chorus 1\", target Bridge); " +
			"move one before or after another; delete one (the gap closes); rename one; put the cursor at one (goto) or play from it. " +
			"Everything in a section's time moves with it and later sections move along. Each edit is confirmed from Studio Pro's arranger " +
			"and answered in one line. Undo with run_command Edit / Undo (a move takes two).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in sectionIn) (*mcp.CallToolResult, any, error) {
		return call(ctx, daw.Request{Op: "section", Action: in.Action, Section: in.Section, Where: in.Where, Target: in.Target, Name: in.Name, Bars: in.Bars, Seconds: in.Seconds}, false)
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
