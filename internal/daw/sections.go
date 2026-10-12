package daw

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Arrangement edits work through Studio Pro's own commands, proven live:
//
//   - Arranger / Goto Section {Number: n} moves the cursor to section n
//     (1-based) and selects it on the arranger track.
//   - With a section selected, Edit / Duplicate inserts a copy right after
//     it, Edit / Copy and Edit / Paste paste it at the cursor (inserting time),
//     Arranger / Delete Range removes it and closes the gap, and
//     Event / Rename Events {"New Name": n} renames it.
//   - Edit / Create Range from Cursor, Transport / Forward Bar {Bars: n} and
//     Edit / Range End to Cursor make a range; Arranger / Add Arranger Section
//     from Selection turns it into a section.
//
// Every edit is confirmed from the arranger list Studio Pro sends back.

// Section is one arranger section, numbered from 1 in song order.
type Section struct {
	Number   int     `json:"number"`
	Name     string  `json:"name"`
	Start    float64 `json:"start"`
	End      float64 `json:"end"`
	StartBar float64 `json:"startBar,omitempty"`
	Bars     float64 `json:"bars,omitempty"`
}

// DefaultSectionBars is the length of a new section when none is given.
const DefaultSectionBars = 8

// MaxSectionBars bounds a new section.
const MaxSectionBars = 999

// sectionWait bounds how long an arrangement edit waits for Studio Pro's
// new arranger list.
var sectionWait = 3 * time.Second

// secondsPerBar is one bar at the song's tempo and meter, 0 when unknown.
func (st *Studio) secondsPerBar() float64 {
	bpm := st.Tempo()
	if bpm <= 0 {
		return 0
	}
	num, denom := st.timeSignature()
	return float64(num) * 4 / float64(denom) * 60 / bpm
}

// barAt is the bar number at a time in seconds (bar 1 is the start).
func barAt(seconds, spb float64) float64 {
	if spb <= 0 {
		return 0
	}
	return math.Round((seconds/spb+1)*100) / 100
}

func (st *Studio) sectionList() []Section {
	spb := st.secondsPerBar()
	events := st.model().Events("Arng")
	out := make([]Section, len(events))
	for i, e := range events {
		out[i] = Section{Number: i + 1, Name: e.Name, Start: e.Start, End: e.End}
		if spb > 0 {
			out[i].StartBar = barAt(e.Start, spb)
			out[i].Bars = math.Round((e.End-e.Start)/spb*100) / 100
		}
	}
	return out
}

// label names a section for a musician: its name, or its number when it has
// none.
func (s Section) label() string {
	if strings.TrimSpace(s.Name) == "" {
		return fmt.Sprintf("section %d", s.Number)
	}
	return s.Name
}

func (s Section) where() string {
	if s.StartBar > 0 {
		return "bar " + trim(s.StartBar)
	}
	return clock(s.Start)
}

var ordinalWords = map[string]int{
	"one": 1, "two": 2, "three": 3, "four": 4, "five": 5, "six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10,
	"first": 1, "second": 2, "third": 3, "fourth": 4, "fifth": 5, "sixth": 6, "seventh": 7, "eighth": 8, "ninth": 9, "tenth": 10,
	"1st": 1, "2nd": 2, "3rd": 3, "4th": 4, "5th": 5,
}

func ordinal(word string) (int, bool) {
	word = strings.ToLower(strings.TrimSpace(word))
	if n, err := strconv.Atoi(word); err == nil && n > 0 {
		return n, true
	}
	n, ok := ordinalWords[word]
	return n, ok
}

