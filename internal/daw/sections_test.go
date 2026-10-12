package daw

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/steveclarke/ucmix/internal/proto"
)

// encodeEventList builds a BO "EvDL" payload in the layout Studio Pro sends.
func encodeEventList(kind string, events []Event) []byte {
	var body []byte
	u32 := func(v uint32) { body = binary.LittleEndian.AppendUint32(body, v) }
	f64 := func(v float64) { body = binary.LittleEndian.AppendUint64(body, math.Float64bits(v)) }
	body = binary.LittleEndian.AppendUint16(body, 1)
	body = append(body, kind...)
	u32(0)
	u32(uint32(len(events)))
	for _, e := range events {
		body = append(body, kind...)
		u32(0)
		u32(0xffcb7300)
		f64(e.Start)
		f64(e.End)
		body = append(body, e.Name...)
		body = append(body, 0)
	}
	out := []byte("EvDL")
	out = binary.LittleEndian.AppendUint32(out, uint32(len(body)))
	return append(out, body...)
}

// fakeArranger behaves like Studio Pro's arranger track as observed live, at
// 120 bpm in 4/4 (two seconds a bar).
type fakeArranger struct {
	sections   []Event
	selected   int // 1-based, 0 for none
	cursor     float64
	clip       *Event
	rangeStart float64
	rangeEnd   float64
	log        []string
	ignore     bool // a Studio Pro that does nothing
	// deleteLast makes Delete Range remove the last section, not the
	// selected one: the count comes out right, the arrangement does not.
	deleteLast bool
	// pasteAs makes Paste insert a section with this name instead of the
	// one copied.
	pasteAs string
	// noShift makes an insert leave later sections where they were.
	noShift bool
	// renameFirst makes Rename Events rename section 1 as well.
	renameFirst bool
}

func (a *fakeArranger) frame() []proto.Frame {
	return []proto.Frame{{Code: codeBO, Payload: encodeEventList("Arng", a.sections)}}
}

func (a *fakeArranger) insert(at float64, e Event) {
	length := e.End - e.Start
	for i := range a.sections {
		if !a.noShift && a.sections[i].Start >= at-1e-6 {
			a.sections[i].Start += length
			a.sections[i].End += length
		}
	}
	a.sections = append(a.sections, Event{Name: e.Name, Start: at, End: at + length})
	sort.Slice(a.sections, func(i, j int) bool { return a.sections[i].Start < a.sections[j].Start })
}

func (a *fakeArranger) invoke(category, name string, args map[string]string) []proto.Frame {
	a.log = append(a.log, category+">"+name)
	if a.ignore {
		return nil
	}
	switch category + "|" + name {
	case "Arranger|Goto Section":
		n, _ := strconv.Atoi(args["Number"])
		if n >= 1 && n <= len(a.sections) {
			a.selected = n
			a.cursor = a.sections[n-1].Start
		}
	case "Transport|Return to Zero":
		a.cursor = 0
	case "Transport|Forward Bar":
		n, _ := strconv.Atoi(args["Bars"])
		a.cursor += float64(n) * 2
	case "Edit|Duplicate":
		if a.selected > 0 {
			s := a.sections[a.selected-1]
			a.insert(s.End, s)
			return a.frame()
		}
	case "Edit|Copy":
		if a.selected > 0 {
			s := a.sections[a.selected-1]
			a.clip = &s
		}
	case "Edit|Paste":
		if a.clip != nil {
			e := *a.clip
			if a.pasteAs != "" {
				e.Name = a.pasteAs
			}
			a.insert(a.cursor, e)
			return a.frame()
		}
	case "Arranger|Delete Range":
		if a.deleteLast && a.selected > 0 {
			a.selected = len(a.sections)
		}
		if a.selected > 0 {
			s := a.sections[a.selected-1]
			a.sections = append(a.sections[:a.selected-1], a.sections[a.selected:]...)
			for i := range a.sections {
				if a.sections[i].Start >= s.End-1e-6 {
					a.sections[i].Start -= s.End - s.Start
					a.sections[i].End -= s.End - s.Start
				}
			}
			a.selected = 0
			return a.frame()
		}
	case "Event|Rename Events":
		if a.renameFirst && len(a.sections) > 0 {
			a.sections[0].Name = args["New Name"]
		}
		if a.selected > 0 {
			a.sections[a.selected-1].Name = args["New Name"]
			return a.frame()
		}
	case "Edit|Create Range from Cursor":
		a.rangeStart = a.cursor
	case "Edit|Range End to Cursor":
		a.rangeEnd = a.cursor
	case "Arranger|Add Arranger Section from Selection":
		a.sections = append(a.sections, Event{Name: "Intro", Start: a.rangeStart, End: a.rangeEnd})
		sort.Slice(a.sections, func(i, j int) bool { return a.sections[i].Start < a.sections[j].Start })
		return a.frame()
	}
	return nil
}

