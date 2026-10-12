package daw

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/steveclarke/ucmix/internal/proto"
)

// newSongStudio is a fake Studio Pro with File > New Session in its catalog
// and a song titled "Old Song".
func newSongStudio(t *testing.T, first, second string) (*Studio, *fakeStudio) {
	t.Helper()
	st, fake := newStudio(t)
	m := st.session.Model()
	m.mutate(func(rec func(string)) {
		m.commands = append(m.commands, Command{Category: "File", Name: "New Session"})
		m.setStr("document/title", "Old Song", rec)
	})
	fake.mu.Lock()
	fake.onCommand["File|New Session"] = []proto.Frame{
		zm(t, fmt.Sprintf(`{"id": "BeginDialog","dialogType": 1,"dialogText": "","dialogTitle": "New","firstButton": %q,"secondButton": %q}`, first, second)),
	}
	fake.onConfirm = []proto.Frame{
		zm(t, `{"id": "EndDialog"}`),
		ps("document/title", "2026-10-12 Stephen Clarke"),
		zm(t, `{"id": "Synchronize","children": {"transport": {"values": {"tempo": 0.28205128205128205}},"metronome": {"values": {"clickOn": 0.0}},"mixer": {"children": {"channel": {"values": {"count": 0.0}}}}}}`),
	}
	fake.mu.Unlock()
	origDialog, origTitle := newDialogWait, newTitleWait
	t.Cleanup(func() { newDialogWait, newTitleWait = origDialog, origTitle })
	newDialogWait, newTitleWait = 500*time.Millisecond, 500*time.Millisecond
	return st, fake
}

func lastConfirm(fake *fakeStudio) string {
	jm := fake.sentJM()
	for i := len(jm) - 1; i >= 0; i-- {
		if strings.Contains(jm[i], "ConfirmDialog") {
			return jm[i]
		}
	}
	return ""
}

