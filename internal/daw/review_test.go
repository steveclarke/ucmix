package daw

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/steveclarke/ucmix/internal/proto"
)

func TestPressReleasesEvenWhenCancelledMidHold(t *testing.T) {
	fake := newFake(t)
	s := dialFake(t, fake)
	ctx, cancel := context.WithCancel(context.Background())
	sleepFor = func(context.Context, time.Duration) error { cancel(); return context.Canceled }
	_ = s.Press(ctx, "transport/start", 1)
	fake.mu.Lock()
	last := fake.sent[len(fake.sent)-1]
	fake.mu.Unlock()
	key, state, err := proto.UnmarshalPE(last.Payload)
	if err != nil || key != "transport/start" || state != 0 {
		t.Errorf("last frame = %s %d %v, want the release", key, state, err)
	}
}

func TestTimedPlayStopsWhenStartIsNotConfirmed(t *testing.T) {
	st, fake := newStudio(t)
	orig := stopWait
	stopWait = 20 * time.Millisecond
	t.Cleanup(func() { stopWait = orig })
	fake.mu.Lock()
	fake.ignoreStart = true
	fake.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := st.Do(ctx, Request{Op: "transport", Action: "play", Seconds: 1})
	if err == nil {
		t.Fatal("want an error")
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	stops := 0
	for _, b := range fake.batches {
		if k, _, _ := proto.UnmarshalPV(b[1].Payload); k == "transport/stop" {
			stops++
		}
	}
	if stops == 0 {
		t.Error("no Stop after a failed timed play")
	}
}

func TestPlayRejectsNegativeAndNaNLengths(t *testing.T) {
	st, _ := newStudio(t)
	for _, req := range []Request{
		{Op: "transport", Action: "play", Seconds: -1},
		{Op: "transport", Action: "play", Bars: -2},
		{Op: "transport", Action: "play", Seconds: math.NaN()},
	} {
		if _, err := st.Do(context.Background(), req); err == nil {
			t.Errorf("%+v accepted", req)
		}
	}
	if _, err := st.Do(context.Background(), Request{Op: "set", Path: "transport/tempo", Value: "NaN"}); err == nil {
		t.Error("NaN tempo accepted")
	}
}

func TestSummaryMatchesChannelsByNameNotPath(t *testing.T) {
	st, _ := newStudio(t)
	before := []Channel{
		{Path: "mixer/channel/ch1", Name: "A", Kind: "AudioTrack"},
		{Path: "mixer/channel/ch2", Name: "B", Kind: "AudioTrack"},
		{Path: "mixer/channel/ch3", Name: "C", Kind: "AudioTrack", Inserts: []string{"Pro EQ"}},
	}
	// The song now has A and C (C renumbered to ch2), as after deleting B.
	st.model().mutate(func(r func(string)) {
		st.model().setNum("mixer/channel/count", 0.002, r)
		st.model().setStr("mixer/channel/ch1/label", "A", r)
		st.model().setStr("mixer/channel/ch1/type", "AudioTrack", r)
		st.model().setStr("mixer/channel/ch2/label", "C", r)
		st.model().setStr("mixer/channel/ch2/type", "AudioTrack", r)
		st.model().setNum("mixer/channel/ch2/inserts/count", 0.001, r)
		st.model().setStr("mixer/channel/ch2/inserts/slot1/deviceName", "Pro EQ", r)
	})
	if got := st.summarize(before, nil); got != "removed B" {
		t.Errorf("summary = %q", got)
	}
}

func TestUnchangedValuesAreNotChanges(t *testing.T) {
	m := NewModel()
	_ = m.ApplyFrame(proto.Frame{Code: proto.CodePV, Payload: proto.MarshalPV("a", 1)})
	seq := m.Seq()
	_ = m.ApplyFrame(proto.Frame{Code: proto.CodePV, Payload: proto.MarshalPV("a", 1)})
	if got := m.ChangesSince(seq); len(got) != 0 {
		t.Errorf("changes = %v", got)
	}
	if !strings.Contains(strings.Join(m.Paths(""), ","), "a") {
		t.Error("value lost")
	}
}
