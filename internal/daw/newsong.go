package daw

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// newDialogWait bounds the wait for Studio Pro's New window after File > New
// Session; newTitleWait bounds the wait for the new song to load after OK.
var (
	newDialogWait = 5 * time.Second
	newTitleWait  = 20 * time.Second
)

// newSongHint points `command run "File > New Session"` at the command that
// answers the New window.
const newSongHint = "start a new song with: ucdaw song new (MCP new_song)"

// newSong starts a new song the way File > New does on screen: Studio Pro
// opens its New window and announces it as a "New" dialog; ucdaw presses the
// button labeled OK, which creates the song with the window's current
// template and settings. The previous song stays open in its own tab. This is
// the one place ucdaw accepts a dialog: every other command cancels one.
func (st *Studio) newSong(ctx context.Context, req Request) (Result, error) {
	click := strings.ToLower(strings.TrimSpace(req.Click))
	if click != "" && click != "on" && click != "off" {
		return Result{}, Problem{Message: fmt.Sprintf("click takes on or off, not %q", req.Click)}
	}
	if req.BPM != 0 {
		p, _ := st.schema.Lookup("transport/tempo")
		if _, err := p.ToWire(req.BPM); err != nil {
			return Result{}, Problem{Message: fmt.Sprintf("tempo must be between %s and %s bpm", trim(p.Min), trim(p.Max))}
		}
	}
	cmd, err := FindCommand(st.model().Commands(), "File", "New Session")
	if err != nil {
		return Result{}, err
	}
	if d, open := st.model().Dialog(); open {
		return Result{}, dialogProblem(d)
	}
	was, _ := st.model().Str("document/title")
	// Straight to Invoke, not through run: run cancels any dialog a command
	// opens, and the New window is the dialog this command must answer.
	if err := st.session.Invoke(ctx, cmd.Category, cmd.Name, nil); err != nil {
		return Result{}, err
	}
	d, err := st.waitDialog(ctx, newDialogWait)
	if err != nil {
		return Result{}, err
	}
	ok := buttonIndex(d, "ok")
	if !strings.EqualFold(strings.TrimSpace(d.Title), "New") || ok < 0 {
		return Result{}, st.dismiss(ctx, d,
			fmt.Sprintf("Studio Pro opened the %q dialog instead of its New window, so no new song was started", strings.TrimSpace(d.Title+" "+d.Text)),
			"check Studio Pro, then try again")
	}
	seq := st.model().Seq()
	// AnswerDialog confirms the window closed within two seconds; a new song
	// can take longer to load, so an unconfirmed close is not a failure
	// here. The title below is the proof.
	if err := st.session.AnswerDialog(ctx, ok); err != nil && !errors.Is(err, ErrUnconfirmed) {
		return Result{}, err
	}
	waitCtx, cancel := context.WithTimeout(ctx, newTitleWait)
	err = st.session.WaitFor(waitCtx, func(m *Model) bool {
		title, _ := m.Str("document/title")
		return title != "" && title != was
	})
	cancel()
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, ErrConnectionClosed) {
			return Result{}, st.unconfirmed(err, "The new song")
		}
		return Result{}, Problem{
			Message: fmt.Sprintf("Pressed OK in Studio Pro's New window, but Studio Pro did not report a new song within %s; the song is still %q.", newTitleWait, was),
			Hint:    "look at Studio Pro: the New window may still be open, or the new song may have the same name",
		}
	}
	// The new song's own values arrive just after its title; let them land
	// before writing tempo, or the song's default tempo overwrites it.
	st.settle(ctx, seq)
	if req.BPM != 0 {
		if _, err := st.tempo(ctx, req.BPM); err != nil {
			return Result{}, st.partial(err)
		}
	}
	if click != "" {
		if _, err := st.click(ctx, click); err != nil {
			return Result{}, st.partial(err)
		}
	}
	song := st.Song()
	return Result{
		Message: fmt.Sprintf("Started a new song (%s) at %s bpm, click %s.", song.Document, trim(song.Tempo), onOff(song.Click)),
		Data:    song,
	}, nil
}

// partial reports a tempo or click that failed after the song was created.
func (st *Studio) partial(err error) error {
	title, _ := st.model().Str("document/title")
	return Problem{
		Message: fmt.Sprintf("Started a new song (%s), but %s", title, lowerFirst(Problem{Message: err.Error()}.Sentence())),
		Hint:    "set it with ucdaw tempo or ucdaw click",
	}
}

// waitDialog waits for Studio Pro to announce a dialog.
func (st *Studio) waitDialog(ctx context.Context, wait time.Duration) (Dialog, error) {
	waitCtx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	err := st.session.WaitFor(waitCtx, func(m *Model) bool { _, open := m.Dialog(); return open })
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, ErrConnectionClosed) {
			return Dialog{}, st.unconfirmed(err, "File > New Session")
		}
		return Dialog{}, Problem{
			Message: fmt.Sprintf("Studio Pro did not open its New window within %s, so no new song was started.", wait),
			Hint:    "check Studio Pro: another window may be in front",
		}
	}
	d, _ := st.model().Dialog()
	return d, nil
}

// buttonIndex finds a dialog button by its label, ignoring case; -1 when no
// button has that label.
func buttonIndex(d Dialog, label string) int {
	for i, b := range d.Buttons {
		if strings.EqualFold(strings.TrimSpace(b), label) {
			return i
		}
	}
	return -1
}
