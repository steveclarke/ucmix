package daw

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/steveclarke/ucmix/internal/dawschema"
)

// Request is one musician-level operation. The CLI, the serve socket and the
// MCP server all speak it.
type Request struct {
	Op       string  `json:"op"`
	Action   string  `json:"action,omitempty"`
	Path     string  `json:"path,omitempty"`
	Value    string  `json:"value,omitempty"`
	Category string  `json:"category,omitempty"`
	Name     string  `json:"name,omitempty"`
	Filter   string  `json:"filter,omitempty"`
	Seconds  float64 `json:"seconds,omitempty"`
	Bars     float64 `json:"bars,omitempty"`
	BPM      float64 `json:"bpm,omitempty"`
	// Click is on or off for new_song; empty keeps the new song's own.
	Click string `json:"click,omitempty"`
	// Args are command arguments for run, as a Studio Pro macro carries them.
	Args map[string]string `json:"args,omitempty"`
	// Section, Target and Where name arranger sections for section edits:
	// move Section Where (before/after) Target.
	Section string `json:"section,omitempty"`
	Target  string `json:"target,omitempty"`
	Where   string `json:"where,omitempty"`
	// Plugin and Preset name a plug-in (or a favorite) for add_track and
	// add_instrument, and for favorite set.
	Plugin string `json:"plugin,omitempty"`
	Preset string `json:"preset,omitempty"`
	// Files, Onto and At are for import: absolute paths of audio files or
	// folders, an existing track to place them on, and the bar to place
	// them at (0 or 1 is the song start).
	Files []string `json:"files,omitempty"`
	Onto  string   `json:"onto,omitempty"`
	At    int      `json:"at,omitempty"`
}

// Result is what happened, in one line, plus structured detail for --json.
type Result struct {
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Engine runs requests against Studio Pro.
type Engine interface {
	Do(ctx context.Context, req Request) (Result, error)
}

// Problem is an error a musician can act on: what went wrong and what to do.
type Problem struct {
	Message string
	Hint    string
}

func (p Problem) Error() string { return p.Message }

// Sentence is the message as a sentence: capital first letter, final stop.
func (p Problem) Sentence() string {
	m := strings.TrimSpace(p.Message)
	if m == "" {
		return m
	}
	m = strings.ToUpper(m[:1]) + m[1:]
	if !strings.HasSuffix(m, ".") && !strings.HasSuffix(m, "?") && !strings.HasSuffix(m, "!") {
		m += "."
	}
	return m
}

// MaxPlay bounds a timed playback burst.
const MaxPlay = 60 * time.Second

// Studio runs requests on a live session.
type Studio struct {
	session *Session
	schema  *dawschema.Catalog
}

// NewStudio wraps a session with the parameter catalog.
func NewStudio(session *Session, schema *dawschema.Catalog) *Studio {
	return &Studio{session: session, schema: schema}
}

// Do runs one request.
func (st *Studio) Do(ctx context.Context, req Request) (Result, error) {
	switch req.Op {
	case "transport":
		return st.transport(ctx, req)
	case "click":
		return st.click(ctx, req.Action)
	case "tempo":
		return st.tempo(ctx, req.BPM)
	case "get":
		return st.get(req.Path)
	case "set":
		return st.set(ctx, req.Path, req.Value)
	case "song":
		return st.song(), nil
	case "commands":
		return st.commands(req.Filter), nil
	case "run":
		return st.run(ctx, req.Category, req.Name, req.Args)
	case "params":
		return Params(st.schema, req.Filter), nil
	case "dump":
		return st.dump(req.Filter), nil
	case "add_track":
		res, err := st.addTrack(ctx, req.Action, req.Name)
		if err != nil || strings.TrimSpace(req.Plugin) == "" {
			return res, err
		}
		data, _ := res.Data.(map[string]string)
		ins, err := st.insert(ctx, data["path"], req.Plugin, req.Preset)
		if err != nil {
			return Result{}, Problem{Message: strings.TrimSuffix(res.Message, ".") + ", but " + lowerFirst(Problem{Message: err.Error()}.Sentence())}
		}
		return Result{Message: strings.TrimSuffix(res.Message, ".") + "; " + lowerFirst(ins.Message), Data: ins.Data}, nil
	case "add_instrument":
		return st.addInstrument(ctx, req)
	case "favorite":
		return Favorites(req)
	case "insert":
		return st.insert(ctx, req.Path, req.Name, req.Value)
	case "plugins":
		return Plugins(req.Filter)
	case "section":
		return st.section(ctx, req)
	case "audio":
		return st.audio(ctx, req)
	case "fader":
		return st.fader(ctx, req)
	case "import":
		return st.importAudio(ctx, req)
	case "new_song":
		return st.newSong(ctx, req)
	}
	return Result{}, Problem{Message: fmt.Sprintf("unknown request %q", req.Op)}
}

func (st *Studio) model() *Model { return st.session.Model() }

func (st *Studio) num(path string) float64 {
	v, _ := st.model().Num(path)
	return v
}

func (st *Studio) human(path string) float64 {
	p, ok := st.schema.Lookup(path)
	if !ok {
		return st.num(path)
	}
	return p.FromWire(st.num(path))
}

// Tempo is the current tempo in bpm.
func (st *Studio) Tempo() float64 { return st.human("transport/tempo") }

func (st *Studio) timeSignature() (int, int) {
	num := int(st.human("transport/timeSignatureNum"))
	denom := 4
	if p, ok := st.schema.Lookup("transport/timeSignatureDenom"); ok {
		if d, err := strconv.Atoi(p.Format(st.num("transport/timeSignatureDenom"))); err == nil {
			denom = d
		}
	}
	if num <= 0 {
		num = 4
	}
	return num, denom
}

func (st *Studio) playing() bool { return st.num("transport/start") >= 0.5 }

func (st *Studio) press(ctx context.Context, path string, cond func(*Model) bool) error {
	if err := st.session.Press(ctx, path, 1); err != nil {
		return err
	}
	return st.session.confirm(ctx, cond)
}

// stop presses Stop until Studio Pro reports playback ended. Live tests saw
// one Stop answered by a stop=1/stop=0 blip with playback still running, so a
// second press follows if the first is not confirmed.
func (st *Studio) stop(ctx context.Context) error {
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		if err = st.session.Press(ctx, "transport/stop", 1); err != nil {
			return err
		}
		waitCtx, cancel := context.WithTimeout(ctx, stopWait)
		err = st.session.WaitFor(waitCtx, NumIs("transport/start", 0))
		cancel()
		if err == nil || ctx.Err() != nil || errors.Is(err, ErrConnectionClosed) {
			return err
		}
	}
	return ErrUnconfirmed
}