func TestNewSongPressesOKAndSetsTempoAndClick(t *testing.T) {
	st, fake := newSongStudio(t, "OK", "Cancel")
	res, err := st.Do(context.Background(), Request{Op: "new_song", BPM: 72, Click: "on"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "Started a new song (2026-10-12 Stephen Clarke) at 72 bpm, click on."; res.Message != want {
		t.Errorf("message = %q, want %q", res.Message, want)
	}
	if got := lastConfirm(fake); got != `{"id": "ConfirmDialog","dialogResult": 0}` {
		t.Errorf("pressed %s", got)
	}
	if bpm := round2(st.Tempo()); bpm != 72 {
		t.Errorf("tempo = %v", bpm)
	}
	if v, _ := st.model().Num("metronome/clickOn"); v != 1 {
		t.Errorf("clickOn = %v", v)
	}
	song, ok := res.Data.(Song)
	if !ok || song.Document != "2026-10-12 Stephen Clarke" || song.Tempo != 72 || !song.Click {
		t.Errorf("data = %+v", res.Data)
	}
}

func TestNewSongMatchesTheOKLabelNotItsPlace(t *testing.T) {
	st, fake := newSongStudio(t, "Cancel", "OK")
	if _, err := st.Do(context.Background(), Request{Op: "new_song"}); err != nil {
		t.Fatal(err)
	}
	if got := lastConfirm(fake); got != `{"id": "ConfirmDialog","dialogResult": 1}` {
		t.Errorf("pressed %s", got)
	}
}

func TestNewSongKeepsTheNewSongsOwnSettings(t *testing.T) {
	st, _ := newSongStudio(t, "OK", "Cancel")
	res, err := st.Do(context.Background(), Request{Op: "new_song"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "Started a new song (2026-10-12 Stephen Clarke) at 120 bpm, click off."; res.Message != want {
		t.Errorf("message = %q, want %q", res.Message, want)
	}
}

func TestNewSongCancelsAnotherDialog(t *testing.T) {
	st, fake := newSongStudio(t, "OK", "Cancel")
	fake.mu.Lock()
	fake.onCommand["File|New Session"] = []proto.Frame{
		zm(t, `{"id": "BeginDialog","dialogType": 1,"dialogText": "","dialogTitle": "Save Changes","firstButton": "OK","secondButton": "Cancel"}`),
	}
	fake.onConfirm = []proto.Frame{zm(t, `{"id": "EndDialog"}`)}
	fake.mu.Unlock()
	_, err := st.Do(context.Background(), Request{Op: "new_song"})
	var p Problem
	if !errors.As(err, &p) || !strings.Contains(p.Message, `"Save Changes"`) || !strings.Contains(p.Message, `closed it with "Cancel"`) {
		t.Errorf("err = %v", err)
	}
	if got := lastConfirm(fake); got != `{"id": "ConfirmDialog","dialogResult": 1}` {
		t.Errorf("pressed %s, want Cancel (1)", got)
	}
	if title, _ := st.model().Str("document/title"); title != "Old Song" {
		t.Errorf("title = %q", title)
	}
}

func TestNewSongRefusesWhileADialogIsOpen(t *testing.T) {
	st, fake := newSongStudio(t, "OK", "Cancel")
	fake.mu.Lock()
	fake.frames <- zm(t, `{"id": "BeginDialog","dialogType": 1,"dialogTitle": "Add Tracks","firstButton": "Cancel","secondButton": "OK"}`)
	fake.mu.Unlock()
	_ = st.session.WaitFor(context.Background(), func(m *Model) bool { _, open := m.Dialog(); return open })
	_, err := st.Do(context.Background(), Request{Op: "new_song"})
	var p Problem
	if !errors.As(err, &p) || !strings.Contains(p.Message, `"Add Tracks"`) {
		t.Errorf("err = %v", err)
	}
	for _, jm := range fake.sentJM() {
		if strings.Contains(jm, "New Session") {
			t.Error("sent New Session with a dialog open")
		}
	}
}

func TestNewSongSaysWhenNoWindowOpens(t *testing.T) {
	st, fake := newSongStudio(t, "OK", "Cancel")
	fake.mu.Lock()
	delete(fake.onCommand, "File|New Session")
	fake.mu.Unlock()
	_, err := st.Do(context.Background(), Request{Op: "new_song"})
	var p Problem
	if !errors.As(err, &p) || !strings.Contains(p.Message, "did not open its New window") {
		t.Errorf("err = %v", err)
	}
}

func TestNewSongSaysWhenTheTitleDoesNotChange(t *testing.T) {
	st, fake := newSongStudio(t, "OK", "Cancel")
	fake.mu.Lock()
	fake.onConfirm = []proto.Frame{zm(t, `{"id": "EndDialog"}`)}
	fake.mu.Unlock()
	_, err := st.Do(context.Background(), Request{Op: "new_song", BPM: 72})
	var p Problem
	if !errors.As(err, &p) || !strings.Contains(p.Message, "did not report a new song") {
		t.Errorf("err = %v", err)
	}
	if bpm := round2(st.Tempo()); bpm != 120 {
		t.Errorf("tempo changed on the old song: %v", bpm)
	}
}

func TestNewSongRejectsABadClick(t *testing.T) {
	st, fake := newSongStudio(t, "OK", "Cancel")
	_, err := st.Do(context.Background(), Request{Op: "new_song", Click: "loud"})
	if err == nil {
		t.Fatal("want an error")
	}
	for _, jm := range fake.sentJM() {
		if strings.Contains(jm, "New Session") {
			t.Error("started a song before checking the arguments")
		}
	}
}

func TestDismissDialogPressesCancelByLabel(t *testing.T) {
	fake := newFake(t)
	s := dialFake(t, fake)
	fake.mu.Lock()
	fake.frames <- zm(t, `{"id": "BeginDialog","dialogType": 1,"dialogTitle": "New","firstButton": "OK","secondButton": "Cancel"}`)
	fake.onConfirm = []proto.Frame{zm(t, `{"id": "EndDialog"}`)}
	fake.mu.Unlock()
	_ = s.WaitFor(context.Background(), func(m *Model) bool { _, open := m.Dialog(); return open })
	if _, err := s.DismissDialog(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := lastConfirm(fake); got != `{"id": "ConfirmDialog","dialogResult": 1}` {
		t.Errorf("pressed %s, want Cancel (1)", got)
	}
}

func TestRunCancelsTheNewWindow(t *testing.T) {
	st, fake := newSongStudio(t, "OK", "Cancel")
	fake.mu.Lock()
	fake.onConfirm = []proto.Frame{zm(t, `{"id": "EndDialog"}`)}
	fake.mu.Unlock()
	_, err := st.Do(context.Background(), Request{Op: "run", Category: "File", Name: "New Session"})
	var p Problem
	if !errors.As(err, &p) || !strings.Contains(p.Hint, "ucdaw song new") {
		t.Errorf("err = %v", err)
	}
	if got := lastConfirm(fake); got != `{"id": "ConfirmDialog","dialogResult": 1}` {
		t.Errorf("pressed %s, want Cancel (1)", got)
	}
}