// resolveSection finds a section by number ("3", "#3"), by name ("Chorus"),
// or by name and occurrence ("Chorus 2", "chorus two", "second chorus",
// "last chorus").
func resolveSection(all []Section, ref string) (Section, error) {
	ref = strings.Join(strings.Fields(ref), " ")
	if len(all) == 0 {
		return Section{}, Problem{Message: "the song has no arranger sections yet", Hint: "add one with: ucdaw section add Verse"}
	}
	if ref == "" {
		return Section{}, Problem{Message: "name a section"}
	}
	if n, err := strconv.Atoi(strings.TrimPrefix(ref, "#")); err == nil {
		if n < 1 || n > len(all) {
			return Section{}, Problem{Message: fmt.Sprintf("there is no section %d; the song has %d", n, len(all)), Hint: "sections: " + sectionNames(all)}
		}
		return all[n-1], nil
	}
	named := func(name string) []Section {
		var out []Section
		for _, s := range all {
			if strings.EqualFold(strings.TrimSpace(s.Name), name) {
				out = append(out, s)
			}
		}
		return out
	}
	if exact := named(ref); len(exact) == 1 {
		return exact[0], nil
	} else if len(exact) > 1 {
		return Section{}, ambiguous(ref, exact)
	}
	words := strings.Fields(ref)
	if len(words) >= 2 {
		// "Chorus 2", "chorus two"
		if n, ok := ordinal(words[len(words)-1]); ok {
			if matches := named(strings.Join(words[:len(words)-1], " ")); len(matches) > 0 {
				if n > len(matches) {
					return Section{}, Problem{Message: fmt.Sprintf("there are only %d %q sections", len(matches), matches[0].Name)}
				}
				return matches[n-1], nil
			}
		}
		// "second chorus", "last chorus", "the bridge"
		first := strings.ToLower(words[0])
		rest := strings.Join(words[1:], " ")
		if matches := named(rest); len(matches) > 0 {
			switch {
			case first == "the" && len(matches) == 1:
				return matches[0], nil
			case first == "the":
				return Section{}, ambiguous(rest, matches)
			case first == "last" || first == "final":
				return matches[len(matches)-1], nil
			}
			if n, ok := ordinal(first); ok {
				if n > len(matches) {
					return Section{}, Problem{Message: fmt.Sprintf("there are only %d %q sections", len(matches), matches[0].Name)}
				}
				return matches[n-1], nil
			}
		}
	}
	return Section{}, Problem{Message: fmt.Sprintf("no section called %q", ref), Hint: "sections: " + sectionNames(all)}
}

func ambiguous(name string, matches []Section) Problem {
	var opts []string
	for i, s := range matches {
		opts = append(opts, fmt.Sprintf("%s %d (%s)", s.Name, i+1, s.where()))
	}
	return Problem{
		Message: fmt.Sprintf("the song has %d %q sections", len(matches), matches[0].Name),
		Hint:    "say which: " + strings.Join(opts, " or "),
	}
}

func sectionNames(all []Section) string {
	if len(all) == 0 {
		return "none"
	}
	parts := make([]string, len(all))
	for i, s := range all {
		parts[i] = fmt.Sprintf("%d %s (%s)", s.Number, s.label(), s.where())
	}
	return strings.Join(parts, ", ")
}

// sectionsSummary lists sections as "Intro 1, Verse 9, Chorus 17" (start
// bars), the shape a musician scans.
func sectionsSummary(all []Section) string {
	parts := make([]string, len(all))
	for i, s := range all {
		parts[i] = fmt.Sprintf("%s %s", s.label(), s.where())
	}
	return strings.Join(parts, ", ")
}

func (st *Studio) section(ctx context.Context, req Request) (Result, error) {
	if req.Action == "list" || req.Action == "" {
		return st.listSections(), nil
	}
	if d, open := st.model().Dialog(); open {
		return Result{}, dialogProblem(d)
	}
	switch req.Action {
	case "add":
		return st.addSections(ctx, req.Name, req.Bars)
	case "duplicate":
		return st.duplicateSection(ctx, req.Section)
	case "repeat":
		return st.copySection(ctx, req.Section, "after", req.Target)
	case "copy":
		return st.copySection(ctx, req.Section, req.Where, req.Target)
	case "move":
		return st.moveSection(ctx, req.Section, req.Where, req.Target)
	case "delete":
		return st.deleteSection(ctx, req.Section)
	case "rename":
		return st.renameSection(ctx, req.Section, req.Name)
	case "goto", "play":
		return st.gotoSection(ctx, req)
	}
	return Result{}, Problem{
		Message: fmt.Sprintf("unknown section action %q", req.Action),
		Hint:    "use list, add, duplicate, repeat, move, delete, rename, goto or play",
	}
}

func (st *Studio) listSections() Result {
	all := st.sectionList()
	if len(all) == 0 {
		return Result{Message: "The song has no arranger sections.", Data: all}
	}
	parts := make([]string, len(all))
	for i, s := range all {
		part := fmt.Sprintf("%s %s", s.label(), s.where())
		if s.Bars > 0 {
			part += fmt.Sprintf(" (%s bar%s)", trim(s.Bars), plural(s.Bars))
		}
		parts[i] = part
	}
	return Result{
		Message: fmt.Sprintf("%d section%s: %s.", len(all), plural(float64(len(all))), strings.Join(parts, ", ")),
		Data:    all,
	}
}

