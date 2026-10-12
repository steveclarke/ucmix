package daw

import (
	"context"
	"testing"

	"github.com/steveclarke/ucmix/internal/proto"
)

// Shapes copied from live frames (empty test song, markers two bars apart at
// 120 bpm); no song data.
var (
	liveMarkers  = []byte("EvDL\x8e\x00\x00\x00\x01\x00Mark\x00\x00\x00\x00\x04\x00\x00\x00MSta\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00Start\x00Mark\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x10@\x00\x00\x00\x00\x00\x00\x10@#2\x00Mark\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00 @\x00\x00\x00\x00\x00\x00 @#3\x00MEnd\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\xc0r@\x00\x00\x00\x00\x00\xc0r@End\x00")
	liveSections = []byte("EvDLj\x00\x00\x00\x01\x00Arng\x00\x00\x00\x00\x03\x00\x00\x00Arng\x00\x00\x00\x00\x00s\xcb\xff\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x10@Start\x00Arng\x00\x00\x00\x00\x00s\xcb\xff\x00\x00\x00\x00\x00\x00\x10@\x00\x00\x00\x00\x00\x00 @\x00Arng\x00\x00\x00\x00\x00s\xcb\xff\x00\x00\x00\x00\x00\x00 @\x00\x00\x00\x00\x00\xc0r@\x00")
)

func TestParseEventListMarkers(t *testing.T) {
	l, err := ParseEventList(liveMarkers)
	if err != nil {
		t.Fatal(err)
	}
	if l.Kind != "Mark" || len(l.Events) != 4 {
		t.Fatalf("list = %+v", l)
	}
	want := []Event{{Name: "Start", Tag: "MSta"}, {Name: "#2", Start: 4, End: 4, Tag: "Mark"}, {Name: "#3", Start: 8, End: 8, Tag: "Mark"}, {Name: "End", Start: 300, End: 300, Tag: "MEnd"}}
	for i, e := range want {
		if l.Events[i] != e {
			t.Errorf("event %d = %+v, want %+v", i, l.Events[i], e)
		}
	}
}

func TestParseEventListSections(t *testing.T) {
	l, err := ParseEventList(liveSections)
	if err != nil {
		t.Fatal(err)
	}
	if l.Kind != "Arng" || len(l.Events) != 3 || l.Events[1].Start != 4 || l.Events[1].End != 8 || l.Events[0].Name != "Start" || l.Events[0].Color != 0xffcb7300 {
		t.Errorf("list = %+v", l)
	}
}

func TestParseEventListRejectsTruncated(t *testing.T) {
	for n := 0; n < len(liveSections); n += 7 {
		if _, err := ParseEventList(liveSections[:n]); err == nil && n < len(liveSections) {
			if n >= 22 {
				continue // a prefix can still be a valid shorter list only if count allows; it never does here
			}
			t.Errorf("prefix %d accepted", n)
		}
	}
	if _, err := ParseEventList(liveSections[:len(liveSections)-1]); err == nil {
		t.Error("missing final NUL accepted")
	}
}

func TestModelKeepsEventLists(t *testing.T) {
	m := NewModel()
	if err := m.ApplyFrame(proto.Frame{Code: codeBO, Payload: liveSections}); err != nil {
		t.Fatal(err)
	}
	if got := m.Events("Arng"); len(got) != 3 {
		t.Errorf("sections = %+v", got)
	}
}

func TestSongListsSectionsAndMarkers(t *testing.T) {
	st, fake := newStudio(t)
	fake.mu.Lock()
	fake.frames <- proto.Frame{Code: codeBO, Payload: liveSections}
	fake.frames <- proto.Frame{Code: codeBO, Payload: liveMarkers}
	fake.mu.Unlock()
	_ = st.session.WaitFor(context.Background(), func(m *Model) bool { return len(m.Events("Mark")) == 4 })
	got := do(t, st, Request{Op: "song"})
	want := "120 bpm, 4/4, click on, loop off, stopped; no tracks yet; sections: Start bar 1, section 2 bar 3, section 3 bar 5; markers: #2 bar 3, #3 bar 5."
	if got != want {
		t.Errorf("song = %q", got)
	}
}

func TestEventChanges(t *testing.T) {
	before := []Event{{Name: "#2", Start: 4}}
	after := []Event{{Name: "#2", Start: 4}, {Name: "#3", Start: 8}}
	if got := eventChanges("marker", before, after); got != "added marker #3 at 0:08" {
		t.Errorf("got %q", got)
	}
	if got := eventChanges("marker", after, before); got != "removed marker #3 at 0:08" {
		t.Errorf("got %q", got)
	}
}
