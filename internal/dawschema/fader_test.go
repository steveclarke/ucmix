package dawschema

import (
	"math"
	"testing"
)

// Every point Studio Pro's console showed live (wire position → dB).
var measured = [][2]float64{
	{1, 10}, {0.95, 7.9}, {0.9, 5.8}, {0.85, 3.7}, {0.8, 1.7}, {0.76, 0}, {0.7, -2.5},
	{0.65, -4.6}, {0.6, -6.7}, {0.55, -8.8}, {0.53, -9.6}, {0.52, -10}, {0.51, -10.8},
	{0.5, -11.7}, {0.45, -15.8}, {0.4, -20}, {0.35, -24.2}, {0.3, -28.3}, {0.25, -32.5},
	{0.2, -36.7}, {0.17, -39.2}, {0.16, -40}, {0.15, -41.7}, {0.1, -50}, {0.05, -58.3},
	{0.045, -59.2}, {0.04, -60}, {0.035, -70.5}, {0.03, -81}, {0.025, -91.5}, {0.02, -102},
	{0.015, -112.5}, {0.01, -123}, {0.005, -133.5}, {0.002, -139.8},
}

func TestFaderCurveMatchesStudioPro(t *testing.T) {
	for _, m := range measured {
		if got := FaderDB(m[0]); math.Abs(got-m[1]) > 0.051 {
			t.Errorf("FaderDB(%v) = %.2f, Studio Pro showed %v", m[0], got, m[1])
		}
		if back := FaderWire(FaderDB(m[0])); math.Abs(back-m[0]) > 1e-9 {
			t.Errorf("FaderWire(FaderDB(%v)) = %v", m[0], back)
		}
	}
	if !math.IsInf(FaderDB(0), -1) || FaderWire(math.Inf(-1)) != 0 || FaderWire(-200) != 0 || FaderWire(20) != 1 {
		t.Error("ends of the fader")
	}
}

func TestFaderParse(t *testing.T) {
	p := Param{Path: "mixer/channel/ch1/volume", Type: Float, Curve: "fadercurve", Units: "dB", Min: MinFaderDB, Max: 10}
	for in, want := range map[string]float64{"0": 0.76, "-10 dB": 0.52, "+10dB": 1, "-inf": 0, "off": 0, "50%": 0.5, " -40 ": 0.16, "-200": 0} {
		got, err := p.ParseHuman(in)
		if err != nil || math.Abs(got-want) > 1e-9 {
			t.Errorf("ParseHuman(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"11", "loud", "150%", "NaN", "-5%"} {
		if _, err := p.ParseHuman(bad); err == nil {
			t.Errorf("ParseHuman(%q) accepted", bad)
		}
	}
	if FormatDB(1.66) != "+1.7 dB" || FormatDB(-0.04) != "0 dB" {
		t.Errorf("FormatDB = %q %q", FormatDB(1.66), FormatDB(-0.04))
	}
}
