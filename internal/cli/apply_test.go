package cli

import (
	"errors"
	"testing"

	"github.com/steveclarke/ucmix/internal/boardconfig"
	"github.com/steveclarke/ucmix/internal/errs"
)

// TestApplyValueForReadScaleSeam is the correctness guard on the write path:
// a tapered float is written from its HUMAN value so Set applies the taper (and
// never the read-scale). A -6 dB fader must resolve to -6.0, which Set turns
// into wire 0.746 — NOT the read-scale WireValue 74.6 that would pin the fader.
func TestApplyValueForReadScaleSeam(t *testing.T) {
	d := boardconfig.Desired{Path: "line/ch1/volume", WireValue: 74.6, HumanValue: -6.0}
	v, err := applyValueFor(d)
	if err != nil {
		t.Fatalf("applyValueFor: %v", err)
	}
	f, ok := v.(float64)
	if !ok || f != -6.0 {
		t.Fatalf("volume apply value = %v (%T), want -6.0 (human, not the 74.6 wire value)", v, v)
	}
}

func TestApplyValueForKinds(t *testing.T) {
	cases := []struct {
		name string
		d    boardconfig.Desired
		want any
	}{
		{
			name: "bool writes the human bool",
			d:    boardconfig.Desired{Path: "line/ch1/mute", WireValue: true, HumanValue: true},
			want: true,
		},
		{
			name: "string writes the human string",
			d:    boardconfig.Desired{Path: "line/ch1/username", WireValue: "Kick", HumanValue: "Kick"},
			want: "Kick",
		},
		{
			name: "color writes the 8-digit wire form, not the human hex",
			d:    boardconfig.Desired{Path: "line/ch1/color", WireValue: "4ed2ffff", HumanValue: "4ed2ff"},
			want: "4ed2ffff",
		},
		{
			name: "nil-taper enum float writes the wire position directly",
			d:    boardconfig.Desired{Path: "aux/ch5/auxpremode", WireValue: 0.5, HumanValue: "pre2"},
			want: 0.5,
		},
		{
			name: "send with a numeric human writes the human dB",
			d:    boardconfig.Desired{Path: "line/ch1/aux5", WireValue: 0.746, HumanValue: -6.0},
			want: -6.0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := applyValueFor(tc.d)
			if err != nil {
				t.Fatalf("applyValueFor: %v", err)
			}
			if got != tc.want {
				t.Errorf("applyValueFor = %v (%T), want %v (%T)", got, got, tc.want, tc.want)
			}
		})
	}
}

// TestApplyValueForOffLevel checks the off/floor case: a send at wire 0.0 has a
// non-numeric human ("off"), so it must invert through the taper to the floor dB
// that Set re-tapers back to 0.0 — writing the raw 0.0 through the send taper
// would instead land near 0 dB.
func TestApplyValueForOffLevel(t *testing.T) {
	d := boardconfig.Desired{Path: "line/ch1/aux5", WireValue: 0.0, HumanValue: "off"}
	v, err := applyValueFor(d)
	if err != nil {
		t.Fatalf("applyValueFor: %v", err)
	}
	f, ok := v.(float64)
	if !ok || f > -80 {
		t.Fatalf("off send apply value = %v, want the taper floor (~-84 dB)", v)
	}
}

// TestExitErrorMapping locks the exit-code contract used by verify/apply.
func TestExitErrorMapping(t *testing.T) {
	// A plain CLIError (no exitError) still surfaces as one.
	var ce errs.CLIError
	if !errors.As(errs.CLIError{Message: "x"}, &ce) {
		t.Fatal("CLIError should match errors.As")
	}
	// An exitError carrying a CLIError exposes both the code and the inner error.
	ee := &exitError{code: 2, err: errs.CLIError{Message: "boom", Hint: "h"}}
	var got *exitError
	if !errors.As(error(ee), &got) || got.code != 2 {
		t.Fatalf("exitError code = %v, want 2", got)
	}
	if !errors.As(error(ee), &ce) || ce.Message != "boom" {
		t.Fatalf("inner CLIError not unwrapped: %v", ce)
	}
	// A silent drift exitError has code 1 and no inner error.
	drift := &exitError{code: 1}
	if drift.Error() != "" {
		t.Errorf("silent exitError.Error() = %q, want empty", drift.Error())
	}
}
