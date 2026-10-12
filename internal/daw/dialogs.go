package daw

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// Dialog buttons are matched by label, never by position: Studio Pro's New
// window lists OK first and Cancel second, the Add Insert dialog the reverse.
// Labels are compared as lower-case words, so "no" never matches "Now" and
// "ok" never matches "Book".

// labelWords splits a button label into lower-case words, keeping
// apostrophes ("don't") and folding the typographic one into the plain one.
func labelWords(label string) []string {
	label = strings.ToLower(strings.ReplaceAll(label, "’", "'"))
	return strings.FieldsFunc(label, func(r rune) bool {
		return r != '\'' && !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// declineRank says how plainly a label means "don't do it": 0 for Cancel, 1
// for No, 2 for Don't…/Do Not…; -1 when it does not. "Don't Save" is not a
// decline: it closes without saving and the work is lost.
func declineRank(label string) int {
	w := labelWords(label)
	if len(w) == 0 {
		return -1
	}
	switch w[0] {
	case "cancel":
		return 0
	case "no":
		return 1
	}
	var rest []string
	switch {
	case w[0] == "don't" || w[0] == "dont":
		rest = w[1:]
	case len(w) >= 2 && w[0] == "do" && w[1] == "not":
		rest = w[2:]
	default:
		return -1
	}
	if len(rest) > 0 && (rest[0] == "save" || rest[0] == "keep") {
		return -1
	}
	return 2
}

// negativeLabel reports whether a label turns an offer down in any way:
// Cancel, No, Don't…, Do Not…, or "… without …".
func negativeLabel(label string) bool {
	w := labelWords(label)
	for i, word := range w {
		switch word {
		case "cancel", "no", "don't", "dont", "without", "never", "skip":
			return true
		case "do":
			if i+1 < len(w) && w[i+1] == "not" {
				return true
			}
		}
	}
	return false
}

// hasWord reports whether a label holds one of words as a whole word.
func hasWord(label string, words ...string) bool {
	for _, w := range labelWords(label) {
		for _, want := range words {
			if w == want {
				return true
			}
		}
	}
	return false
}

// labeled returns the slots of the buttons that carry a label.
func labeled(d Dialog) []int {
	var out []int
	for i, b := range d.Buttons {
		if strings.TrimSpace(b) != "" {
			out = append(out, i)
		}
	}
	return out
}

// declineButton is the button that dismisses a dialog without choosing for
// the musician: the plainest Cancel/No/Don't button, or the only button of a
// one-button alert. ok is false when there is none.
func declineButton(d Dialog) (int, bool) {
	if l := labeled(d); len(l) == 1 {
		return l[0], true
	}
	best, bestRank := -1, 99
	for i, b := range d.Buttons {
		if r := declineRank(b); r >= 0 && r < bestRank {
			best, bestRank = i, r
		}
	}
	return best, best >= 0
}

// buttonList names a dialog's buttons for a musician: "Save" and "Don't Save".
func buttonList(d Dialog) string {
	var names []string
	for _, i := range labeled(d) {
		names = append(names, fmt.Sprintf("%q", d.Buttons[i]))
	}
	switch len(names) {
	case 0:
		return "no labeled buttons"
	case 1:
		return "only " + names[0]
	}
	return joinAnd(names)
}

// leftOpen is the Problem for a dialog ucdaw will not answer.
func leftOpen(d Dialog) Problem {
	return Problem{
		Message: fmt.Sprintf("the %q dialog is open with %s, and none of them just cancels it, so ucdaw left it open", d.Title, buttonList(d)),
		Hint:    "answer it in Studio Pro",
	}
}

// dismiss closes a dialog ucdaw cannot answer and returns the Problem that
// reports it: what happened, then the button that closed the dialog, or that
// it is still open because no button just cancels it.
func (st *Studio) dismiss(ctx context.Context, d Dialog, what, hint string) Problem {
	label, err := st.session.DismissDialog(ctx)
	var p Problem
	switch {
	case err == nil:
		return Problem{Message: fmt.Sprintf("%s; ucdaw closed it with %q", what, label), Hint: hint}
	case errors.As(err, &p):
		return Problem{
			Message: fmt.Sprintf("%s; it has %s, none of which just cancels it, so ucdaw left it open", what, buttonList(d)),
			Hint:    p.Hint,
		}
	}
	return Problem{Message: what + "; ucdaw could not close it", Hint: "close it in Studio Pro"}
}
