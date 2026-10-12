package daw

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/steveclarke/ucmix/internal/dawfav"
	"github.com/steveclarke/ucmix/internal/dawplugins"
)

// Test seams: the favorites file and the folders holding Studio Pro presets.
var (
	favoritesPath = func() (string, error) { return dawfav.DefaultPath() }
	presetRoots   = func() []string {
		home, _ := os.UserHomeDir()
		return []string{
			filepath.Join(home, "Documents", "Studio One", "Presets"),
			filepath.Join(home, "Documents", "Studio Pro", "Presets"),
			"/Applications/Studio Pro 8.app/Contents/Presets",
		}
	}
)

// instrumentWait bounds how long a new instrument may take to load.
var instrumentWait = 15 * time.Second

func loadFavorites() []dawfav.Favorite {
	p, err := favoritesPath()
	if err != nil {
		return nil
	}
	favs, _ := dawfav.Load(p)
	return favs
}

// pickPlugin resolves what the musician said, a favorite first, then an
// installed plug-in name. An explicit preset overrides the favorite's.
func pickPlugin(said, preset string) (dawplugins.Plugin, string, *dawfav.Favorite, error) {
	all, err := loadPlugins()
	if err != nil {
		return dawplugins.Plugin{}, "", nil, Problem{
			Message: "Could not read the list of plug-ins Studio Pro has scanned.",
			Hint:    "open Studio Pro once so it writes its plug-in list, then try again",
		}
	}
	var fav *dawfav.Favorite
	name := said
	if f, ok := dawfav.Find(loadFavorites(), said); ok {
		fav, name = &f, f.Plugin
		if strings.TrimSpace(preset) == "" {
			preset = f.Preset
		}
	}
	p, err := dawplugins.Find(all, name)
	if err != nil {
		hint := "list plug-ins with: ucdaw plugins " + name
		if fav == nil {
			hint += "; favorites with: ucdaw favorite list"
		}
		return dawplugins.Plugin{}, "", nil, Problem{Message: err.Error(), Hint: hint}
	}
	return p, preset, fav, nil
}

// presetExists reports whether a Studio Pro preset file exists for the
// plug-in under any preset folder, by its path relative to the plug-in's
// folder ("Misc/50 Hz Removal") or by its file name alone.
func presetExists(p dawplugins.Plugin, preset string) bool {
	preset = strings.TrimSuffix(strings.TrimSpace(preset), ".preset")
	if preset == "" || strings.Contains(preset, "..") {
		return false
	}
	for _, root := range presetRoots() {
		for _, vendor := range []string{p.Vendor, "Fender", "PreSonus", ""} {
			dir := filepath.Join(root, vendor, p.Name)
			if _, err := os.Stat(filepath.Join(dir, preset+".preset")); err == nil {
				return true
			}
		}
	}
	return false
}

func (st *Studio) addInstrument(ctx context.Context, req Request) (Result, error) {
	p, preset, fav, err := pickPlugin(req.Plugin, req.Preset)
	if err != nil {
		return Result{}, err
	}
	if !p.Instrument() {
		return Result{}, Problem{Message: fmt.Sprintf("%s is an effect, not an instrument", p.Name), Hint: "put it on a track with: ucdaw insert <track> " + p.Name}
	}
	if d, open := st.model().Dialog(); open {
		return Result{}, dialogProblem(d)
	}
	before := st.count("mixer/channel/count")
	presetArg := strings.TrimSuffix(strings.TrimSpace(preset), ".preset")
	if presetArg == "" {
		presetArg = "default"
	}
	args := map[string]string{"mode": "1", "cid": p.ClassID, "preset": presetArg, "openEditor": "0"}
	if err := st.session.Invoke(ctx, "Track", "Add Instrument and Track", args); err != nil {
		return Result{}, err
	}
	waitCtx, cancel := context.WithTimeout(ctx, instrumentWait)
	err = st.session.WaitFor(waitCtx, func(m *Model) bool {
		_, open := m.Dialog()
		return open || st.count("mixer/channel/count") > before
	})
	cancel()
	if d, open := st.model().Dialog(); open {
		_ = st.session.DismissDialog(ctx)
		return Result{}, Problem{Message: fmt.Sprintf("Studio Pro asked for choices (%q) instead of adding %s; canceled it.", d.Title, p.Name)}
	}
	if err != nil {
		return Result{}, st.unconfirmed(ErrUnconfirmed, "Adding "+p.Name)
	}
	chPath := ""
	for _, ch := range st.channels() {
		if strings.HasPrefix(ch.Path, "mixer/channel/") && st.num(ch.Path+"/selected") >= 0.5 {
			chPath = ch.Path
		}
	}
	if chPath == "" {
		chPath = fmt.Sprintf("mixer/channel/ch%d", st.count("mixer/channel/count"))
	}
	labelCtx, cancelLabel := context.WithTimeout(ctx, 2*time.Second)
	_ = st.session.WaitFor(labelCtx, func(m *Model) bool { s, _ := m.Str(chPath + "/label"); return s != "" })
	cancelLabel()
	label, _ := st.model().Str(chPath + "/label")
	if name := strings.TrimSpace(req.Name); name != "" && name != label {
		if err := st.session.SetStr(ctx, chPath+"/label", name); err != nil {
			return Result{}, st.unconfirmed(err, fmt.Sprintf("Added %s, but naming it %q", p.Name, name))
		}
		label = name
	}
	msg := fmt.Sprintf("Added instrument track %s with %s", label, p.Name)
	loaded := ""
	if presetArg != "default" {
		loaded = st.instrumentPreset(ctx)
		switch {
		case strings.EqualFold(loaded, path.Base(presetArg)):
			msg += " (preset " + loaded + ")"
		case loaded != "":
			msg += fmt.Sprintf("; Studio Pro loaded %q, not %q", loaded, preset)
		case presetExists(p, presetArg):
			msg += " (preset " + path.Base(presetArg) + " sent; Studio Pro did not report it back)"
		default:
			msg += missingPreset(p, preset, "default sound")
		}
	}
	if fav != nil {
		msg += fmt.Sprintf(", from your favorite %q", fav.Name)
	}
	data := map[string]any{"path": chPath, "name": label, "plugin": p, "preset": loaded}
	return Result{Message: msg + ".", Data: data}, nil
}