// stopWait is how long one Stop press waits for playback to end.
var stopWait = time.Second

func (st *Studio) transport(ctx context.Context, req Request) (Result, error) {
	switch req.Action {
	case "play":
		return st.play(ctx, req)
	case "stop":
		if err := st.stop(ctx); err != nil {
			return Result{}, st.unconfirmed(err, "Stop")
		}
		return Result{Message: "Stopped."}, nil
	case "rtz", "return":
		// The returnToZero button gesture did nothing live; Studio Pro's own
		// command does. Studio Pro reports no song position, so this is sent,
		// not read back.
		if err := st.session.Invoke(ctx, "Transport", "Return to Zero", nil); err != nil {
			return Result{}, err
		}
		return Result{Message: "Returned to the start of the song."}, nil
	case "loop", "loop-on", "loop-off":
		return st.loop(ctx, req.Action, req.Value)
	}
	return Result{}, Problem{
		Message: fmt.Sprintf("unknown transport action %q", req.Action),
		Hint:    "use play, stop, rtz or loop",
	}
}

func (st *Studio) play(ctx context.Context, req Request) (Result, error) {
	if req.Seconds < 0 || req.Bars < 0 || math.IsNaN(req.Seconds) || math.IsNaN(req.Bars) {
		return Result{}, Problem{Message: "a play length must be a positive number of seconds or bars"}
	}
	if req.Bars > 0 && st.Tempo() <= 0 {
		return Result{}, Problem{Message: "Studio Pro has not reported the tempo, so bars cannot be timed", Hint: "give a length in seconds instead"}
	}
	duration := time.Duration(req.Seconds * float64(time.Second))
	what := ""
	if req.Bars > 0 {
		num, denom := st.timeSignature()
		beats := req.Bars * float64(num) * 4 / float64(denom)
		duration = time.Duration(beats * 60 / st.Tempo() * float64(time.Second))
		what = fmt.Sprintf("%s bar%s", trim(req.Bars), plural(req.Bars))
	}
	if duration > MaxPlay {
		return Result{}, Problem{
			Message: fmt.Sprintf("%s is longer than the %s limit for a timed play", duration.Round(time.Second), MaxPlay),
			Hint:    "play without a length and stop it yourself",
		}
	}
	if !st.playing() {
		if err := st.press(ctx, "transport/start", NumIs("transport/start", 1)); err != nil {
			if duration > 0 {
				// Playback may have started anyway; a timed play must end stopped.
				stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
				_ = st.stop(stopCtx)
				cancel()
			}
			return Result{}, st.unconfirmed(err, "Play")
		}
	}
	if duration <= 0 {
		return Result{Message: fmt.Sprintf("Playing at %s bpm.", trim(round2(st.Tempo())))}, nil
	}
	if what == "" {
		what = fmt.Sprintf("%s s", trim(round2(duration.Seconds())))
	}
	waitErr := sleepFor(ctx, duration)
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if err := st.stop(stopCtx); err != nil {
		return Result{}, st.unconfirmed(err, "Stop after playing")
	}
	if waitErr != nil {
		return Result{Message: "Interrupted; stopped."}, nil
	}
	return Result{Message: fmt.Sprintf("Played %s at %s bpm, then stopped.", what, trim(round2(st.Tempo())))}, nil
}

