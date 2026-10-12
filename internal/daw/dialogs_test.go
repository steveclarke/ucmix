package daw

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveclarke/ucmix/internal/proto"
)

func beginDialog(t *testing.T, title, first, second string) proto.Frame {
	t.Helper()
	return zm(t, fmt.Sprintf(`{"id": "BeginDialog","dialogType": 1,"dialogText": "","dialogTitle": %q,"firstButton": %q,"secondButton": %q}`, title, first, second))
}

func confirms(fake *fakeStudio) []string {
	var out []string
	for _, jm := range fake.sentJM() {
		if strings.Contains(jm, "ConfirmDialog") {
			out = append(out, jm)
		}
	}
	return out
}

// DismissDialog presses only a button that means "don't do it", or the only
// button of a one-button alert. Anything else is the musician's choice.
func TestDismissDialogPressesOnlyADecliningButton(t *testing.T) {
	tests := []struct {
		first, second string
		want          int // -1: press nothing
	}{
		{"OK", "Cancel", 1},
		{"Cancel", "OK", 0},
		{"CANCEL", "ok", 0},
		{"", "Cancel", 1},
		{"Yes", "No", 1},
		{"No", "Cancel", 1},
		{"Don't Convert", "Convert", 0},
		{"Convert", "Don’t Convert", 1},
		{"Do Not Import", "Import", 0},
		{"OK", "", 0},
		{"", "OK", 1},
		{"Yes", "OK", -1},
		{"Save", "Don't Save", -1},
		{"Replace", "Keep Both", -1},
		{"Now", "Notify", -1},
		{"", "", -1},
	}
	for _, tt := range tests {
		t.Run(tt.first+"|"+tt.second, func(t *testing.T) {
			fake := newFake(t)
			s := dialFake(t, fake)
			fake.mu.Lock()
			fake.frames <- beginDialog(t, "Question", tt.first, tt.second)
			fake.onConfirm = []proto.Frame{zm(t, `{"id": "EndDialog"}`)}
			fake.mu.Unlock()
			_ = s.WaitFor(context.Background(), func(m *Model) bool { _, open := m.Dialog(); return open })
			label, err := s.DismissDialog(context.Background())
			sent := confirms(fake)
			if tt.want < 0 {
				var p Problem
				if !errors.As(err, &p) || !strings.Contains(p.Message, `"Question"`) || p.Hint != "answer it in Studio Pro" {
					t.Errorf("err = %v, want a Problem naming the dialog", err)
				}
				for _, b := range []string{tt.first, tt.second} {
					if b != "" && !strings.Contains(p.Message, b) {
						t.Errorf("message %q does not name button %q", p.Message, b)
					}
				}
				if len(sent) != 0 {
					t.Errorf("pressed %v, want nothing", sent)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if want := fmt.Sprintf(`{"id": "ConfirmDialog","dialogResult": %d}`, tt.want); len(sent) != 1 || sent[0] != want {
				t.Errorf("pressed %v, want %s", sent, want)
			}
			if label != []string{tt.first, tt.second}[tt.want] {
				t.Errorf("label = %q", label)
			}
		})
	}
}

// Every caller that meets a dialog it did not ask for says truthfully what
// happened: closed with the button pressed, or left open for the musician.
func TestDialogCallersLeaveAChoiceOpenAndSaySo(t *testing.T) {
	type rig struct {
		st   *Studio
		fake *fakeStudio
		do   func() error
	}
	dialog := func(t *testing.T) proto.Frame { return beginDialog(t, "Save Changes", "Save", "Don't Save") }
	callers := map[string]func(t *testing.T) rig{
		"run": func(t *testing.T) rig {
			st, fake := newStudio(t)
			fake.onCommand["Track|Add Audio Track (mono)"] = []proto.Frame{dialog(t)}
			return rig{st, fake, func() error {
				_, err := st.Do(context.Background(), Request{Op: "run", Category: "Track", Name: "Add Audio Track (mono)"})
				return err
			}}
		},
		"insert": func(t *testing.T) rig {
			useFixturePlugins(t)
			st, fake := studioWithTrack(t)
			fake.onCommand["Track|Add Insert to Selected Channels"] = []proto.Frame{dialog(t)}
			return rig{st, fake, func() error {
				_, err := st.Do(context.Background(), Request{Op: "insert", Path: "Guitar", Name: "Pro EQ"})
				return err
			}}
		},
		"instrument": func(t *testing.T) rig {
			useFixturePlugins(t)
			useTempFavorites(t)
			st, fake := instrumentFake(t, "default")
			fake.onInvoke = nil
			fake.onCommand["Track|Add Instrument and Track"] = []proto.Frame{dialog(t)}
			return rig{st, fake, func() error {
				_, err := st.Do(context.Background(), Request{Op: "add_instrument", Plugin: "Mai Tai"})
				return err
			}}
		},
		"audio": func(t *testing.T) rig {
			st, _ := newTakeStudio(t)
			fake := st.session.t.(*fakeStudio)
			fake.mu.Lock()
			fake.onCommand["Edit|Split at Cursor"] = []proto.Frame{dialog(t)}
			fake.mu.Unlock()
			return rig{st, fake, func() error {
				_, err := st.Do(context.Background(), Request{Op: "audio", Action: "split", Path: "Take", Value: "3"})
				return err
			}}
		},
		"new_song": func(t *testing.T) rig {
			st, fake := newSongStudio(t, "OK", "Cancel")
			fake.mu.Lock()
			fake.onCommand["File|New Session"] = []proto.Frame{dialog(t)}
			fake.mu.Unlock()
			return rig{st, fake, func() error {
				_, err := st.Do(context.Background(), Request{Op: "new_song"})
				return err
			}}
		},
		"import": func(t *testing.T) rig {
			r := newImportRig(t)
			dir := stemFolder(t)
			r.onOpen = func(path string) {
				r.fake.frames <- dialog(t)
				_ = r.st.session.WaitFor(context.Background(), func(m *Model) bool { _, open := m.Dialog(); return open })
				a, _ := ReadAudioFile(path)
				_ = os.WriteFile(filepath.Join(r.peaks, "x1"+peakSuffix(a)), nil, 0o600)
			}
			return rig{r.st, r.fake, func() error {
				_, err := r.st.Do(context.Background(), Request{Op: "import", Files: []string{filepath.Join(dir, "03-Pad (Stereo) v2.wav")}})
				return err
			}}
		},
	}
	for name, setup := range callers {
		t.Run(name, func(t *testing.T) {
			r := setup(t)
			r.fake.mu.Lock()
			r.fake.onConfirm = []proto.Frame{zm(t, `{"id": "EndDialog"}`)}
			r.fake.mu.Unlock()
			err := r.do()
			var p Problem
			if !errors.As(err, &p) {
				t.Fatalf("err = %v, want a Problem", err)
			}
			for _, want := range []string{`"Save Changes"`, `"Save"`, `"Don't Save"`, "left it open"} {
				if !strings.Contains(p.Message, want) {
					t.Errorf("message %q lacks %s", p.Message, want)
				}
			}
			if strings.Contains(p.Message, "cancel") && !strings.Contains(p.Message, "just cancels") {
				t.Errorf("message %q claims a cancel", p.Message)
			}
			if p.Hint != "answer it in Studio Pro" {
				t.Errorf("hint = %q", p.Hint)
			}
			if sent := confirms(r.fake); len(sent) != 0 {
				t.Errorf("pressed %v", sent)
			}
			if _, open := r.st.model().Dialog(); !open {
				t.Error("dialog closed")
			}
		})
	}
}

func TestDialogCallersNameTheButtonThatClosedIt(t *testing.T) {
	st, fake := newStudio(t)
	fake.onCommand["Track|Add Audio Track (mono)"] = []proto.Frame{beginDialog(t, "Add Tracks", "OK", "Cancel")}
	fake.onConfirm = []proto.Frame{zm(t, `{"id": "EndDialog"}`)}
	_, err := st.Do(context.Background(), Request{Op: "run", Category: "Track", Name: "Add Audio Track (mono)"})
	var p Problem
	if !errors.As(err, &p) || !strings.Contains(p.Message, `closed it with "Cancel"`) {
		t.Errorf("err = %v", err)
	}
	if sent := confirms(fake); len(sent) != 1 || sent[0] != `{"id": "ConfirmDialog","dialogResult": 1}` {
		t.Errorf("pressed %v", sent)
	}
}