// instrumentPreset reads the selected instrument's preset name: Studio Pro
// reports it only for the device in focus, so the instrument editor is
// opened, read and closed again (a moment on screen). It returns "" when
// Studio Pro reports nothing.
func (st *Studio) instrumentPreset(ctx context.Context) string {
	if name, _ := st.model().Str("surface/focusDeviceName"); name != "" {
		return "" // an editor is already in focus; leave the musician's windows alone
	}
	if err := st.session.Invoke(ctx, "Console", "Show Instrument Editor", nil); err != nil {
		return ""
	}
	waitCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	_ = st.session.WaitFor(waitCtx, func(m *Model) bool { s, _ := m.Str("surface/focusDeviceName"); return s != "" })
	cancel()
	// The preset name can follow the device name by a moment.
	settleCtx, cancelSettle := context.WithTimeout(ctx, 500*time.Millisecond)
	_ = st.session.WaitFor(settleCtx, func(m *Model) bool { s, _ := m.Str("surface/focusPresetName"); return s != "" })
	cancelSettle()
	loaded, _ := st.model().Str("surface/focusPresetName")
	if device, _ := st.model().Str("surface/focusDeviceName"); device != "" {
		_ = st.session.Invoke(ctx, "Console", "Show Instrument Editor", nil)
		closeCtx, cancelClose := context.WithTimeout(ctx, 3*time.Second)
		_ = st.session.WaitFor(closeCtx, func(m *Model) bool { s, _ := m.Str("surface/focusDeviceName"); return s == "" })
		cancelClose()
	}
	return loaded
}

// Favorites lists, sets or removes favorites. It needs no Studio Pro.
func Favorites(req Request) (Result, error) {
	p, err := favoritesPath()
	if err != nil {
		return Result{}, Problem{Message: "Could not find the folder for ucdaw's settings."}
	}
	favs, err := dawfav.Load(p)
	if err != nil {
		return Result{}, Problem{Message: "Could not read your favorites file.", Hint: "fix or remove " + p}
	}
	switch req.Action {
	case "", "list":
		if len(favs) == 0 {
			return Result{Message: "No favorites yet.", Data: favs}, nil
		}
		parts := make([]string, len(favs))
		for i, f := range favs {
			parts[i] = f.Name + ": " + describeFavorite(f)
		}
		return Result{Message: fmt.Sprintf("%d favorite%s: %s.", len(favs), plural(float64(len(favs))), strings.Join(parts, "; ")), Data: favs}, nil
	case "remove":
		out, found := dawfav.Remove(favs, req.Name)
		if !found {
			return Result{}, Problem{Message: fmt.Sprintf("no favorite called %q", req.Name), Hint: "list them with: ucdaw favorite list"}
		}
		if err := dawfav.Save(p, out); err != nil {
			return Result{}, Problem{Message: "Could not save your favorites: " + err.Error()}
		}
		return Result{Message: fmt.Sprintf("Forgot %s.", req.Name)}, nil
	case "set":
		all, err := loadPlugins()
		if err != nil {
			return Result{}, Problem{Message: "Could not read the list of plug-ins Studio Pro has scanned."}
		}
		plug, err := dawplugins.Find(all, req.Plugin)
		if err != nil {
			return Result{}, Problem{Message: err.Error(), Hint: "list plug-ins with: ucdaw plugins " + req.Plugin}
		}
		kind := "effect"
		if plug.Instrument() {
			kind = "instrument"
		}
		f := dawfav.Favorite{Name: req.Name, Plugin: plug.Name, Preset: strings.TrimSuffix(strings.TrimSpace(req.Preset), ".preset"), Kind: kind}
		out, replaced, err := dawfav.Set(favs, f)
		if err != nil {
			return Result{}, Problem{Message: err.Error()}
		}
		if err := dawfav.Save(p, out); err != nil {
			return Result{}, Problem{Message: "Could not save your favorites: " + err.Error()}
		}
		saved, _ := dawfav.Find(out, req.Name)
		msg := fmt.Sprintf("Remembered %s: %s", saved.Name, describeFavorite(saved))
		if replaced != nil {
			msg += fmt.Sprintf(" (was %s)", describeFavorite(*replaced))
		}
		if saved.Preset != "" && !presetExists(plug, saved.Preset) {
			msg += fmt.Sprintf("; Studio Pro has no preset file %q for %s yet, so save it from the plug-in window's preset menu", saved.Preset, plug.Name)
		}
		return Result{Message: msg + ".", Data: saved}, nil
	}
	return Result{}, Problem{Message: fmt.Sprintf("unknown favorite action %q", req.Action), Hint: "use list, set or remove"}
}