func (st *Studio) loop(ctx context.Context, action, value string) (Result, error) {
	current := st.num("transport/loop") >= 0.5
	target := !current
	switch {
	case action == "loop-on" || value == "on":
		target = true
	case action == "loop-off" || value == "off":
		target = false
	}
	if target == current {
		return Result{Message: "Loop is already " + onOff(target) + "."}, nil
	}
	// The loop button carries its target state (1 on, 0 off); it is not a toggle.
	if err := st.session.Press(ctx, "transport/loop", boolWire(target)); err != nil {
		return Result{}, err
	}
	if err := st.session.confirm(ctx, NumIs("transport/loop", boolWire(target))); err != nil {
		if target && st.num("transport/loop") < 0.5 && !errors.Is(err, ErrConnectionClosed) {
			return Result{}, Problem{
				Message: "Studio Pro kept the loop off: the song has no loop range yet.",
				Hint:    `select a range and run: ucdaw command run "Transport > Loop Selection"`,
			}
		}
		return Result{}, st.unconfirmed(err, "Loop "+onOff(target))
	}
	return Result{Message: "Loop " + onOff(target) + "."}, nil
}

func (st *Studio) click(ctx context.Context, action string) (Result, error) {
	current := st.num("metronome/clickOn") >= 0.5
	var target bool
	switch action {
	case "on":
		target = true
	case "off":
		target = false
	case "", "toggle":
		target = !current
	case "status":
		return Result{Message: "Click is " + onOff(current) + ".", Data: map[string]bool{"on": current}}, nil
	default:
		return Result{}, Problem{Message: fmt.Sprintf("click takes on, off or toggle, not %q", action)}
	}
	data := map[string]bool{"on": target}
	if target == current {
		return Result{Message: "Click is already " + onOff(target) + ".", Data: data}, nil
	}
	if err := st.session.SetNum(ctx, "metronome/clickOn", boolWire(target)); err != nil {
		return Result{}, st.unconfirmed(err, "Click "+onOff(target))
	}
	return Result{Message: "Click " + onOff(target) + ".", Data: data}, nil
}

func (st *Studio) tempo(ctx context.Context, bpm float64) (Result, error) {
	before := round2(st.Tempo())
	if bpm == 0 {
		return Result{Message: fmt.Sprintf("Tempo is %s bpm.", trim(before)), Data: map[string]float64{"bpm": before}}, nil
	}
	p, _ := st.schema.Lookup("transport/tempo")
	wire, err := p.ToWire(bpm)
	if err != nil {
		return Result{}, Problem{Message: fmt.Sprintf("tempo must be between %s and %s bpm", trim(p.Min), trim(p.Max))}
	}
	if closeTo(st.num("transport/tempo"), wire) {
		return Result{Message: fmt.Sprintf("Tempo is already %s bpm.", trim(before)), Data: map[string]float64{"bpm": before}}, nil
	}
	if err := st.session.SetNum(ctx, "transport/tempo", wire); err != nil {
		return Result{}, st.unconfirmed(err, "Tempo change")
	}
	after := round2(st.Tempo())
	return Result{
		Message: fmt.Sprintf("Tempo %s bpm (was %s).", trim(after), trim(before)),
		Data:    map[string]float64{"bpm": after, "was": before},
	}, nil
}

