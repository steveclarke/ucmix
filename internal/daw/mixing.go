package daw

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Mixing: the input trim (Studio Pro's channel Input Gain), measuring the
// stems on tracks, and gain staging.
//
// Input gain is mixer/channel/chN/inputfx/gain, range -24..+24 dB, carried
// on the wire like every ranged value: normalized 0..1 on a straight line,
// so 0.5 is 0 dB and 44/48 is +20 dB (read back live from Studio Pro 8.0.2).
// It sits before the inserts and the fader, which is where gain staging
// belongs: the fader stays free for the balance.

func trimPath(ch Channel) string { return ch.Path + "/inputfx/gain" }

// trimDB reads a channel's input trim in dB.
func (st *Studio) trimDB(ch Channel) (float64, bool, error) {
	p, err := st.param(trimPath(ch))
	if err != nil {
		return 0, false, err
	}
	wire, ok := st.model().Num(p.Path)
	if !ok {
		return 0, false, nil
	}
	return math.Round(p.FromWire(wire)*10) / 10, true, nil
}

// setTrim writes a trim in dB and reads it back.
func (st *Studio) setTrim(ctx context.Context, ch Channel, db float64) (float64, error) {
	p, err := st.param(trimPath(ch))
	if err != nil {
		return 0, err
	}
	db = math.Round(db*10) / 10
	if math.IsNaN(db) || db < TrimMin || db > TrimMax {
		return 0, Problem{Message: fmt.Sprintf("the input trim goes from -24 to +24 dB, not %s", signedDB(db))}
	}
	wire, err := p.ToWire(db)
	if err != nil {
		return 0, Problem{Message: err.Error()}
	}
	if err := st.session.SetNum(ctx, p.Path, wire); err != nil {
		return 0, st.unconfirmed(err, "Setting "+ch.Name+"'s trim")
	}
	after, _, err := st.trimDB(ch)
	return after, err
}

// trim reads or sets a channel's input trim in dB: "set" to a level, "up"
// or "down" by an amount, or read it.
func (st *Studio) trim(ctx context.Context, req Request) (Result, error) {
	ch, err := st.resolveChannel(req.Path)
	if err != nil {
		return Result{}, err
	}
	before, ok, err := st.trimDB(ch)
	if err != nil {
		return Result{}, err
	}
	if !ok {
		return Result{}, Problem{Message: fmt.Sprintf("Studio Pro has not reported %s's input trim", ch.Name), Hint: "only tracks and input channels have one"}
	}
	data := func(db float64) map[string]any { return map[string]any{"channel": ch.Path, "trimDB": db} }
	value := strings.TrimSpace(strings.TrimSuffix(strings.ToLower(strings.TrimSpace(req.Value)), "db"))
	if req.Action == "" && value == "" || req.Action == "get" {
		return Result{Message: fmt.Sprintf("%s trim is at %s.", ch.Name, signedDB(before)), Data: data(before)}, nil
	}
	amount, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(amount) || math.IsInf(amount, 0) {
		return Result{}, Problem{Message: fmt.Sprintf("a trim is a number of dB, not %q", req.Value)}
	}
	target := amount
	switch req.Action {
	case "up", "down":
		if amount <= 0 || amount > 48 {
			return Result{}, Problem{Message: fmt.Sprintf("say how many dB to move %s, e.g. 3", req.Action)}
		}
		if req.Action == "down" {
			amount = -amount
		}
		target = before + amount
	case "set":
	default:
		return Result{}, Problem{Message: fmt.Sprintf("unknown trim action %q", req.Action)}
	}
	if target < TrimMin-1e-9 || target > TrimMax+1e-9 {
		return Result{}, Problem{
			Message: fmt.Sprintf("%s trim is at %s; the input trim goes from -24 to +24 dB", ch.Name, signedDB(before)),
			Hint:    "for more, use the fader too",
		}
	}
	after, err := st.setTrim(ctx, ch, target)
	if err != nil {
		return Result{}, err
	}
	return Result{Message: fmt.Sprintf("%s trim %s (was %s).", ch.Name, signedDB(after), signedDB(before)), Data: data(after)}, nil
}

// audioTracks are the song's audio tracks, or the named ones.
func (st *Studio) mixTracks(refs []string) ([]Channel, error) {
	if len(refs) == 0 {
		var out []Channel
		for _, ch := range st.channels() {
			if strings.HasPrefix(ch.Path, "mixer/channel/") && ch.Kind == "AudioTrack" {
				out = append(out, ch)
			}
		}
		if len(out) == 0 {
			return nil, Problem{Message: "the song has no audio tracks to measure"}
		}
		return out, nil
	}
	out := make([]Channel, 0, len(refs))
	for _, r := range refs {
		ch, err := st.resolveChannel(r)
		if err != nil {
			return nil, err
		}
		out = append(out, ch)
	}
	return out, nil
}

// trackLevels measures the stem (or stems) on a track.
func (st *Studio) trackLevels(ctx context.Context, ch Channel, target float64) (Levels, error) {
	files, err := st.stemFiles(ctx, ch)
	if err != nil {
		return Levels{}, err
	}
	all, err := analyzeAll(ctx, files, nil, target)
	if err != nil {
		return Levels{}, err
	}
	for _, lv := range all {
		if lv.Seconds == 0 && lv.PeakDBFS == nil && !lv.Silent {
			return Levels{}, Problem{Message: strings.TrimSuffix(lv.Line, ".")}
		}
	}
	lv := mergeLevels(all, target)
	lv.Name = ch.Name
	lv.Line = levelsLine(lv)
	return lv, nil
}

// mergeLevels combines the stems on one track: the average is weighted by
// each stem's active time, the peak is the highest. Integrated loudness is
// not additive, so it is left out when there is more than one file.
func mergeLevels(all []Levels, target float64) Levels {
	if len(all) == 1 {
		return all[0]
	}
	out := Levels{File: fmt.Sprintf("%d files", len(all)), TargetDBFS: target, Silent: true}
	var power, active float64
	var names []string
	for _, lv := range all {
		names = append(names, lv.File)
		out.Seconds += lv.Seconds
		out.ActiveSeconds += lv.ActiveSeconds
		out.ClippedSamples += lv.ClippedSamples
		out.Clipping = out.Clipping || lv.Clipping
		out.Partial = out.Partial || lv.Partial
		out.Silent = out.Silent && lv.Silent
		if lv.PeakDBFS != nil && (out.PeakDBFS == nil || *lv.PeakDBFS > *out.PeakDBFS) {
			out.PeakDBFS = ptr(*lv.PeakDBFS)
		}
		if lv.AverageDBFS != nil && !lv.Silent {
			power += math.Pow(10, *lv.AverageDBFS/10) * lv.ActiveSeconds
			active += lv.ActiveSeconds
		}
	}
	out.Path = strings.Join(names, ", ")
	if active > 0 && !out.Silent {
		avg := 10 * math.Log10(power/active)
		out.AverageDBFS = ptr(round2(avg))
		trim, needed, limited := suggestTrim(avg, target)
		out.TrimDB, out.TrimLimited = ptr(trim), limited
		if limited {
			out.NeededDB = ptr(needed)
		}
	}
	return out
}

// analyze measures files and the stems on tracks; with neither, every audio
// track in the song. Read-only: nothing is sent to Studio Pro.
func (st *Studio) analyze(ctx context.Context, req Request) (Result, error) {
	target := req.TargetDBFS
	if target == 0 {
		target = DefaultTarget
	}
	if err := CheckTarget(target); err != nil {
		return Result{}, err
	}
	var all []Levels
	var skips []skipped
	if len(req.Files) > 0 {
		files, sk, err := collectAnalyzable(req.Files)
		if err != nil {
			return Result{}, err
		}
		skips = sk
		levels, err := analyzeAll(ctx, files, trackNames(files), target)
		if err != nil {
			return Result{}, err
		}
		all = append(all, levels...)
	}
	if len(req.Tracks) > 0 || len(req.Files) == 0 {
		channels, err := st.mixTracks(req.Tracks)
		if err != nil {
			return Result{}, err
		}
		for _, ch := range channels {
			lv, err := st.trackLevels(ctx, ch, target)
			if err != nil {
				if len(req.Tracks) > 0 {
					return Result{}, err
				}
				// Measuring the whole song: a track without a stem is a
				// line, not a failure.
				all = append(all, Levels{Name: ch.Name, TargetDBFS: target, Line: fmt.Sprintf("%s: %s.", ch.Name, problemText(err))})
				continue
			}
			all = append(all, lv)
		}
	}
	if len(all) == 0 {
		return Result{}, Problem{Message: "nothing to measure", Hint: "ucdaw measures WAV and AIFF files"}
	}
	return levelsResult(all, skips, target), nil
}

// gainStage sets each track's input trim so its stem averages the target.
// Faders, mutes and pans are never touched: the musician's balance stays as
// it is. A silent stem, or a track without one, is left alone and named.
func (st *Studio) gainStage(ctx context.Context, req Request) (Result, error) {
	target := req.TargetDBFS
	if target == 0 {
		target = DefaultTarget
	}
	if err := CheckTarget(target); err != nil {
		return Result{}, err
	}
	channels, err := st.mixTracks(req.Tracks)
	if err != nil {
		return Result{}, err
	}
	faders := map[string]float64{}
	for _, ch := range channels {
		if v, ok := st.model().Num(ch.Path + "/volume"); ok {
			faders[ch.Path] = v
		}
	}
	type staged struct {
		Track       string   `json:"track"`
		Channel     string   `json:"channel"`
		Before      *float64 `json:"trimBeforeDB"`
		After       *float64 `json:"trimAfterDB"`
		AverageDBFS *float64 `json:"averageDBFS"`
		ShortDB     float64  `json:"shortDB,omitempty"`
		Skipped     string   `json:"skipped,omitempty"`
		Line        string   `json:"line"`
	}
	var rows []staged
	var lines []string
	set := 0
	for _, ch := range channels {
		row := staged{Track: ch.Name, Channel: ch.Path}
		lv, err := st.trackLevels(ctx, ch, target)
		switch {
		case err != nil:
			row.Skipped = problemText(err)
			row.Line = fmt.Sprintf("%s: left alone, %s.", ch.Name, row.Skipped)
		case lv.Silent:
			row.Skipped = "silent"
			row.Line = fmt.Sprintf("%s: left alone, %s", ch.Name, strings.TrimPrefix(lv.Line, ch.Name+" is "))
		case lv.TrimDB == nil:
			row.Skipped = "nothing to measure"
			row.Line = fmt.Sprintf("%s: left alone, nothing to measure.", ch.Name)
		default:
			row.AverageDBFS = lv.AverageDBFS
			before, ok, err := st.trimDB(ch)
			if err != nil || !ok {
				row.Skipped = "no input trim reported"
				row.Line = fmt.Sprintf("%s: left alone, Studio Pro has not reported its input trim.", ch.Name)
				break
			}
			row.Before = ptr(before)
			want := *lv.TrimDB
			after := before
			if math.Abs(want-before) >= 0.05 {
				if after, err = st.setTrim(ctx, ch, want); err != nil {
					return Result{}, err
				}
			}
			row.After = ptr(after)
			set++
			note := fmt.Sprintf("stem averages %s", dbfs(*lv.AverageDBFS))
			if lv.TrimLimited && lv.NeededDB != nil {
				row.ShortDB = math.Round(math.Abs(*lv.NeededDB-after)*10) / 10
				note += fmt.Sprintf("; %s dB short of %s, the most the trim gives", trim(row.ShortDB), dbfs(target))
			}
			if lv.Clipping {
				note += "; the export clips"
			}
			if after == before {
				row.Line = fmt.Sprintf("%s: trim already %s (%s).", ch.Name, signedDB(after), note)
			} else {
				row.Line = fmt.Sprintf("%s: trim %s → %s (%s).", ch.Name, signed(before), signedDB(after), note)
			}
		}
		rows = append(rows, row)
		lines = append(lines, row.Line)
	}
	moved := []string{}
	for path, v := range faders {
		if now, ok := st.model().Num(path + "/volume"); ok && now != v {
			moved = append(moved, path)
		}
	}
	head := fmt.Sprintf("Gain-staged %d %s to a %s average; faders untouched", set, trackWord(set), dbfs(target))
	if len(moved) > 0 {
		head = fmt.Sprintf("Gain-staged %d %s to a %s average; a fader moved during it (%s), check the balance", set, trackWord(set), dbfs(target), strings.Join(moved, ", "))
	}
	return Result{
		Message: head + ":\n" + strings.Join(lines, "\n"),
		Data:    map[string]any{"targetDBFS": target, "tracks": rows},
	}, nil
}

func trackWord(n int) string {
	if n == 1 {
		return "track"
	}
	return "tracks"
}

func problemText(err error) string {
	return strings.TrimSuffix(lowerFirst(Problem{Message: err.Error()}.Sentence()), ".")
}