func describeFavorite(f dawfav.Favorite) string {
	s := f.Plugin
	if f.Preset != "" {
		s += " with preset " + f.Preset
	}
	if f.Kind == "instrument" {
		s += " (instrument)"
	}
	return s
}

// maxPresetScan bounds how many files one preset lookup reads.
const maxPresetScan = 5000

// presetNames lists the Studio Pro presets saved for a plug-in, as the
// relative names --preset takes ("Misc/50 Hz Removal"), sorted.
func presetNames(p dawplugins.Plugin) []string {
	seen := map[string]bool{}
	var out []string
	scanned := 0
	for _, root := range presetRoots() {
		for _, vendor := range []string{p.Vendor, "Fender", "PreSonus"} {
			dir := filepath.Join(root, vendor, p.Name)
			_ = filepath.WalkDir(dir, func(file string, d os.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if scanned++; scanned > maxPresetScan {
					return filepath.SkipAll
				}
				if d.IsDir() || !strings.HasSuffix(d.Name(), ".preset") {
					return nil
				}
				rel, err := filepath.Rel(dir, file)
				if err != nil {
					return nil
				}
				name := strings.TrimSuffix(filepath.ToSlash(rel), ".preset")
				if !seen[name] {
					seen[name] = true
					out = append(out, name)
				}
				return nil
			})
		}
	}
	sort.Strings(out)
	return out
}

// missingPreset explains a preset Studio Pro could not load, with what it
// does have.
func missingPreset(p dawplugins.Plugin, preset, fallback string) string {
	names := presetNames(p)
	msg := fmt.Sprintf("; Studio Pro has no preset %q for it, so it has its %s", preset, fallback)
	switch {
	case len(names) == 0:
		msg += fmt.Sprintf(" (no %s sounds are saved as Studio Pro presets yet: save one from the preset menu in its window)", p.Name)
	case len(names) <= 6:
		msg += " (its presets: " + strings.Join(names, ", ") + ")"
	default:
		var close []string
		want := strings.ToLower(path.Base(preset))
		for _, n := range names {
			if strings.Contains(strings.ToLower(n), want) && len(close) < 6 {
				close = append(close, n)
			}
		}
		if len(close) > 0 {
			msg += " (close: " + strings.Join(close, ", ") + ")"
		} else {
			msg += fmt.Sprintf(" (%d presets; ucdaw presets %q lists them)", len(names), p.Name)
		}
	}
	return msg
}

// Presets lists a plug-in's Studio Pro presets.
func Presets(plugin, filter string) (Result, error) {
	p, _, _, err := pickPlugin(plugin, "")
	if err != nil {
		return Result{}, err
	}
	var out []string
	f := strings.ToLower(filter)
	for _, n := range presetNames(p) {
		if f == "" || strings.Contains(strings.ToLower(n), f) {
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		return Result{Message: fmt.Sprintf("%s has no Studio Pro presets saved%s; save one from the preset menu in its window.", p.Name, map[bool]string{true: "", false: " matching " + strconv.Quote(filter)}[filter == ""]), Data: out}, nil
	}
	return Result{Message: fmt.Sprintf("%d %s preset%s: %s.", len(out), p.Name, plural(float64(len(out))), strings.Join(out, ", ")), Data: out}, nil
}
