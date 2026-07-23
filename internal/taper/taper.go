// Package taper holds pure unit-conversion math for StudioLive Series III
// controls: converting human units (dB, Hz, ms, physical input numbers) to and
// from the normalized 0..1 wire positions the board uses.
//
// Everything here is pure math — no I/O, no dependencies outside the standard
// library. Each control exposes a [Taper]; the schema layer attaches the right
// one to each known key.
//
// # Calibration sources
//
// Anchor points come from the protocol notes and are confirmed against a real
// board capture (StudioLive 32R, firmware 3.4.0). The featherbear
// logVolumeToLinear cubic was evaluated as the fader curve and rejected: it
// matches the endpoints (-84 dB -> 0, +10 dB -> 1.0) and unity, but places
// pos 0.746 at roughly 0 dB (its own docs pin unity at pos 72), whereas the
// board capture puts -6 dB at 0.746 (aux masters and monitor sends all default
// to -6 dB and read 0.746). A ~7 dB miss on a confirmed interior point means a
// blind port cannot pass calibration, so the fader is fit to the measured
// anchors instead.
package taper

import (
	"errors"
	"fmt"
	"math"
)

// Taper converts between a human unit and the board's normalized 0..1 wire
// position. Names and signatures are fixed by the ucmix design doc (§3.5).
type Taper interface {
	// ToWire maps a human value (dB, Hz, ms, ...) to a 0..1 wire position.
	// Values below the control's range clamp to the bottom; values above it
	// return an error (see each taper's documented range).
	ToWire(human float64) (float64, error)
	// FromWire maps a 0..1 wire position back to the human value. The position
	// is clamped to the valid range; it never errors.
	FromWire(pos float64) float64
	// Unit is the human unit this taper speaks: "dB", "Hz", "ms", "input".
	Unit() string
}

// ErrOverRange is returned by ToWire when a human value exceeds the control's
// maximum (e.g. +50 dB on a fader that tops out at +10 dB).
var ErrOverRange = errors.New("taper: value above control range")

// Package-level tapers, one per modeled control. See §3.5 of the design doc.
var (
	// Fader converts channel fader level (line/chN/volume) in dB to position.
	// Anchors: 0 -> -84 dB (bottom), 0.746 -> -6 dB, 1.0 -> +10 dB. Unity
	// (0 dB) therefore lands near pos 0.84 — a consequence of the one measured
	// interior point, not of the vague "unity near mid" descriptor, which has
	// no datapoint behind it (no fader sits at unity in the capture).
	//
	// Phase-0 approximation: only three points are known, so this linearly
	// interpolates between them. Below 0.746 the curve is unvalidated.
	// TODO(phase2-hw): capture more fader dB<->position points and refit.
	Fader Taper = anchorTaper{
		unit: "dB",
		pts: []anchor{
			{pos: 0.0, human: -84},
			{pos: 0.746, human: -6},
			{pos: 1.0, human: 10},
		},
	}

	// SendLevel converts monitor-send level (line/chN/auxM) in dB to position.
	// Same curve as Fader — the board uses one level taper for sends and
	// faders, and the capture shows sends defaulting to 0.746 = -6 dB.
	SendLevel = Fader

	// LimiterThresh converts aux limiter threshold (aux/chN/limit/threshold) in
	// dB to position. Linear over -28..0 dB: pos = (dB + 28) / 28. Confirmed
	// exactly by the capture (0.785714 = -6 dB, 1.0 = 0 dB).
	LimiterThresh Taper = anchorTaper{
		unit: "dB",
		pts: []anchor{
			{pos: 0.0, human: -28},
			{pos: 1.0, human: 0},
		},
	}

	// InputPatch converts a physical input number to position: pos = input/32;
	// inverse rounds to the nearest input. Confirmed: input 25 -> 0.78125,
	// input 32 -> 1.0. Valid inputs are 0..32 (0 = unpatched).
	InputPatch Taper = patchTaper{}

	// Release converts aux limiter release (aux/chN/limit/release) in ms to
	// position. Only one point is known (0.5 = 400 ms), so this is a documented
	// linear stub over 0..800 ms. Do not trust it beyond the single anchor.
	// TODO(phase2-hw): calibrate release curve.
	Release Taper = anchorTaper{
		unit: "ms",
		pts: []anchor{
			{pos: 0.0, human: 0},
			{pos: 1.0, human: 800},
		},
	}

	// HPF converts the high-pass filter (line/chN/filter/hpf) 0..1 <-> Hz. The
	// curve is unknown, so this is a raw pass-through: the value is returned
	// unchanged in both directions (0 = filter off; the config `raw:` escape
	// hatch relies on this identity). The capture shows an active channel at
	// 0.0598, so the true Hz mapping is still undecoded.
	// TODO(phase2-hw): calibrate 0..1 -> Hz.
	HPF Taper = passThroughTaper{}
)