func newArrangerStudio(t *testing.T, names ...string) (*Studio, *fakeArranger) {
	t.Helper()
	st, fake := newStudio(t)
	arr := &fakeArranger{}
	for i, n := range names {
		arr.sections = append(arr.sections, Event{Name: n, Start: float64(i) * 8, End: float64(i+1) * 8})
	}
	fake.mu.Lock()
	fake.onInvoke = arr.invoke
	if len(names) > 0 {
		fake.frames <- arr.frame()[0]
	}
	fake.mu.Unlock()
	_ = st.session.WaitFor(context.Background(), func(m *Model) bool { return len(m.Events("Arng")) == len(names) })
	return st, arr
}

func sectionNamesOf(st *Studio) string {
	var out []string
	for _, s := range st.sectionList() {
		out = append(out, s.Name+"@"+trim(s.StartBar))
	}
	return strings.Join(out, " ")
}

func TestSectionList(t *testing.T) {
	st, _ := newArrangerStudio(t, "Verse", "Chorus", "Bridge")
	got := do(t, st, Request{Op: "section", Action: "list"})
	if got != "3 sections: Verse bar 1 (4 bars), Chorus bar 5 (4 bars), Bridge bar 9 (4 bars)." {
		t.Errorf("list = %q", got)
	}
	st2, _ := newArrangerStudio(t)
	if got := do(t, st2, Request{Op: "section"}); got != "The song has no arranger sections." {
		t.Errorf("empty list = %q", got)
	}
}

func TestResolveSection(t *testing.T) {
	all := []Section{{Number: 1, Name: "Verse"}, {Number: 2, Name: "Chorus"}, {Number: 3, Name: "Bridge"}, {Number: 4, Name: "Chorus"}}
	cases := map[string]int{
		"Verse": 1, "verse": 1, "3": 3, "#2": 2, "Chorus 2": 4, "chorus one": 2, "second chorus": 4,
		"last chorus": 4, "first chorus": 2, "the bridge": 3, "Chorus 1": 2,
	}
	for ref, want := range cases {
		got, err := resolveSection(all, ref)
		if err != nil || got.Number != want {
			t.Errorf("%q = %d, %v; want %d", ref, got.Number, err, want)
		}
	}
	for _, ref := range []string{"Chorus", "Outro", "9", "0", "Chorus 3", "", "the chorus"} {
		if _, err := resolveSection(all, ref); err == nil {
			t.Errorf("%q resolved, want an error", ref)
		}
	}
	_, err := resolveSection(all, "Chorus")
	if p, ok := err.(Problem); !ok || !strings.Contains(p.Hint, "Chorus 2 (") {
		t.Errorf("ambiguous hint = %v", err)
	}
	if _, err := resolveSection(nil, "Verse"); err == nil {
		t.Error("no sections should be an error")
	}
}

func TestSectionDuplicate(t *testing.T) {
	st, _ := newArrangerStudio(t, "Verse", "Chorus", "Bridge")
	got := do(t, st, Request{Op: "section", Action: "duplicate", Section: "chorus"})
	if got != "Duplicated Chorus; the copy starts at bar 9 and the rest moved 4 bars later." {
		t.Errorf("duplicate = %q", got)
	}
	if s := sectionNamesOf(st); s != "Verse@1 Chorus@5 Chorus@9 Bridge@13" {
		t.Errorf("sections = %s", s)
	}
}

func TestSectionRepeatAfter(t *testing.T) {
	st, _ := newArrangerStudio(t, "Verse", "Chorus", "Bridge", "Outro")
	got := do(t, st, Request{Op: "section", Action: "repeat", Section: "Chorus", Target: "Bridge"})
	if got != "Repeated Chorus after Bridge, at bar 13; the rest moved 4 bars later." {
		t.Errorf("repeat = %q", got)
	}
	if s := sectionNamesOf(st); s != "Verse@1 Chorus@5 Bridge@9 Chorus@13 Outro@17" {
		t.Errorf("sections = %s", s)
	}
}

