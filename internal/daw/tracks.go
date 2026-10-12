package daw

import (
	"context"
	"fmt"
	"math"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/steveclarke/ucmix/internal/dawplugins"
	"github.com/steveclarke/ucmix/internal/dawschema"
)

// Test seam: the plug-ins Studio Pro has scanned on this Mac.
var loadPlugins = func() ([]dawplugins.Plugin, error) { return dawplugins.Load("") }

// resolveChannel finds a channel by name ("Guitar"), number ("2", "ch2") or
// path ("mixer/channel/ch2").
func (st *Studio) resolveChannel(ref string) (Channel, error) {
	ref = strings.TrimSpace(ref)
	channels := st.channels()
	want := ref
	if n, err := strconv.Atoi(strings.TrimPrefix(strings.ToLower(ref), "ch")); err == nil {
		want = fmt.Sprintf("mixer/channel/ch%d", n)
	}
	var byName []Channel
	for _, ch := range channels {
		if ch.Path == want {
			return ch, nil
		}
		if strings.EqualFold(ch.Name, ref) {
			byName = append(byName, ch)
		}
	}
	if len(byName) == 1 {
		return byName[0], nil
	}
	var names []string
	for _, ch := range channels {
		if strings.HasPrefix(ch.Path, "mixer/channel/") {
			names = append(names, ch.Name)
		}
	}
	if len(byName) > 1 {
		return Channel{}, Problem{Message: fmt.Sprintf("more than one channel is called %q", ref), Hint: "use its number from get_song, e.g. ch2"}
	}
	hint := "the song has no tracks yet"
	if len(names) > 0 {
		hint = "channels: " + strings.Join(names, ", ")
	}
	return Channel{}, Problem{Message: fmt.Sprintf("no channel called %q", ref), Hint: hint}
}

func (st *Studio) count(p string) int { return int(math.Round(st.num(p) * 1000)) }

// addTrack adds a mono or stereo audio track, names it, and reports it.
func (st *Studio) addTrack(ctx context.Context, format, name string) (Result, error) {
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "" {
		format = "mono"
	}
	if format != "mono" && format != "stereo" {
		return Result{}, Problem{Message: fmt.Sprintf("audio tracks are mono or stereo, not %q", format)}
	}
	if d, open := st.model().Dialog(); open {
		return Result{}, dialogProblem(d)
	}
	before := st.count("mixer/channel/count")
	if err := st.session.Invoke(ctx, "Track", "Add Audio Track ("+format+")", nil); err != nil {
		return Result{}, err
	}
	waitCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	err := st.session.WaitFor(waitCtx, func(m *Model) bool {
		return st.count("mixer/channel/count") > before
	})
	cancel()
	if err != nil {
		return Result{}, st.unconfirmed(ErrUnconfirmed, "Add track")
	}
	chPath := fmt.Sprintf("mixer/channel/ch%d", st.count("mixer/channel/count"))
	// The new track is selected; find it in case Studio Pro put it elsewhere.
	for _, ch := range st.channels() {
		if strings.HasPrefix(ch.Path, "mixer/channel/") && st.num(ch.Path+"/selected") >= 0.5 {
			chPath = ch.Path
		}
	}
	labelCtx, cancelLabel := context.WithTimeout(ctx, time.Second)
	_ = st.session.WaitFor(labelCtx, func(m *Model) bool { s, _ := m.Str(chPath + "/label"); return s != "" })
	cancelLabel()
	label, _ := st.model().Str(chPath + "/label")
	if name != "" && name != label {
		if err := st.session.SetStr(ctx, chPath+"/label", name); err != nil {
			return Result{}, st.unconfirmed(err, fmt.Sprintf("Added %s, but naming it %q", label, name))
		}
		label = name
	}
	return Result{
		Message: fmt.Sprintf("Added %s audio track %s.", format, label),
		Data:    map[string]string{"path": chPath, "name": label},
	}, nil
}

