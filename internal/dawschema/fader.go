package dawschema

import "math"

// Studio Pro's fader curve ("fadercurve" in dawremote.xml), measured live
// from the dB value Studio Pro 8.0.2's console shows at 36 fader positions:
// straight lines in dB between these points, and silence (-inf) at 0.
var faderPoints = [][2]float64{
	{0.04, -60},
	{0.16, -40},
	{0.52, -10},
	{0.76, 0},
	{1, 10},
}

// faderFloorSlope continues the lowest segment to the bottom of the fader
// (-60 dB at 0.04 falling 2100 dB per unit of travel, -139.8 at 0.002).
const faderFloorSlope = 2100

// MinFaderDB is the quietest level ucdaw treats as a number; below it the
// fader is at the bottom (-inf).
const MinFaderDB = -144

// FaderDB converts a fader's wire position (0..1) to dB. 0 is -inf.
func FaderDB(wire float64) float64 {
	switch {
	case wire <= 0:
		return math.Inf(-1)
	case wire >= 1:
		return faderPoints[len(faderPoints)-1][1]
	case wire < faderPoints[0][0]:
		return faderPoints[0][1] - (faderPoints[0][0]-wire)*faderFloorSlope
	}
	for i := 1; i < len(faderPoints); i++ {
		a, b := faderPoints[i-1], faderPoints[i]
		if wire <= b[0] {
			return a[1] + (wire-a[0])*(b[1]-a[1])/(b[0]-a[0])
		}
	}
	return faderPoints[len(faderPoints)-1][1]
}

// FaderWire converts dB to the fader's wire position. -inf and anything at
// or below MinFaderDB is 0; above +10 dB is out of range (callers check).
func FaderWire(db float64) float64 {
	switch {
	case math.IsInf(db, -1) || db <= MinFaderDB:
		return 0
	case db >= faderPoints[len(faderPoints)-1][1]:
		return 1
	case db < faderPoints[0][1]:
		return math.Max(0, faderPoints[0][0]-(faderPoints[0][1]-db)/faderFloorSlope)
	}
	for i := 1; i < len(faderPoints); i++ {
		a, b := faderPoints[i-1], faderPoints[i]
		if db <= b[1] {
			return a[0] + (db-a[1])*(b[0]-a[0])/(b[1]-a[1])
		}
	}
	return 1
}