func TestSectionRepeatAfterLastUsesBars(t *testing.T) {
	st, arr := newArrangerStudio(t, "Verse", "Chorus", "Bridge")
	got := do(t, st, Request{Op: "section", Action: "repeat", Section: "Chorus", Target: "Bridge"})
	if got != "Repeated Chorus after Bridge, at bar 13." {
		t.Errorf("repeat = %q", got)
	}
	if s := sectionNamesOf(st); s != "Verse@1 Chorus@5 Bridge@9 Chorus@13" {
		t.Errorf("sections = %s", s)
	}
	if !strings.Contains(strings.Join(arr.log, ","), "Transport>Forward Bar") {
		t.Errorf("log = %v", arr.log)
	}
}

func TestSectionMove(t *testing.T) {
	st, _ := newArrangerStudio(t, "Verse", "Chorus", "Bridge")
	got := do(t, st, Request{Op: "section", Action: "move", Section: "Bridge", Where: "before", Target: "Chorus"})
	if got != "Moved Bridge before Chorus; it now starts at bar 5 (undo takes two steps)." {
		t.Errorf("move = %q", got)
	}
	if s := sectionNamesOf(st); s != "Verse@1 Bridge@5 Chorus@9" {
		t.Errorf("sections = %s", s)
	}
	got = do(t, st, Request{Op: "section", Action: "move", Section: "Verse", Where: "after", Target: "Chorus"})
	if got != "Moved Verse after Chorus; it now starts at bar 9 (undo takes two steps)." {
		t.Errorf("move after = %q", got)
	}
	if s := sectionNamesOf(st); s != "Bridge@1 Chorus@5 Verse@9" {
		t.Errorf("sections = %s", s)
	}
	if got := do(t, st, Request{Op: "section", Action: "move", Section: "Chorus", Where: "after", Target: "Bridge"}); got != "Chorus is already after Bridge." {
		t.Errorf("no-op move = %q", got)
	}
	if _, err := st.Do(context.Background(), Request{Op: "section", Action: "move", Section: "Chorus", Where: "after", Target: "Chorus"}); err == nil {
		t.Error("moving a section after itself should fail")
	}
}

func TestSectionDelete(t *testing.T) {
	st, _ := newArrangerStudio(t, "Verse", "Chorus", "Bridge")
	got := do(t, st, Request{Op: "section", Action: "delete", Section: "Chorus"})
	if got != "Deleted Chorus (bar 5, 4 bars); the rest moved up to close the gap." {
		t.Errorf("delete = %q", got)
	}
	if s := sectionNamesOf(st); s != "Verse@1 Bridge@5" {
		t.Errorf("sections = %s", s)
	}
}

func TestSectionRename(t *testing.T) {
	st, _ := newArrangerStudio(t, "Start", "Chorus")
	if got := do(t, st, Request{Op: "section", Action: "rename", Section: "Start", Name: "Verse"}); got != "Renamed Start to Verse." {
		t.Errorf("rename = %q", got)
	}
	if got := do(t, st, Request{Op: "section", Action: "rename", Section: "1", Name: "Verse"}); got != "Section 1 is already called Verse." {
		t.Errorf("rename same = %q", got)
	}
	if _, err := st.Do(context.Background(), Request{Op: "section", Action: "rename", Section: "1", Name: " "}); err == nil {
		t.Error("blank name should fail")
	}
}

func TestSectionAdd(t *testing.T) {
	st, _ := newArrangerStudio(t)
	got := do(t, st, Request{Op: "section", Action: "add", Name: "Verse, Chorus, Bridge"})
	if got != "Added sections Verse, Chorus and Bridge, 8 bars each; sections now: Verse bar 1, Chorus bar 9, Bridge bar 17." {
		t.Errorf("add = %q", got)
	}
	got = do(t, st, Request{Op: "section", Action: "add", Name: "Intro", Bars: 4})
	if got != "Added section Intro, 4 bars each; sections now: Verse bar 1, Chorus bar 9, Bridge bar 17, Intro bar 25." {
		t.Errorf("add one = %q", got)
	}
	for _, bars := range []float64{-1, 0.5, 5000, math.NaN()} {
		if _, err := st.Do(context.Background(), Request{Op: "section", Action: "add", Name: "X", Bars: bars}); err == nil {
			t.Errorf("bars %v accepted", bars)
		}
	}
}