func (st *Studio) param(path string) (dawschema.Param, error) {
	path = strings.Trim(path, "/")
	p, ok := st.schema.Lookup(path)
	if !ok {
		return p, Problem{
			Message: fmt.Sprintf("Studio Pro has no parameter %q", path),
			Hint:    "list them with: ucdaw params " + lastSegment(path),
		}
	}
	return p, nil
}

// describe renders a path's current value as "<where> <what>: <value>".
func (st *Studio) describe(p dawschema.Param) (string, any) {
	label := st.pathLabel(p.Path)
	switch p.Type {
	case dawschema.String:
		s, _ := st.model().Str(p.Path)
		return fmt.Sprintf("%s: %q", label, s), s
	case dawschema.Color:
		return fmt.Sprintf("%s: #%08x", label, uint32(st.num(p.Path))), uint32(st.num(p.Path))
	}
	wire, ok := st.model().Num(p.Path)
	if !ok {
		return label + ": not reported by Studio Pro", nil
	}
	return fmt.Sprintf("%s: %s", label, p.Format(wire)), p.FromWire(wire)
}

// missingChannel explains a path to a channel the song does not have.
func (st *Studio) missingChannel(path string) error {
	parts := strings.Split(path, "/")
	if len(parts) < 3 || parts[0] != "mixer" || !strings.HasPrefix(parts[2], "ch") {
		return nil
	}
	n, err := strconv.Atoi(strings.TrimPrefix(parts[2], "ch"))
	if err != nil {
		return nil
	}
	have := st.count("mixer/" + parts[1] + "/count")
	if n <= have {
		return nil
	}
	noun := map[string]string{"channel": "track", "input": "input channel", "sub": "sub output", "main": "main output", "listen": "listen bus"}[parts[1]]
	if noun == "" {
		noun = parts[1]
	}
	return Problem{
		Message: fmt.Sprintf("there is no %s %d; the song has %s", noun, n, map[bool]string{true: "none", false: strconv.Itoa(have)}[have == 0]),
		Hint:    "get_song or `ucdaw state` lists the channels",
	}
}

func (st *Studio) get(path string) (Result, error) {
	p, err := st.param(path)
	if err != nil {
		return Result{}, err
	}
	if err := st.missingChannel(p.Path); err != nil {
		return Result{}, err
	}
	msg, value := st.describe(p)
	return Result{Message: msg + ".", Data: map[string]any{"path": p.Path, "value": value}}, nil
}

func (st *Studio) set(ctx context.Context, path, value string) (Result, error) {
	p, err := st.param(path)
	if err != nil {
		return Result{}, err
	}
	if err := st.missingChannel(p.Path); err != nil {
		return Result{}, err
	}
	if p.ReadOnly() {
		return Result{}, Problem{Message: fmt.Sprintf("%s is read-only; Studio Pro reports it but does not take changes", p.Path)}
	}
	before, _ := st.describe(p)
	oldName, _ := st.model().Str(p.Path)
	if p.Type == dawschema.String {
		if err := st.session.SetStr(ctx, p.Path, value); err != nil {
			return Result{}, st.unconfirmed(err, "Change to "+p.Path)
		}
	} else {
		wire, err := p.ParseHuman(value)
		if err != nil {
			return Result{}, Problem{Message: err.Error()}
		}
		if err := st.session.SetNum(ctx, p.Path, wire); err != nil {
			return Result{}, st.unconfirmed(err, "Change to "+p.Path)
		}
	}
	after, v := st.describe(p)
	msg := fmt.Sprintf("%s (was %s).", after, valuePart(before))
	if strings.HasPrefix(p.Path, "mixer/") && strings.HasSuffix(p.Path, "/label") {
		msg = fmt.Sprintf("Renamed %s to %s.", oldName, value)
	}
	return Result{
		Message: msg,
		Data:    map[string]any{"path": p.Path, "value": v},
	}, nil
}

func valuePart(described string) string {
	if i := strings.LastIndex(described, ": "); i >= 0 {
		return described[i+2:]
	}
	return described
}