// invokeAll runs commands in order.
func (st *Studio) invokeAll(ctx context.Context, steps ...step) error {
	for _, s := range steps {
		if err := st.session.Invoke(ctx, s.category, s.name, s.args); err != nil {
			return err
		}
	}
	return nil
}

type step struct {
	category, name string
	args           map[string]string
}

func gotoStep(n int) step {
	return step{"Arranger", "Goto Section", map[string]string{"Number": strconv.Itoa(n)}}
}

// waitSections waits until Studio Pro's arranger list satisfies cond.
func (st *Studio) waitSections(ctx context.Context, cond func([]Section) bool) ([]Section, error) {
	waitCtx, cancel := context.WithTimeout(ctx, sectionWait)
	defer cancel()
	err := st.session.WaitFor(waitCtx, func(*Model) bool { return cond(st.sectionList()) })
	if err != nil && ctx.Err() == nil && waitCtx.Err() != nil {
		err = ErrUnconfirmed
	}
	return st.sectionList(), err
}

func (st *Studio) notConfirmed(err error, what string, after []Section) error {
	if err == ErrUnconfirmed {
		return Problem{
			Message: fmt.Sprintf("%s was sent, but Studio Pro's arranger did not show it; sections now: %s", what, orNone(sectionsSummary(after))),
			Hint:    "check Studio Pro; undo with: ucdaw command run \"Edit > Undo\"",
		}
	}
	return st.unconfirmed(err, what)
}

// pasted is the arranger list expected after a section named name, length
// seconds long, goes in at a section boundary: sections from there on move
// later by its length.
func pasted(before []Section, name string, at, length float64) []Section {
	out := make([]Section, 0, len(before)+1)
	placed := false
	for _, s := range before {
		if !placed && s.Start >= at-0.01 {
			out = append(out, Section{Name: name, Start: at, End: at + length})
			placed = true
		}
		if s.Start >= at-0.01 {
			s.Start += length
			s.End += length
		}
		out = append(out, s)
	}
	if !placed {
		out = append(out, Section{Name: name, Start: at, End: at + length})
	}
	return renumber(out)
}

// removed is the arranger list expected after section n (1-based) is
// deleted with Delete Range: later sections move up to close the gap.
func removed(before []Section, n int) []Section {
	gone := before[n-1]
	length := gone.End - gone.Start
	out := make([]Section, 0, len(before)-1)
	for i, s := range before {
		if i == n-1 {
			continue
		}
		if s.Start >= gone.End-0.01 {
			s.Start -= length
			s.End -= length
		}
		out = append(out, s)
	}
	return renumber(out)
}

func renumber(all []Section) []Section {
	for i := range all {
		all[i].Number = i + 1
	}
	return all
}

// sameSections compares two arranger lists by name and start, in order.
func sameSections(want, got []Section) bool {
	if len(want) != len(got) {
		return false
	}
	for i := range want {
		if want[i].Name != got[i].Name || !near(want[i].Start, got[i].Start) {
			return false
		}
	}
	return true
}

// expectSections waits until Studio Pro's arranger shows exactly want. If it
// never does, the Problem says what Studio Pro shows instead: unchanged
// (the edit did not happen) or something else (it went wrong).
func (st *Studio) expectSections(ctx context.Context, what string, before, want []Section, hint string) ([]Section, error) {
	after, err := st.waitSections(ctx, func(s []Section) bool { return sameSections(want, s) })
	if err == nil {
		return after, nil
	}
	if err == ErrUnconfirmed && !sameSections(before, after) {
		return after, Problem{
			Message: fmt.Sprintf("%s did not come out as expected; sections now: %s (expected %s)", what, orNone(sectionsSummary(after)), orNone(st.summaryOf(want))),
			Hint:    hint,
		}
	}
	return after, st.notConfirmed(err, what, after)
}

// summaryOf lists expected sections the way sectionsSummary lists real ones.
func (st *Studio) summaryOf(all []Section) string {
	spb := st.secondsPerBar()
	out := make([]Section, len(all))
	for i, s := range all {
		out[i] = s
		out[i].StartBar = barAt(s.Start, spb)
	}
	return sectionsSummary(out)
}

