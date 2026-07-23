package taper

import (
	"errors"
	"math"
	"testing"
)

// calibration holds a known (position, human) point that must round-trip.
type calibration struct {
	name   string
	taper  Taper
	pos    float64
	human  float64
	posTol float64 // tolerance in position units for ToWire
	humTol float64 // tolerance in human units for FromWire
}

func TestCalibrationPoints(t *testing.T) {
	cases := []calibration{
		// Fader: measured board anchors (StudioLive 32R, fw 3.4.0).
		{"fader/bottom", Fader, 0.0, -84, 0.01, 0.3},
		{"fader/-6dB", Fader, 0.746, -6, 0.01, 0.3},
		{"fader/top", Fader, 1.0, 10, 0.01, 0.3},
		// SendLevel shares the fader curve; -6 dB default confirmed by capture.
		{"send/-6dB", SendLevel, 0.746, -6, 0.01, 0.3},
		// LimiterThresh: linear (dB+28)/28; 0.785714 = -6 dB exact in capture.
		{"limiter/-6dB", LimiterThresh, 0.7857142857142857, -6, 0.01, 0.3},
		{"limiter/0dB", LimiterThresh, 1.0, 0, 0.01, 0.3},
		{"limiter/bottom", LimiterThresh, 0.0, -28, 0.01, 0.3},
		// InputPatch: pos = input/32.
		{"patch/25", InputPatch, 0.78125, 25, 0.5 / 32, 0.5},
		{"patch/32", InputPatch, 1.0, 32, 0.5 / 32, 0.5},
		// Release stub: single known point 0.5 = 400 ms.
		{"release/400ms", Release, 0.5, 400, 0.01, 5},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.taper.FromWire(c.pos)
			if math.Abs(got-c.human) > c.humTol {
				t.Errorf("FromWire(%v) = %v %s, want %v (±%v)", c.pos, got, c.taper.Unit(), c.human, c.humTol)
			}
			pos, err := c.taper.ToWire(c.human)
			if err != nil {
				t.Fatalf("ToWire(%v) unexpected error: %v", c.human, err)
			}
			if math.Abs(pos-c.pos) > c.posTol {
				t.Errorf("ToWire(%v %s) = %v, want %v (±%v)", c.human, c.taper.Unit(), pos, c.pos, c.posTol)
			}
		})
	}
}

func TestUnits(t *testing.T) {
	cases := []struct {
		taper Taper
		unit  string
	}{
		{Fader, "dB"},
		{SendLevel, "dB"},
		{LimiterThresh, "dB"},
		{InputPatch, "input"},
		{Release, "ms"},
		{HPF, "Hz"},
	}
	for _, c := range cases {
		if got := c.taper.Unit(); got != c.unit {
			t.Errorf("Unit() = %q, want %q", got, c.unit)
		}
	}
}

// TestMonotonic sweeps [0,1] and asserts FromWire strictly increases for the
// level tapers.
func TestMonotonic(t *testing.T) {
	tapers := map[string]Taper{
		"Fader":         Fader,
		"SendLevel":     SendLevel,
		"LimiterThresh": LimiterThresh,
	}
	const steps = 1000
	for name, tp := range tapers {
		t.Run(name, func(t *testing.T) {
			prev := tp.FromWire(0)
			for i := 1; i <= steps; i++ {
				pos := float64(i) / steps
				cur := tp.FromWire(pos)
				if cur <= prev {
					t.Fatalf("not strictly increasing at pos %v: %v then %v", pos, prev, cur)
				}
				prev = cur
			}
		})
	}
}

// TestRoundTrip sweeps positions and checks ToWire(FromWire(p)) ~= p across all
// tapers, staying inside each taper's monotonic wire range.
func TestRoundTrip(t *testing.T) {
	tapers := map[string]struct {
		taper Taper
		tol   float64
	}{
		"Fader":         {Fader, 1e-9},
		"SendLevel":     {SendLevel, 1e-9},
		"LimiterThresh": {LimiterThresh, 1e-9},
		"HPF":           {HPF, 1e-9},
		// Patch quantizes to nearest 1/32, so round-trip is within half a step.
		"InputPatch": {InputPatch, 0.5/32 + 1e-9},
		"Release":    {Release, 1e-9},
	}
	const steps = 200
	for name, c := range tapers {
		t.Run(name, func(t *testing.T) {
			for i := 0; i <= steps; i++ {
				pos := float64(i) / steps
				human := c.taper.FromWire(pos)
				back, err := c.taper.ToWire(human)
				if err != nil {
					t.Fatalf("ToWire(FromWire(%v)=%v) error: %v", pos, human, err)
				}
				if math.Abs(back-pos) > c.tol {
					t.Errorf("round-trip pos %v -> %v %s -> %v (tol %v)", pos, human, c.taper.Unit(), back, c.tol)
				}
			}
		})
	}
}

// TestOverRange checks that values above a control's maximum error, while
// values below the minimum clamp to the bottom position.
func TestOverRange(t *testing.T) {
	overRange := []struct {
		name  string
		taper Taper
		human float64
	}{
		{"fader/+50dB", Fader, 50},
		{"send/+50dB", SendLevel, 50},
		{"limiter/+6dB", LimiterThresh, 6},
		{"patch/40", InputPatch, 40},
		{"patch/negative", InputPatch, -1},
	}
	for _, c := range overRange {
		t.Run(c.name, func(t *testing.T) {
			if _, err := c.taper.ToWire(c.human); !errors.Is(err, ErrOverRange) {
				t.Errorf("ToWire(%v) err = %v, want ErrOverRange", c.human, err)
			}
		})
	}

	// Below-range values clamp to the bottom position (mute / -inf), no error.
	underRange := []struct {
		name    string
		taper   Taper
		human   float64
		wantPos float64
	}{
		{"fader/-200dB", Fader, -200, 0.0},
		{"limiter/-40dB", LimiterThresh, -40, 0.0},
	}
	for _, c := range underRange {
		t.Run(c.name, func(t *testing.T) {
			pos, err := c.taper.ToWire(c.human)
			if err != nil {
				t.Fatalf("ToWire(%v) unexpected error: %v", c.human, err)
			}
			if pos != c.wantPos {
				t.Errorf("ToWire(%v) = %v, want %v (clamp to bottom)", c.human, pos, c.wantPos)
			}
		})
	}
}

// TestFromWireClamp checks positions outside [0,1] clamp rather than extrapolate.
func TestFromWireClamp(t *testing.T) {
	if got := Fader.FromWire(-0.5); got != -84 {
		t.Errorf("Fader.FromWire(-0.5) = %v, want -84", got)
	}
	if got := Fader.FromWire(1.5); got != 10 {
		t.Errorf("Fader.FromWire(1.5) = %v, want 10", got)
	}
}

// TestHPFPassThrough documents the raw pass-through until the Hz curve lands.
func TestHPFPassThrough(t *testing.T) {
	for _, v := range []float64{0, 0.0598, 0.3826, 1.0} {
		if got := HPF.FromWire(v); got != v {
			t.Errorf("HPF.FromWire(%v) = %v, want unchanged", v, got)
		}
		got, err := HPF.ToWire(v)
		if err != nil {
			t.Fatalf("HPF.ToWire(%v) error: %v", v, err)
		}
		if got != v {
			t.Errorf("HPF.ToWire(%v) = %v, want unchanged", v, got)
		}
	}
}