// pathLabel names a path the way a musician would: "Guitar volume".
func (st *Studio) pathLabel(path string) string {
	parts := strings.Split(path, "/")
	if len(parts) >= 4 && parts[0] == "mixer" && strings.HasPrefix(parts[2], "ch") {
		name, _ := st.model().Str(strings.Join(parts[:3], "/") + "/label")
		if name != "" {
			return name + " " + strings.Join(parts[3:], " ")
		}
	}
	switch path {
	case "transport/tempo":
		return "Tempo"
	case "metronome/clickOn":
		return "Click"
	}
	return path
}

// Channel is one mixer channel in the song.
type Channel struct {
	Path    string   `json:"path"`
	Name    string   `json:"name"`
	Kind    string   `json:"kind"`
	Format  string   `json:"format,omitempty"`
	Volume  string   `json:"volume,omitempty"`
	Mute    bool     `json:"mute,omitempty"`
	Solo    bool     `json:"solo,omitempty"`
	Armed   bool     `json:"armed,omitempty"`
	Inserts []string `json:"inserts,omitempty"`
}

// Song is the live summary of the open song.
type Song struct {
	Document string    `json:"document,omitempty"`
	Tempo    float64   `json:"tempo"`
	Meter    string    `json:"timeSignature"`
	Click    bool      `json:"click"`
	Playing  bool      `json:"playing"`
	Loop     bool      `json:"loop"`
	Channels []Channel `json:"channels"`
	Sections []Event   `json:"sections,omitempty"`
	Markers  []Event   `json:"markers,omitempty"`
}

func (st *Studio) channels() []Channel {
	var out []Channel
	for _, group := range []string{"input", "channel", "sub", "main"} {
		base := "mixer/" + group
		count := int(math.Round(st.num(base+"/count") * 1000))
		for i := 1; i <= count; i++ {
			path := fmt.Sprintf("%s/ch%d", base, i)
			name, _ := st.model().Str(path + "/label")
			kind, _ := st.model().Str(path + "/type")
			format, _ := st.model().Str(path + "/speakerFormat")
			ch := Channel{
				Path:   path,
				Name:   name,
				Kind:   kind,
				Format: format,
				Mute:   st.num(path+"/mute") >= 0.5,
				Solo:   st.num(path+"/solo") >= 0.5,
				Armed:  st.num(path+"/recordArmed") >= 0.5,
			}
			if p, ok := st.schema.Lookup(path + "/volume"); ok {
				if wire, ok := st.model().Num(path + "/volume"); ok {
					ch.Volume = p.Format(wire)
				}
			}
			inserts := int(math.Round(st.num(path+"/inserts/count") * 1000))
			for slot := 1; slot <= inserts; slot++ {
				if device, _ := st.model().Str(fmt.Sprintf("%s/inserts/slot%d/deviceName", path, slot)); device != "" {
					ch.Inserts = append(ch.Inserts, device)
				}
			}
			out = append(out, ch)
		}
	}
	return out
}

// Song summarizes the live song.
func (st *Studio) Song() Song {
	num, denom := st.timeSignature()
	title, _ := st.model().Str("document/title")
	return Song{
		Document: title,
		Tempo:    round2(st.Tempo()),
		Meter:    fmt.Sprintf("%d/%d", num, denom),
		Click:    st.num("metronome/clickOn") >= 0.5,
		Playing:  st.playing(),
		Loop:     st.num("transport/loop") >= 0.5,
		Channels: st.channels(),
		Sections: st.model().Events("Arng"),
		Markers:  userMarkers(st.model().Events("Mark")),
	}
}

// userMarkers drops the song's built-in start and end markers.
func userMarkers(all []Event) []Event {
	var out []Event
	for _, e := range all {
		if e.Tag == "Mark" {
			out = append(out, e)
		}
	}
	return out
}

// eventChanges describes markers or sections added or removed.
func eventChanges(noun string, before, after []Event) string {
	key := func(e Event) string { return e.Name + "@" + clock(e.Start) }
	keys := func(es []Event) []string {
		out := make([]string, len(es))
		for i, e := range es {
			out[i] = key(e)
		}
		return out
	}
	added, removed := diffLists(keys(before), keys(after))
	var parts []string
	for _, k := range added {
		name, at, _ := strings.Cut(k, "@")
		parts = append(parts, fmt.Sprintf("added %s %s at %s", noun, name, at))
	}
	for _, k := range removed {
		name, at, _ := strings.Cut(k, "@")
		parts = append(parts, fmt.Sprintf("removed %s %s at %s", noun, name, at))
	}
	return strings.Join(parts, ", ")
}