const undoOnce = "check Studio Pro; undo with: ucdaw command run \"Edit > Undo\""

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func near(a, b float64) bool { return math.Abs(a-b) < 0.01 }

func (st *Studio) shift(seconds float64) string {
	spb := st.secondsPerBar()
	if spb <= 0 {
		return fmt.Sprintf("%s s", trim(round2(seconds)))
	}
	bars := math.Round(seconds/spb*100) / 100
	return fmt.Sprintf("%s bar%s", trim(bars), plural(bars))
}

func (st *Studio) duplicateSection(ctx context.Context, ref string) (Result, error) {
	before := st.sectionList()
	src, err := resolveSection(before, ref)
	if err != nil {
		return Result{}, err
	}
	if err := st.invokeAll(ctx, gotoStep(src.Number), step{"Edit", "Duplicate", nil}); err != nil {
		return Result{}, err
	}
	after, err := st.expectSections(ctx, "Duplicating "+src.label(), before, pasted(before, src.Name, src.End, src.End-src.Start), undoOnce)
	if err != nil {
		return Result{}, err
	}
	copyAt := after[src.Number]
	msg := fmt.Sprintf("Duplicated %s; the copy starts at %s", src.label(), copyAt.where())
	if src.Number < len(before) {
		msg += fmt.Sprintf(" and the rest moved %s later", st.shift(src.End-src.Start))
	}
	return Result{Message: msg + ".", Data: after}, nil
}

// cursorTo puts the cursor at a section boundary: the start of the section
// that begins there, or a whole number of bars from the song start.
func (st *Studio) cursorTo(ctx context.Context, all []Section, seconds float64) error {
	for _, s := range all {
		if near(s.Start, seconds) {
			return st.invokeAll(ctx, gotoStep(s.Number))
		}
	}
	return st.cursorToBar(ctx, seconds)
}

func (st *Studio) cursorToBar(ctx context.Context, seconds float64) error {
	spb := st.secondsPerBar()
	if spb <= 0 {
		return Problem{Message: "Studio Pro has not reported the tempo, so ucdaw cannot find the bar"}
	}
	bars := seconds / spb
	whole := math.Round(bars)
	if math.Abs(bars-whole) > 0.001 {
		return Problem{Message: fmt.Sprintf("%s is not on a bar line, so ucdaw cannot place the cursor there", clock(seconds))}
	}
	steps := []step{{"Transport", "Return to Zero", nil}}
	if whole > 0 {
		steps = append(steps, step{"Transport", "Forward Bar", map[string]string{"Bars": strconv.Itoa(int(whole))}})
	}
	return st.invokeAll(ctx, steps...)
}

// copySection pastes a copy of a section before or after another one.
func (st *Studio) copySection(ctx context.Context, ref, where, targetRef string) (Result, error) {
	before := st.sectionList()
	src, err := resolveSection(before, ref)
	if err != nil {
		return Result{}, err
	}
	target, at, err := st.target(before, where, targetRef)
	if err != nil {
		return Result{}, err
	}
	if err := st.invokeAll(ctx, gotoStep(src.Number), step{"Edit", "Copy", nil}); err != nil {
		return Result{}, err
	}
	if err := st.cursorTo(ctx, before, at); err != nil {
		return Result{}, err
	}
	if err := st.invokeAll(ctx, step{"Edit", "Paste", nil}); err != nil {
		return Result{}, err
	}
	what := fmt.Sprintf("Repeating %s %s %s", src.label(), where, target.label())
	after, err := st.expectSections(ctx, what, before, pasted(before, src.Name, at, src.End-src.Start), undoOnce)
	if err != nil {
		return Result{}, err
	}
	var placed Section
	for _, x := range after {
		if x.Name == src.Name && near(x.Start, at) {
			placed = x
		}
	}
	verb := "Repeated"
	msg := fmt.Sprintf("%s %s %s %s, at %s", verb, src.label(), where, target.label(), placed.where())
	if at < before[len(before)-1].End-0.01 {
		msg += fmt.Sprintf("; the rest moved %s later", st.shift(src.End-src.Start))
	}
	return Result{Message: msg + ".", Data: after}, nil
}

// target resolves where a section goes: before or after another section.
func (st *Studio) target(all []Section, where, ref string) (Section, float64, error) {
	where = strings.ToLower(strings.TrimSpace(where))
	if where == "" {
		where = "after"
	}
	if where != "before" && where != "after" {
		return Section{}, 0, Problem{Message: fmt.Sprintf("a section goes before or after another, not %q", where)}
	}
	t, err := resolveSection(all, ref)
	if err != nil {
		return Section{}, 0, err
	}
	if where == "before" {
		return t, t.Start, nil
	}
	return t, t.End, nil
}