// insert puts a plug-in on a channel's inserts, optionally loading one of
// the plug-in's Studio Pro presets, and reads both back.
func (st *Studio) insert(ctx context.Context, channel, plugin, preset string) (Result, error) {
	ch, err := st.resolveChannel(channel)
	if err != nil {
		return Result{}, err
	}
	p, preset, fav, err := pickPlugin(plugin, preset)
	if err != nil {
		return Result{}, err
	}
	if p.Instrument() {
		return Result{}, Problem{Message: fmt.Sprintf("%s is an instrument, not an effect", p.Name), Hint: "add an instrument track for it: ucdaw instrument add " + p.Name}
	}
	if d, open := st.model().Dialog(); open {
		return Result{}, dialogProblem(d)
	}
	if err := st.session.SetNum(ctx, ch.Path+"/selected", 1); err != nil {
		return Result{}, st.unconfirmed(err, "Selecting "+ch.Name)
	}
	before := st.count(ch.Path + "/inserts/count")
	presetArg := strings.TrimSuffix(strings.TrimSpace(preset), ".preset")
	if presetArg == "" {
		presetArg = "default"
	}
	args := map[string]string{"mode": "1", "cid": p.ClassID, "preset": presetArg}
	seq := st.model().Seq()
	if err := st.session.Invoke(ctx, "Track", "Add Insert to Selected Channels", args); err != nil {
		return Result{}, err
	}
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	err = st.session.WaitFor(waitCtx, func(m *Model) bool {
		_, open := m.Dialog()
		return open || st.count(ch.Path+"/inserts/count") > before
	})
	cancel()
	if d, open := st.model().Dialog(); open {
		_ = st.session.DismissDialog(ctx)
		return Result{}, Problem{Message: fmt.Sprintf("Studio Pro asked for choices (%q) instead of adding %s; canceled it.", d.Title, p.Name)}
	}
	if err != nil {
		return Result{}, st.unconfirmed(ErrUnconfirmed, "Adding "+p.Name)
	}
	st.settle(ctx, seq)
	slot := st.count(ch.Path + "/inserts/count")
	device, _ := st.model().Str(fmt.Sprintf("%s/inserts/slot%d/deviceName", ch.Path, slot))
	loaded, _ := st.model().Str("surface/focusPresetName")
	msg := fmt.Sprintf("Added %s to %s (insert %d)", p.Name, ch.Name, slot)
	data := map[string]any{"channel": ch.Path, "slot": slot, "device": device, "plugin": p, "preset": loaded}
	switch {
	case preset == "":
	case strings.EqualFold(loaded, path.Base(presetArg)):
		msg += fmt.Sprintf(" with preset %s", loaded)
	default:
		msg += missingPreset(p, preset, "default settings")
	}
	if fav != nil {
		msg += fmt.Sprintf(", from your favorite %q", fav.Name)
	}
	return Result{Message: msg + ".", Data: data}, nil
}

// Plugins lists installed effects and instruments.
func Plugins(filter string) (Result, error) {
	all, err := loadPlugins()
	if err != nil {
		return Result{}, Problem{Message: "Could not read the list of plug-ins Studio Pro has scanned."}
	}
	var out []dawplugins.Plugin
	f := strings.ToLower(filter)
	for _, p := range all {
		if f == "" || strings.Contains(strings.ToLower(p.Name+" "+p.Vendor), f) {
			out = append(out, p)
		}
	}
	msg := fmt.Sprintf("%d plug-in%s", len(out), plural(float64(len(out))))
	if filter != "" {
		msg += fmt.Sprintf(" match %q", filter)
	}
	return Result{Message: msg + ".", Data: out}, nil
}

// fader reads or sets a channel's fader in dB: "set" to a level, "up" or
// "down" by an amount, or read it.
func (st *Studio) fader(ctx context.Context, req Request) (Result, error) {
	ch, err := st.resolveChannel(req.Path)
	if err != nil {
		return Result{}, err
	}
	p, err := st.param(ch.Path + "/volume")
	if err != nil {
		return Result{}, err
	}
	wire, ok := st.model().Num(p.Path)
	if !ok {
		return Result{}, Problem{Message: fmt.Sprintf("Studio Pro has not reported %s's fader", ch.Name)}
	}
	before := dawschema.FaderDB(wire)
	data := func(db float64) map[string]any {
		out := map[string]any{"channel": ch.Path, "db": nil}
		if !math.IsInf(db, -1) {
			out["db"] = math.Round(db*10) / 10
		}
		return out
	}
	value := strings.TrimSpace(req.Value)
	if req.Action == "" && value == "" || req.Action == "get" {
		return Result{Message: fmt.Sprintf("%s is at %s.", ch.Name, dawschema.FormatDB(before)), Data: data(before)}, nil
	}
	var target float64
	switch req.Action {
	case "up", "down":
		amount, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(strings.ToLower(value), "db")), 64)
		if err != nil || math.IsNaN(amount) || amount <= 0 || amount > 100 {
			return Result{}, Problem{Message: fmt.Sprintf("say how many dB to move %s, e.g. 3", req.Action)}
		}
		if req.Action == "down" {
			amount = -amount
		}
		if math.IsInf(before, -1) {
			return Result{}, Problem{Message: fmt.Sprintf("%s's fader is all the way down", ch.Name), Hint: "set a level instead, e.g. -20"}
		}
		target = before + amount
		if target > 10 {
			return Result{}, Problem{Message: fmt.Sprintf("%s is at %s; the fader stops at +10 dB", ch.Name, dawschema.FormatDB(before))}
		}
		value = strconv.FormatFloat(target, 'f', -1, 64)
	}
	newWire, err := p.ParseHuman(value)
	if err != nil {
		return Result{}, Problem{Message: err.Error()}
	}
	if err := st.session.SetNum(ctx, p.Path, newWire); err != nil {
		return Result{}, st.unconfirmed(err, "Moving "+ch.Name+"'s fader")
	}
	got, _ := st.model().Num(p.Path)
	after := dawschema.FaderDB(got)
	return Result{
		Message: fmt.Sprintf("%s at %s (was %s).", ch.Name, dawschema.FormatDB(after), dawschema.FormatDB(before)),
		Data:    data(after),
	}, nil
}