// anchor is one (position, human-value) calibration point.
type anchor struct {
	pos   float64
	human float64
}

// anchorTaper linearly interpolates between calibration anchors, sorted by
// ascending position with strictly ascending human values. It backs the fader,
// send, limiter-threshold, and release tapers.
type anchorTaper struct {
	unit string
	pts  []anchor
}

func (t anchorTaper) Unit() string { return t.unit }

// FromWire clamps pos into the anchor range, then linearly interpolates the
// human value.
func (t anchorTaper) FromWire(pos float64) float64 {
	lo, hi := t.pts[0], t.pts[len(t.pts)-1]
	if pos <= lo.pos {
		return lo.human
	}
	if pos >= hi.pos {
		return hi.human
	}
	for i := 1; i < len(t.pts); i++ {
		a, b := t.pts[i-1], t.pts[i]
		if pos <= b.pos {
			f := (pos - a.pos) / (b.pos - a.pos)
			return a.human + f*(b.human-a.human)
		}
	}
	return hi.human
}

// ToWire linearly interpolates the position for a human value. Values below the
// lowest anchor clamp to the bottom position (e.g. -inf/mute); values above the
// highest anchor return ErrOverRange.
func (t anchorTaper) ToWire(human float64) (float64, error) {
	lo, hi := t.pts[0], t.pts[len(t.pts)-1]
	if human < lo.human {
		return lo.pos, nil
	}
	if human > hi.human {
		return 0, fmt.Errorf("%w: %.4g %s > %.4g %s", ErrOverRange, human, t.unit, hi.human, t.unit)
	}
	for i := 1; i < len(t.pts); i++ {
		a, b := t.pts[i-1], t.pts[i]
		if human <= b.human {
			f := (human - a.human) / (b.human - a.human)
			return a.pos + f*(b.pos-a.pos), nil
		}
	}
	return hi.pos, nil
}

// patchTaper maps physical input numbers to positions: pos = input/32.
type patchTaper struct{}

func (patchTaper) Unit() string { return "input" }

// FromWire returns the nearest input number for a position.
func (patchTaper) FromWire(pos float64) float64 {
	if pos < 0 {
		pos = 0
	}
	if pos > 1 {
		pos = 1
	}
	return math.Round(pos * 32)
}

// ToWire maps an input number (0..32) to a position. Out-of-range inputs error.
func (patchTaper) ToWire(input float64) (float64, error) {
	if input < 0 || input > 32 {
		return 0, fmt.Errorf("%w: input %.4g outside 0..32", ErrOverRange, input)
	}
	return input / 32, nil
}

// passThroughTaper returns values unchanged in both directions. Used for HPF
// until the 0..1 -> Hz curve is calibrated.
type passThroughTaper struct{}

func (passThroughTaper) Unit() string                          { return "Hz" }
func (passThroughTaper) FromWire(pos float64) float64          { return pos }
func (passThroughTaper) ToWire(human float64) (float64, error) { return human, nil }