func (st *Studio) moveSection(ctx context.Context, ref, where, targetRef string) (Result, error) {
	before := st.sectionList()
	src, err := resolveSection(before, ref)
	if err != nil {
		return Result{}, err
	}
	target, at, err := st.target(before, where, targetRef)
	if err != nil {
		return Result{}, err
	}
	if target.Number == src.Number {
		return Result{}, Problem{Message: fmt.Sprintf("%s cannot go %s itself", src.label(), where)}
	}
	if near(at, src.Start) || near(at, src.End) {
		return Result{Message: fmt.Sprintf("%s is already %s %s.", src.label(), where, target.label()), Data: before}, nil
	}
	if err := st.invokeAll(ctx, gotoStep(src.Number), step{"Edit", "Copy", nil}); err != nil {
		return Result{}, err
	}
	if err := st.cursorTo(ctx, before, at); err != nil {
		return Result{}, err
	}
	if err := st.invokeAll(ctx, step{"Edit", "Paste", nil}); err != nil {
		return Result{}, err
	}
	length := src.End - src.Start
	// The copy must be in, with the source's name, where it was pasted, and
	// the rest moved along, before the original goes: otherwise deleting it
	// would lose the section.
	wantPasted := pasted(before, src.Name, at, length)
	after, err := st.waitSections(ctx, func(s []Section) bool { return sameSections(wantPasted, s) })
	if err != nil {
		if err == ErrUnconfirmed && !sameSections(before, after) {
			return Result{}, Problem{
				Message: fmt.Sprintf("the copy of %s did not land as expected, so the original was left in place; sections now: %s", src.label(), sectionsSummary(after)),
				Hint:    undoOnce,
			}
		}
		return Result{}, st.notConfirmed(err, "Moving "+src.label(), after)
	}
	// The original moved along if the copy went in before it.
	original := src.Number
	if at <= src.Start {
		original++
	}
	if err := st.invokeAll(ctx, gotoStep(original), step{"Arranger", "Delete Range", nil}); err != nil {
		return Result{}, err
	}
	want := removed(wantPasted, original)
	after, err = st.expectSections(ctx, "Removing the original "+src.label(), wantPasted, want, "check Studio Pro; undo twice with: ucdaw command run \"Edit > Undo\"")
	if err != nil {
		return Result{}, err
	}
	newStart := at
	if at > src.Start {
		newStart = at - length
	}
	var moved Section
	for _, x := range after {
		if x.Name == src.Name && near(x.Start, newStart) {
			moved = x
		}
	}
	return Result{
		Message: fmt.Sprintf("Moved %s %s %s; it now starts at %s (undo takes two steps).", src.label(), where, target.label(), moved.where()),
		Data:    after,
	}, nil
}

func (st *Studio) deleteSection(ctx context.Context, ref string) (Result, error) {
	before := st.sectionList()
	src, err := resolveSection(before, ref)
	if err != nil {
		return Result{}, err
	}
	if err := st.invokeAll(ctx, gotoStep(src.Number), step{"Arranger", "Delete Range", nil}); err != nil {
		return Result{}, err
	}
	after, err := st.expectSections(ctx, "Deleting "+src.label(), before, removed(before, src.Number), undoOnce)
	if err != nil {
		return Result{}, err
	}
	msg := fmt.Sprintf("Deleted %s (%s, %s)", src.label(), src.where(), st.shift(src.End-src.Start))
	if src.Number < len(before) {
		msg += "; the rest moved up to close the gap"
	}
	return Result{Message: msg + ".", Data: after}, nil
}

func (st *Studio) renameSection(ctx context.Context, ref, name string) (Result, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Result{}, Problem{Message: "give the section a new name"}
	}
	before := st.sectionList()
	src, err := resolveSection(before, ref)
	if err != nil {
		return Result{}, err
	}
	if src.Name == name {
		return Result{Message: fmt.Sprintf("Section %d is already called %s.", src.Number, name), Data: before}, nil
	}
	if err := st.invokeAll(ctx, gotoStep(src.Number), step{"Event", "Rename Events", map[string]string{"New Name": name}}); err != nil {
		return Result{}, err
	}
	want := append([]Section(nil), before...)
	want[src.Number-1].Name = name
	after, err := st.expectSections(ctx, "Renaming "+src.label(), before, want, undoOnce)
	if err != nil {
		return Result{}, err
	}
	return Result{Message: fmt.Sprintf("Renamed %s to %s.", src.label(), name), Data: after}, nil
}

