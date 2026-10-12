package daw

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Audio edits run Studio Pro's own editing commands on every event of one
// track, proven live on a bounced take:
//
//   - selecting the track's channel (PV selected) selects the track, and
//     Edit / Select All on Tracks selects its events;
//   - Transport / Locate Selection End, Rewind Bar {Bars}, Audio / Fade Out
//     from Cursor fades out the last bars;
//   - Return to Zero, Forward Bar {Bars}, Edit / Split at Cursor splits;
//   - Audio / Create Crossfades joins overlapping events, and
//     Audio / Create Autofades {Length: seconds} puts short fades on every
//     event edge.
//
// Studio Pro does not send audio events to remote apps (checked with a
// capture while bouncing a take: only the new channel arrived), so these
// edits are sent but cannot be read back. Answers say so.

// MaxFadeBars bounds a fade length.
const MaxFadeBars = 64

// notReadBack ends every audio-edit answer.
const notReadBack = " Studio Pro does not report audio events to remotes, so this one is not read back."

func (st *Studio) audio(ctx context.Context, req Request) (Result, error) {
	if d, open := st.model().Dialog(); open {
		return Result{}, dialogProblem(d)
	}
	ch, err := st.resolveChannel(req.Path)
	if err != nil {
		return Result{}, err
	}
	if !strings.HasPrefix(ch.Path, "mixer/channel/") {
		return Result{}, Problem{Message: fmt.Sprintf("%s is not a track", ch.Name)}
	}
	bars := req.Bars
	if math.IsNaN(bars) || bars < 0 || bars != math.Trunc(bars) || bars > MaxFadeBars {
		return Result{}, Problem{Message: fmt.Sprintf("give a whole number of bars from 1 to %d", MaxFadeBars)}
	}
	var steps []step
	var msg string
	switch req.Action {
	case "fade-out", "fadeout":
		if bars == 0 {
			bars = 1
		}
		steps = []step{
			{"Transport", "Locate Selection End", nil},
			{"Transport", "Rewind Bar", map[string]string{"Bars": strconv.Itoa(int(bars))}},
			{"Audio", "Fade Out from Cursor", nil},
		}
		msg = fmt.Sprintf("Faded out the last %s of %s.", barWord(bars), ch.Name)
	case "fade-in", "fadein":
		if bars == 0 {
			bars = 1
		}
		steps = []step{
			{"Transport", "Locate Selection", nil},
			{"Transport", "Forward Bar", map[string]string{"Bars": strconv.Itoa(int(bars))}},
			{"Audio", "Fade In to Cursor", nil},
		}
		msg = fmt.Sprintf("Faded in the first %s of %s.", barWord(bars), ch.Name)
	case "split", "trim-start", "trim-end":
		at, err := barNumber(req.Value)
		if err != nil {
			return Result{}, err
		}
		steps = cursorSteps(at)
		switch req.Action {
		case "split":
			steps = append(steps, step{"Edit", "Split at Cursor", nil})
			msg = fmt.Sprintf("Split %s at bar %d.", ch.Name, at)
		case "trim-start":
			steps = append(steps, step{"Event", "Trim Start to Cursor", nil})
			msg = fmt.Sprintf("Trimmed the start of %s to bar %d.", ch.Name, at)
		default:
			steps = append(steps, step{"Event", "Trim End to Cursor", nil})
			msg = fmt.Sprintf("Trimmed the end of %s to bar %d.", ch.Name, at)
		}
	case "crossfade", "crossfades":
		ms := 10.0
		if req.Seconds > 0 {
			ms = req.Seconds * 1000
		}
		if ms > 5000 {
			return Result{}, Problem{Message: "a crossfade is at most 5 seconds"}
		}
		steps = []step{
			{"Audio", "Create Crossfades", nil},
			{"Audio", "Create Autofades", map[string]string{"Length": strconv.FormatFloat(ms/1000, 'f', -1, 64)}},
		}
		msg = fmt.Sprintf("Crossfaded the joins in %s, with %s ms fades on every edit.", ch.Name, trim(ms))
	case "quantize":
		steps = []step{{"Event", "Quantize", nil}}
		msg = fmt.Sprintf("Quantized %s to the quantize grid.", ch.Name)
	default:
		return Result{}, Problem{
			Message: fmt.Sprintf("unknown audio edit %q", req.Action),
			Hint:    "use fade-out, fade-in, split, trim-start, trim-end, crossfade or quantize",
		}
	}
	if err := st.selectTrackEvents(ctx, ch); err != nil {
		return Result{}, err
	}
	seq := st.model().Seq()
	if err := st.invokeAll(ctx, steps...); err != nil {
		return Result{}, err
	}
	st.settle(ctx, seq)
	if d, open := st.model().Dialog(); open {
		return Result{}, st.dismiss(ctx, d, fmt.Sprintf("Studio Pro opened the %q dialog during the edit", d.Title), "")
	}
	return Result{Message: msg + notReadBack, Data: map[string]any{"channel": ch.Path, "commands": stepNames(steps)}}, nil
}

// selectTrackEvents selects one track (exclusively, as clicking it does) and
// all its events.
func (st *Studio) selectTrackEvents(ctx context.Context, ch Channel) error {
	if err := st.session.SetNum(ctx, ch.Path+"/selected", 1); err != nil {
		return st.unconfirmed(err, "Selecting "+ch.Name)
	}
	return st.invokeAll(ctx, step{"Edit", "Select All on Tracks", nil})
}

func cursorSteps(bar int) []step {
	steps := []step{{"Transport", "Return to Zero", nil}}
	if bar > 1 {
		steps = append(steps, step{"Transport", "Forward Bar", map[string]string{"Bars": strconv.Itoa(bar - 1)}})
	}
	return steps
}

func barNumber(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "bar")))
	if err != nil || n < 1 || n > 9999 {
		return 0, Problem{Message: fmt.Sprintf("give a bar number from 1, not %q", s)}
	}
	return n, nil
}

func barWord(n float64) string {
	if n == 1 {
		return "bar"
	}
	return fmt.Sprintf("%s bars", trim(n))
}

func stepNames(steps []step) []string {
	out := make([]string, len(steps))
	for i, s := range steps {
		out[i] = s.category + " > " + s.name
	}
	return out
}