func TestSectionUnconfirmedSaysSo(t *testing.T) {
	st, arr := newArrangerStudio(t, "Verse")
	orig := sectionWait
	sectionWait = 50 * time.Millisecond
	t.Cleanup(func() { sectionWait = orig })
	arr.ignore = true
	_, err := st.Do(context.Background(), Request{Op: "section", Action: "duplicate", Section: "Verse"})
	if err == nil || !strings.Contains(err.Error(), "arranger did not show it; sections now: Verse bar 1") {
		t.Errorf("err = %v", err)
	}
}

func shortSectionWait(t *testing.T) {
	t.Helper()
	orig := sectionWait
	sectionWait = 50 * time.Millisecond
	t.Cleanup(func() { sectionWait = orig })
}

// An edit is confirmed by the whole arranger list, names and starts, not by
// the number of sections alone.
func TestSectionDeleteChecksTheWholeList(t *testing.T) {
	st, arr := newArrangerStudio(t, "Verse", "Chorus", "Bridge")
	shortSectionWait(t)
	arr.deleteLast = true
	_, err := st.Do(context.Background(), Request{Op: "section", Action: "delete", Section: "Chorus"})
	var p Problem
	if !errors.As(err, &p) || !strings.Contains(p.Message, "sections now: Verse bar 1, Chorus bar 5") || !strings.Contains(p.Hint, "Undo") {
		t.Errorf("err = %v", err)
	}
}

func TestSectionMoveChecksTheWholeList(t *testing.T) {
	st, arr := newArrangerStudio(t, "Verse", "Chorus", "Bridge")
	shortSectionWait(t)
	arr.deleteLast = true
	_, err := st.Do(context.Background(), Request{Op: "section", Action: "move", Section: "Verse", Where: "after", Target: "Chorus"})
	var p Problem
	if !errors.As(err, &p) || !strings.Contains(p.Message, "sections now:") || !strings.Contains(p.Hint, "Undo") {
		t.Errorf("err = %v", err)
	}
}

// Move checks the copy landed, with the right name, before it deletes the
// original; otherwise the original would be lost.
func TestSectionMoveKeepsTheOriginalWhenTheCopyIsWrong(t *testing.T) {
	st, arr := newArrangerStudio(t, "Verse", "Chorus", "Bridge")
	shortSectionWait(t)
	arr.pasteAs = "Chorus"
	_, err := st.Do(context.Background(), Request{Op: "section", Action: "move", Section: "Verse", Where: "after", Target: "Chorus"})
	var p Problem
	if !errors.As(err, &p) || !strings.Contains(p.Message, "left in place") {
		t.Errorf("err = %v", err)
	}
	for _, c := range arr.log {
		if c == "Arranger>Delete Range" {
			t.Fatalf("deleted the original: %v", arr.log)
		}
	}
	if s := sectionNamesOf(st); !strings.HasPrefix(s, "Verse@1 ") {
		t.Errorf("sections = %s", s)
	}
}

func TestSectionCopyAndDuplicateCheckTheWholeList(t *testing.T) {
	for _, req := range []Request{
		{Op: "section", Action: "duplicate", Section: "Verse"},
		{Op: "section", Action: "repeat", Section: "Verse", Target: "Chorus"},
	} {
		st, arr := newArrangerStudio(t, "Verse", "Chorus", "Bridge")
		shortSectionWait(t)
		arr.noShift = true
		if _, err := st.Do(context.Background(), req); err == nil {
			t.Errorf("%s: accepted a wrong arrangement; sections %s", req.Action, sectionNamesOf(st))
		}
	}
}

func TestSectionRenameChecksTheWholeList(t *testing.T) {
	st, arr := newArrangerStudio(t, "Verse", "Chorus", "Bridge")
	shortSectionWait(t)
	arr.renameFirst = true
	_, err := st.Do(context.Background(), Request{Op: "section", Action: "rename", Section: "Bridge", Name: "Outro"})
	var p Problem
	if !errors.As(err, &p) || !strings.Contains(p.Message, "sections now: Outro bar 1") {
		t.Errorf("err = %v", err)
	}
}