// addSections appends one section per name, each bars long, after the last
// section (or at the song start).
func (st *Studio) addSections(ctx context.Context, names string, bars float64) (Result, error) {
	var list []string
	for _, n := range strings.Split(names, ",") {
		if n = strings.TrimSpace(n); n != "" {
			list = append(list, n)
		}
	}
	if len(list) == 0 {
		return Result{}, Problem{Message: "name the section to add, e.g. Verse"}
	}
	if len(list) > 32 {
		return Result{}, Problem{Message: "add at most 32 sections at once"}
	}
	if bars == 0 {
		bars = DefaultSectionBars
	}
	if bars < 1 || bars > MaxSectionBars || bars != math.Trunc(bars) || math.IsNaN(bars) {
		return Result{}, Problem{Message: fmt.Sprintf("a section is a whole number of bars from 1 to %d", MaxSectionBars)}
	}
	// Studio Pro makes no section from a range in a song with no tracks
	// (seen live on a new song); it answers nothing at all.
	if len(st.channels()) == 0 {
		return Result{}, Problem{
			Message: "Studio Pro adds arranger sections only in a song with a track, and this song has no tracks",
			Hint:    "add one first, e.g. ucdaw track add --name Guide",
		}
	}
	var added []string
	for _, name := range list {
		before := st.sectionList()
		at := 0.0
		if len(before) > 0 {
			at = before[len(before)-1].End
		}
		if err := st.cursorToBar(ctx, at); err != nil {
			return Result{}, err
		}
		if err := st.invokeAll(ctx,
			step{"Edit", "Create Range from Cursor", nil},
			step{"Transport", "Forward Bar", map[string]string{"Bars": strconv.Itoa(int(bars))}},
			step{"Edit", "Range End to Cursor", nil},
			step{"Arranger", "Add Arranger Section from Selection", nil},
		); err != nil {
			return Result{}, err
		}
		// Studio Pro names a new section itself, so the check is the old
		// sections untouched and the new one, any name, at the end.
		length := bars * st.secondsPerBar()
		after, err := st.waitSections(ctx, func(s []Section) bool {
			return len(s) == len(before)+1 && sameSections(before, s[:len(before)]) &&
				near(s[len(before)].Start, at) && near(s[len(before)].End, at+length)
		})
		if err != nil {
			if err == ErrUnconfirmed && len(after) != len(before) {
				return Result{}, Problem{
					Message: fmt.Sprintf("adding section %s did not come out as expected; sections now: %s", name, orNone(sectionsSummary(after))),
					Hint:    undoOnce,
				}
			}
			return Result{}, st.notConfirmed(err, "Adding section "+name, after)
		}
		n := len(after)
		if after[n-1].Name != name {
			if _, err := st.renameSection(ctx, strconv.Itoa(n), name); err != nil {
				return Result{}, err
			}
		}
		added = append(added, name)
	}
	all := st.sectionList()
	what := "section " + added[0]
	if len(added) > 1 {
		what = "sections " + joinAnd(added)
	}
	return Result{
		Message: fmt.Sprintf("Added %s, %s bars each; sections now: %s.", what, trim(bars), sectionsSummary(all)),
		Data:    all,
	}, nil
}

func joinAnd(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return items[0] + " and " + items[1]
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// gotoSection puts the cursor at a section's start, and for "play" plays
// from there.
func (st *Studio) gotoSection(ctx context.Context, req Request) (Result, error) {
	all := st.sectionList()
	s, err := resolveSection(all, req.Section)
	if err != nil {
		return Result{}, err
	}
	if err := st.invokeAll(ctx, gotoStep(s.Number)); err != nil {
		return Result{}, err
	}
	if req.Action == "goto" {
		return Result{Message: fmt.Sprintf("Cursor at %s (%s).", s.label(), s.where()), Data: s}, nil
	}
	res, err := st.play(ctx, Request{Seconds: req.Seconds, Bars: req.Bars})
	if err != nil {
		return Result{}, err
	}
	res.Message = fmt.Sprintf("From %s: %s", s.label(), res.Message)
	return res, nil
}