// clock renders seconds as m:ss.
func clock(seconds float64) string {
	s := int(math.Round(seconds))
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

func eventSummary(events []Event) string {
	parts := make([]string, len(events))
	for i, e := range events {
		name := e.Name
		if name == "" {
			name = fmt.Sprintf("#%d", i+1)
		}
		parts[i] = fmt.Sprintf("%s %s", name, clock(e.Start))
	}
	return strings.Join(parts, ", ")
}

func kindWord(kind string) string {
	switch kind {
	case "AudioTrack":
		return "audio"
	case "MusicTrack", "InstrumentTrack", "AudioSynth":
		return "instrument"
	case "AudioOutput":
		return "output"
	case "AudioInput":
		return "input"
	case "AudioEffect", "FXChannel":
		return "FX"
	case "AudioGroup", "BusChannel":
		return "bus"
	}
	return strings.ToLower(kind)
}

func (st *Studio) song() Result {
	song := st.Song()
	state := "stopped"
	if song.Playing {
		state = "playing"
	}
	var tracks []string
	for _, ch := range song.Channels {
		if strings.HasPrefix(ch.Path, "mixer/main/") {
			continue
		}
		desc := ch.Name + " (" + kindWord(ch.Kind)
		if ch.Format != "" && ch.Kind == "AudioTrack" {
			desc += ", " + strings.ToLower(ch.Format)
		}
		if ch.Volume != "" && ch.Volume != "0 dB" {
			desc += ", " + ch.Volume
		}
		if ch.Mute {
			desc += ", muted"
		}
		if len(ch.Inserts) > 0 {
			desc += "; " + strings.Join(ch.Inserts, ", ")
		}
		tracks = append(tracks, desc+")")
	}
	msg := fmt.Sprintf("%s bpm, %s, click %s, loop %s, %s; ", trim(song.Tempo), song.Meter, onOff(song.Click), onOff(song.Loop), state)
	switch len(tracks) {
	case 0:
		msg += "no tracks yet"
	default:
		msg += fmt.Sprintf("%d channel%s: %s", len(tracks), plural(float64(len(tracks))), strings.Join(tracks, ", "))
	}
	if sections := st.sectionList(); len(sections) > 0 {
		msg += "; sections: " + sectionsSummary(sections)
	}
	if len(song.Markers) > 0 {
		msg += "; markers: " + st.markerSummary(song.Markers)
	}
	msg += "."
	return Result{Message: msg, Data: song}
}

// FindCommand resolves a command from a category and name, matching case
// and accepting a unique name without its category.
func FindCommand(all []Command, category, name string) (Command, error) {
	if category != "" && name == "" {
		if cat, rest, ok := strings.Cut(category, "|"); ok {
			category, name = cat, rest
		} else if cat, rest, ok := strings.Cut(category, ">"); ok {
			category, name = strings.TrimSpace(cat), strings.TrimSpace(rest)
		} else {
			category, name = "", category
		}
	}
	var matches []Command
	for _, c := range all {
		if strings.EqualFold(c.Name, name) && (category == "" || strings.EqualFold(c.Category, category)) {
			matches = append(matches, c)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		hint := "list them with: ucdaw command list " + name
		return Command{}, Problem{Message: fmt.Sprintf("Studio Pro has no command %q", joinCommand(category, name)), Hint: hint}
	}
	var names []string
	for _, m := range matches {
		names = append(names, joinCommand(m.Category, m.Name))
	}
	return Command{}, Problem{
		Message: fmt.Sprintf("%q is in more than one category: %s", name, strings.Join(names, "; ")),
		Hint:    "name the category too",
	}
}

func joinCommand(category, name string) string {
	if category == "" {
		return name
	}
	return category + " > " + name
}

func (st *Studio) commands(filter string) Result {
	all := st.model().Commands()
	if len(all) == 0 {
		return Result{Message: "Studio Pro has not sent its command list."}
	}
	var out []Command
	f := strings.ToLower(filter)
	for _, c := range all {
		if f == "" || strings.Contains(strings.ToLower(c.Category+" "+c.Name), f) {
			out = append(out, c)
		}
	}
	msg := fmt.Sprintf("%d Studio Pro command%s", len(out), plural(float64(len(out))))
	if filter != "" {
		msg += fmt.Sprintf(" match %q", filter)
	}
	return Result{Message: msg + ".", Data: out}
}

// commandSettle is how long a command's effects are gathered after the last
// update Studio Pro sends.
var commandSettle = 400 * time.Millisecond

func (st *Studio) run(ctx context.Context, category, name string, args map[string]string) (Result, error) {
	cmd, err := FindCommand(st.model().Commands(), category, name)
	if err != nil {
		return Result{}, err
	}
	if d, open := st.model().Dialog(); open {
		return Result{}, dialogProblem(d)
	}
	beforeChannels := st.channels()
	beforeMarkers := userMarkers(st.model().Events("Mark"))
	beforeSections := st.model().Events("Arng")
	seq := st.model().Seq()
	if err := st.session.Invoke(ctx, cmd.Category, cmd.Name, args); err != nil {
		return Result{}, err
	}
	st.settle(ctx, seq)
	full := joinCommand(cmd.Category, cmd.Name)
	if d, open := st.model().Dialog(); open {
		// A command that needs choices opens its dialog; leaving it would
		// block Studio Pro, so ucdaw cancels it and says so.
		if err := st.session.DismissDialog(ctx); err != nil {
			return Result{}, Problem{
				Message: fmt.Sprintf("%s opened the %q dialog and ucdaw could not close it.", full, d.Title),
				Hint:    "close it in Studio Pro",
			}
		}
		hint := "pass the command's arguments, or use a command that needs no dialog"
		if strings.EqualFold(cmd.Category, "File") && strings.EqualFold(cmd.Name, "New Session") {
			hint = newSongHint
		}
		return Result{}, Problem{
			Message: fmt.Sprintf("%s opened the %q dialog, which needs choices ucdaw cannot make; canceled it.", full, d.Title),
			Hint:    hint,
		}
	}
	changes := st.model().ChangesSince(seq)
	effect := st.summarize(beforeChannels, changes)
	if effect == "" {
		effect = eventChanges("marker", beforeMarkers, userMarkers(st.model().Events("Mark")))
		if sections := eventChanges("section", beforeSections, st.model().Events("Arng")); sections != "" {
			effect = strings.TrimPrefix(effect+", "+sections, ", ")
		}
	}
	data := map[string]any{"command": cmd, "changed": changes}
	if effect == "" {
		return Result{
			Message: fmt.Sprintf("Ran %s; Studio Pro reported no change (view and editing commands do not report back).", full),
			Data:    data,
		}, nil
	}
	return Result{Message: fmt.Sprintf("Ran %s: %s.", full, effect), Data: data}, nil
}

// settle waits for the first update and then for a quiet gap, up to two
// seconds in all.
func (st *Studio) settle(ctx context.Context, seq uint64) {
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	quiet := time.NewTimer(commandSettle * 2)
	defer quiet.Stop()
	for {
		wake := st.model().Changed()
		select {
		case <-wake:
			if !quiet.Stop() {
				select {
				case <-quiet.C:
				default:
				}
			}
			quiet.Reset(commandSettle)
		case <-quiet.C:
			return
		case <-deadline.C:
			return
		case <-ctx.Done():
			return
		case <-st.session.Done():
			return
		}
	}
}

func (st *Studio) summarize(before []Channel, changes []string) string {
	after := st.channels()
	// Channels are matched by name and kind, not path: removing one channel
	// renumbers the ones after it.
	key := func(c Channel) string { return c.Name + "|" + c.Kind }
	keys := func(cs []Channel) []string {
		out := make([]string, len(cs))
		for i, c := range cs {
			out[i] = key(c)
		}
		return out
	}
	addedCh, removedCh := diffLists(keys(before), keys(after))
	var parts []string
	for _, k := range addedCh {
		name, kind, _ := strings.Cut(k, "|")
		parts = append(parts, fmt.Sprintf("added %s (%s)", name, kindWord(kind)))
	}
	for _, k := range removedCh {
		name, _, _ := strings.Cut(k, "|")
		parts = append(parts, fmt.Sprintf("removed %s", name))
	}
	if len(parts) > 0 {
		return strings.Join(parts, ", ")
	}
	inserts := map[string][]string{}
	for _, c := range before {
		inserts[key(c)] = c.Inserts
	}
	for _, c := range after {
		added, removed := diffLists(inserts[key(c)], c.Inserts)
		for _, a := range added {
			parts = append(parts, fmt.Sprintf("added %s to %s", a, c.Name))
		}
		for _, r := range removed {
			parts = append(parts, fmt.Sprintf("removed %s from %s", r, c.Name))
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, ", ")
	}
	var described []string
	for _, path := range changes {
		if strings.HasPrefix(path, "@") || strings.HasPrefix(path, "recorddiskspace/") ||
			strings.HasPrefix(path, "activity/") || strings.HasSuffix(path, "/clip") {
			continue
		}
		p, ok := st.schema.Lookup(path)
		if !ok || p.Private() {
			continue
		}
		d, _ := st.describe(p)
		described = append(described, strings.Replace(d, ": ", " now ", 1))
	}
	sort.Strings(described)
	if len(described) > 4 {
		described = append(described[:4], fmt.Sprintf("and %d more", len(described)-4))
	}
	return strings.Join(described, ", ")
}

// Value is one live value in the model, formatted for reading.
type Value struct {
	Path  string `json:"path"`
	Value string `json:"value"`
}

func (st *Studio) dump(prefix string) Result {
	prefix = strings.Trim(prefix, "/")
	var out []Value
	for _, path := range st.model().Paths(prefix) {
		v := Value{Path: path}
		if p, ok := st.schema.Lookup(path); ok {
			if p.Private() {
				continue
			}
			v.Value = valuePart(func() string { d, _ := st.describe(p); return d }())
		} else if s, ok := st.model().Str(path); ok {
			v.Value = fmt.Sprintf("%q", s)
		} else {
			v.Value = trim(st.num(path))
		}
		out = append(out, v)
	}
	where := "in the song"
	if prefix != "" {
		where = fmt.Sprintf("under %s", prefix)
	}
	return Result{Message: fmt.Sprintf("%d live values %s.", len(out), where), Data: out}
}

// diffLists returns entries of b missing from a, and of a missing from b,
// counting duplicates.
func diffLists(a, b []string) (added, removed []string) {
	count := map[string]int{}
	for _, x := range a {
		count[x]++
	}
	for _, x := range b {
		if count[x] > 0 {
			count[x]--
			continue
		}
		added = append(added, x)
	}
	for _, x := range a {
		if count[x] > 0 {
			count[x]--
			removed = append(removed, x)
		}
	}
	return added, removed
}

// Params lists catalog entries, one per path pattern.
func Params(schema *dawschema.Catalog, filter string) Result {
	params := schema.Patterns(filter)
	var visible []dawschema.Param
	for _, p := range params {
		if !p.Private() {
			visible = append(visible, p)
		}
	}
	msg := fmt.Sprintf("%d parameter%s", len(visible), plural(float64(len(visible))))
	if filter != "" {
		msg += fmt.Sprintf(" match %q", filter)
	}
	return Result{Message: msg + "; <n> is a channel or slot number.", Data: visible}
}

func dialogProblem(d Dialog) Problem {
	return Problem{
		Message: fmt.Sprintf("Studio Pro has the %q dialog open, so it is not taking commands.", d.Title),
		Hint:    "close it in Studio Pro first",
	}
}

func (st *Studio) unconfirmed(err error, what string) error {
	if d, open := st.model().Dialog(); open && errors.Is(err, ErrUnconfirmed) {
		p := dialogProblem(d)
		p.Message = what + " was not confirmed: Studio Pro has the " + fmt.Sprintf("%q", d.Title) + " dialog open."
		return p
	}
	switch {
	case errors.Is(err, ErrUnconfirmed):
		return Problem{
			Message: what + " was sent, but Studio Pro did not confirm it.",
			Hint:    "check Studio Pro: a dialog may be open, or the song may be busy",
		}
	case errors.Is(err, ErrConnectionClosed):
		return Problem{
			Message: "Studio Pro closed the connection.",
			Hint:    "make sure Studio Pro is still open, then try again",
		}
	}
	return err
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func boolWire(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

func trim(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

func plural(n float64) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func lastSegment(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// markerSummary lists markers by bar when the tempo is known.
func (st *Studio) markerSummary(markers []Event) string {
	spb := st.secondsPerBar()
	if spb <= 0 {
		return eventSummary(markers)
	}
	parts := make([]string, len(markers))
	for i, e := range markers {
		name := e.Name
		if name == "" {
			name = fmt.Sprintf("#%d", i+1)
		}
		parts[i] = fmt.Sprintf("%s bar %s", name, trim(barAt(e.Start, spb)))
	}
	return strings.Join(parts, ", ")
}
