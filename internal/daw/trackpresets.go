package daw

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// trackPresetWait bounds how long a track preset may take to start adding
// its channels; its plug-ins load before the first one appears.
var trackPresetWait = 20 * time.Second

// trackPresetQuiet is how long after its latest channel a track preset is
// taken to be fully loaded.
var trackPresetQuiet = 2 * time.Second

// trackPresetDirs lists the "Track Presets" folders under the preset roots:
// Studio Pro keeps them one level down ("User Presets/Track Presets").
func trackPresetDirs() []string {
	var out []string
	for _, root := range presetRoots() {
		out = append(out, filepath.Join(root, "Track Presets"))
		nested, _ := filepath.Glob(filepath.Join(root, "*", "Track Presets"))
		out = append(out, nested...)
	}
	return out
}

// trackPresetNames lists the saved track presets as the names Studio Pro's
// Load Track Preset takes: the path below "Track Presets", folders first
// ("My Track Presets/Album/Bass"), sorted.
func trackPresetNames() []string {
	seen := map[string]bool{}
	var out []string
	scanned := 0
	for _, dir := range trackPresetDirs() {
		_ = filepath.WalkDir(dir, func(file string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if scanned++; scanned > maxPresetScan {
				return filepath.SkipAll
			}
			if d.IsDir() || !strings.HasSuffix(d.Name(), ".trackpreset") {
				return nil
			}
			rel, err := filepath.Rel(dir, file)
			if err != nil {
				return nil
			}
			name := strings.TrimSuffix(filepath.ToSlash(rel), ".trackpreset")
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
			return nil
		})
	}
	sort.Strings(out)
	return out
}

// TrackPresets lists the saved track presets.
func TrackPresets(filter string) (Result, error) {
	out := []string{}
	f := strings.ToLower(filter)
	for _, n := range trackPresetNames() {
		if f == "" || strings.Contains(strings.ToLower(n), f) {
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		if filter != "" {
			return Result{Message: fmt.Sprintf("No track presets match %q.", filter), Data: out}, nil
		}
		return Result{Message: "No track presets are saved yet; save one in Studio Pro with Track > Store Track Preset.", Data: out}, nil
	}
	return Result{Message: fmt.Sprintf("%d track preset%s: %s.", len(out), plural(float64(len(out))), strings.Join(out, ", ")), Data: out}, nil
}

// PickTrackPreset resolves a track preset name without Studio Pro, so a
// name that fits none or several is answered before anything connects.
func PickTrackPreset(said string) (string, error) { return pickTrackPreset(said) }

// pickTrackPreset resolves what the musician said to one saved track preset:
// its full name, or any unique tail of it ("Bass", "Album/Bass"), matching
// case. Several presets often share a file name, so a tail that fits more
// than one is refused with the choices.
func pickTrackPreset(said string) (string, error) {
	want := strings.Trim(strings.TrimSuffix(strings.TrimSpace(filepath.ToSlash(said)), ".trackpreset"), "/")
	if want == "" || strings.Contains(want, "..") {
		return "", Problem{Message: "name a track preset", Hint: "list them with: ucdaw trackpreset list"}
	}
	names := trackPresetNames()
	low := strings.ToLower(want)
	var matches []string
	for _, n := range names {
		ln := strings.ToLower(n)
		if ln == low {
			return n, nil
		}
		if strings.HasSuffix(ln, "/"+low) {
			matches = append(matches, n)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		hint := "list them with: ucdaw trackpreset list"
		var close []string
		base := strings.ToLower(path.Base(want))
		for _, n := range names {
			if strings.Contains(strings.ToLower(n), base) && len(close) < 6 {
				close = append(close, n)
			}
		}
		if len(close) > 0 {
			hint = "close: " + strings.Join(close, ", ")
		}
		return "", Problem{Message: fmt.Sprintf("no track preset called %q", said), Hint: hint}
	}
	shown := matches
	more := ""
	if len(shown) > 8 {
		more = fmt.Sprintf(", and %d more", len(shown)-8)
		shown = shown[:8]
	}
	return "", Problem{
		Message: fmt.Sprintf("%d track presets are called %q", len(matches), said),
		Hint:    "name its folder too: " + strings.Join(shown, ", ") + more,
	}
}

// channelKeys identifies channels by name and kind, the way summarize does:
// paths renumber as channels come and go.
func channelKeys(cs []Channel) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Name + "|" + c.Kind
	}
	return out
}

// loadTrackPreset loads a saved track preset into the song and reports the
// channels it added. Studio Pro does not answer the command itself, so the
// new channels are the confirmation.
func (st *Studio) loadTrackPreset(ctx context.Context, said string) (Result, error) {
	name, err := pickTrackPreset(said)
	if err != nil {
		return Result{}, err
	}
	if d, open := st.model().Dialog(); open {
		return Result{}, dialogProblem(d)
	}
	before := st.channels()
	seq := st.model().Seq()
	if err := st.session.Invoke(ctx, "Track", "Load Track Preset", map[string]string{"Name": name}); err != nil {
		return Result{}, err
	}
	waitCtx, cancel := context.WithTimeout(ctx, trackPresetWait)
	err = st.session.WaitFor(waitCtx, func(m *Model) bool {
		_, open := m.Dialog()
		return open || len(st.channels()) != len(before)
	})
	cancel()
	if d, open := st.model().Dialog(); open {
		return Result{}, st.dismiss(ctx, d, fmt.Sprintf("Studio Pro opened the %q dialog instead of loading track preset %s", d.Title, name), "")
	}
	if errors.Is(err, ErrConnectionClosed) {
		return Result{}, st.unconfirmed(err, "Loading track preset "+name)
	}
	if err != nil {
		return Result{}, Problem{
			Message: fmt.Sprintf("Track preset %s was sent, but Studio Pro added no channels.", name),
			Hint:    "check Studio Pro: the song may be busy, or the preset may not load in this song",
		}
	}
	// A preset's channels arrive one at a time, each after its plug-ins have
	// loaded; wait until none has come for a while.
	for {
		n := len(st.channels())
		quietCtx, cancel := context.WithTimeout(ctx, trackPresetQuiet)
		err := st.session.WaitFor(quietCtx, func(*Model) bool { return len(st.channels()) != n })
		cancel()
		if err != nil {
			break
		}
	}
	st.settle(ctx, seq)
	after := st.channels()
	added, _ := diffLists(channelKeys(before), channelKeys(after))
	var parts []string
	var channels []Channel
	for _, k := range added {
		chName, kind, _ := strings.Cut(k, "|")
		parts = append(parts, fmt.Sprintf("%s (%s)", chName, kindWord(kind)))
	}
	left := append([]string(nil), added...)
	for _, c := range after {
		for i, k := range left {
			if k == c.Name+"|"+c.Kind {
				channels = append(channels, c)
				left = append(left[:i], left[i+1:]...)
				break
			}
		}
	}
	data := map[string]any{"preset": name, "channels": channels}
	if len(parts) == 0 {
		return Result{Message: fmt.Sprintf("Loaded track preset %s; the song's channels changed, but none were added.", name), Data: data}, nil
	}
	return Result{
		Message: fmt.Sprintf("Loaded track preset %s: added %d channel%s: %s.", name, len(parts), plural(float64(len(parts))), strings.Join(parts, ", ")),
		Data:    data,
	}, nil
}
