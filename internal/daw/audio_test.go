package daw

import (
	"context"
	"strings"
	"testing"
)

func newTakeStudio(t *testing.T) (*Studio, *fakeArranger) {
	t.Helper()
	st, arr := newArrangerStudio(t)
	st.session.Model().mutate(func(r func(string)) {
		st.session.Model().setNum("mixer/channel/count", 0.001, r)
		st.session.Model().setStr("mixer/channel/ch1/label", "Take", r)
		st.session.Model().setStr("mixer/channel/ch1/type", "AudioTrack", r)
	})
	return st, arr
}

func TestAudioEditsRunTheirCommands(t *testing.T) {
	cases := []struct {
		req  Request
		msg  string
		cmds string
	}{
		{Request{Action: "fade-out"}, "Faded out the last bar of Take.", "Edit>Select All on Tracks,Transport>Locate Selection End,Transport>Rewind Bar,Audio>Fade Out from Cursor"},
		{Request{Action: "fade-in", Bars: 2}, "Faded in the first 2 bars of Take.", "Edit>Select All on Tracks,Transport>Locate Selection,Transport>Forward Bar,Audio>Fade In to Cursor"},
		{Request{Action: "split", Value: "bar 3"}, "Split Take at bar 3.", "Edit>Select All on Tracks,Transport>Return to Zero,Transport>Forward Bar,Edit>Split at Cursor"},
		{Request{Action: "trim-end", Value: "1"}, "Trimmed the end of Take to bar 1.", "Edit>Select All on Tracks,Transport>Return to Zero,Event>Trim End to Cursor"},
		{Request{Action: "crossfade"}, "Crossfaded the joins in Take, with 10 ms fades on every edit.", "Edit>Select All on Tracks,Audio>Create Crossfades,Audio>Create Autofades"},
		{Request{Action: "quantize"}, "Quantized Take to the quantize grid.", "Edit>Select All on Tracks,Event>Quantize"},
	}
	for _, c := range cases {
		st, arr := newTakeStudio(t)
		c.req.Op, c.req.Path = "audio", "Take"
		got := do(t, st, c.req)
		if got != c.msg+notReadBack {
			t.Errorf("%s = %q", c.req.Action, got)
		}
		if log := strings.Join(arr.log, ","); log != c.cmds {
			t.Errorf("%s commands = %s", c.req.Action, log)
		}
		if v, _ := st.model().Num("mixer/channel/ch1/selected"); v != 1 {
			t.Errorf("%s did not select the track", c.req.Action)
		}
	}
}

func TestAudioEditsRejectBadInput(t *testing.T) {
	st, _ := newTakeStudio(t)
	for _, req := range []Request{
		{Op: "audio", Action: "fade-out", Path: "Nope"},
		{Op: "audio", Action: "fade-out", Path: "Take", Bars: 0.5},
		{Op: "audio", Action: "fade-out", Path: "Take", Bars: 500},
		{Op: "audio", Action: "split", Path: "Take", Value: "zero"},
		{Op: "audio", Action: "split", Path: "Take", Value: "0"},
		{Op: "audio", Action: "wobble", Path: "Take"},
		{Op: "audio", Action: "crossfade", Path: "Take", Seconds: 9},
	} {
		if _, err := st.Do(context.Background(), req); err == nil {
			t.Errorf("%+v accepted", req)
		}
	}
}
