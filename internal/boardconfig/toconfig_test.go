package boardconfig

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// modeledSnapshot is a raw wire snapshot (plain 0..1 wire, as a real 32R returns
// on read) that exercises every family ToConfig models, plus unmodeled keys it
// must drop.
func modeledSnapshot() map[string]any {
	return map[string]any{
		// unmodeled — must be dropped
		"global/mixer_name":   "Board",
		"main/ch1/volume":     0.746,
		"line/ch1/pan":        0.5,
		"line/ch1/eq/eqgain1": 0.3,
		// channel 1 (odd link master)
		"line/ch1/username":     "Vocals",
		"line/ch1/iconid":       "mic",
		"line/ch1/link":         1.0,
		"line/ch1/linkmaster":   1.0,
		"line/ch1/panlinkstate": 1.0,
		"line/ch1/48v":          1.0,
		"line/ch1/lr":           1.0,
		"line/ch1/volume":       0.746, // -6 dB
		"line/ch1/mute":         0.0,
		"line/ch1/aux5":         0.746,             // -6 dB send
		"line/ch1/adc_src":      0.78125,           // input 25
		"line/ch1/color":        int64(4288910991), // ABGR-packed 8f96a3ff
		// mix 5 (odd stereo master) with a limiter
		"aux/ch5/username":        "Wedges",
		"aux/ch5/link":            1.0,
		"aux/ch5/linkmaster":      1.0,
		"aux/ch5/panlinkstate":    1.0,
		"aux/ch5/volume":          0.746, // -6 dB
		"aux/ch5/limit/limiteron": 1.0,
		"aux/ch5/limit/threshold": 0.5, // -14 dB
		"aux/ch5/limit/release":   0.5, // 400 ms
		// fx bus A: calibrated type
		"fx/ch1/type": 0.375, // vintage-plate
		// fx return A
		"fxreturn/ch1/username": "Plate",
		"fxreturn/ch1/mute":     1.0,
		"fxreturn/ch1/aux5":     0.746,
	}
}

func TestToConfigInvertsModeledFields(t *testing.T) {
	cfg, err := ToConfig(modeledSnapshot())
	if err != nil {
		t.Fatalf("ToConfig: %v", err)
	}

	ch := cfg.Channels[1]
	if ch.Name == nil || *ch.Name != "Vocals" {
		t.Errorf("ch1 name = %v, want Vocals", ch.Name)
	}
	if ch.Link == nil || !*ch.Link {
		t.Errorf("ch1 link = %v, want true (collapsed triple)", ch.Link)
	}
	if ch.Phantom == nil || !*ch.Phantom {
		t.Errorf("ch1 phantom = %v, want true", ch.Phantom)
	}
	if ch.Fader == nil || !approx(*ch.Fader, -6, 0.2) {
		t.Errorf("ch1 fader = %v, want ~-6 dB", ch.Fader)
	}
	if ch.Patch == nil || *ch.Patch != 25 {
		t.Errorf("ch1 patch = %v, want 25", ch.Patch)
	}
	if lvl, ok := ch.Sends["aux5"]; !ok || !approx(lvl.DB, -6, 0.2) {
		t.Errorf("ch1 send aux5 = %v, want ~-6 dB", ch.Sends["aux5"])
	}

	mix := cfg.Mixes[5]
	if mix.Name == nil || *mix.Name != "Wedges" {
		t.Errorf("mix5 name = %v, want Wedges", mix.Name)
	}
	if mix.Stereo == nil || !*mix.Stereo {
		t.Errorf("mix5 stereo = %v, want true", mix.Stereo)
	}
	if mix.Limiter == nil || mix.Limiter.On == nil || !*mix.Limiter.On {
		t.Fatalf("mix5 limiter.on = %v, want true", mix.Limiter)
	}
	if !approx(*mix.Limiter.Threshold, -14, 0.2) {
		t.Errorf("mix5 threshold = %v, want ~-14 dB", *mix.Limiter.Threshold)
	}
	if !approx(*mix.Limiter.Release, 400, 1) {
		t.Errorf("mix5 release = %v, want ~400 ms", *mix.Limiter.Release)
	}

	if fx := cfg.FX["A"]; fx.Type == nil || *fx.Type != "vintage-plate" {
		t.Errorf("fx A type = %v, want vintage-plate", fx.Type)
	}

	fr := cfg.FXReturns["A"]
	if fr.Name == nil || *fr.Name != "Plate" {
		t.Errorf("fxreturn A name = %v, want Plate", fr.Name)
	}
	if fr.Mains == nil || *fr.Mains != "muted" {
		t.Errorf("fxreturn A mains = %v, want muted", fr.Mains)
	}
}

func TestToConfigDropsUnmodeledKeys(t *testing.T) {
	cfg, err := ToConfig(modeledSnapshot())
	if err != nil {
		t.Fatalf("ToConfig: %v", err)
	}
	// pan and eq are modeled in the schema but not in Config → no field carries
	// them, so ch1 must not have gained an HPF/etc from them.
	ch := cfg.Channels[1]
	if ch.HPF != nil {
		t.Errorf("ch1 hpf = %v, want nil (no hpf key in snapshot)", ch.HPF)
	}
	// main/ch1/volume and global/mixer_name are unmodeled → dropped. main is not
	// a channel/mix family, so no phantom entities appear.
	if _, ok := cfg.Channels[0]; ok {
		t.Error("unexpected channel 0 from an unmodeled key")
	}
}

// TestCompileToConfigRoundTrip is the core invariant: ToConfig then Compile must
// Diff-clean against the original snapshot for every modeled field.
func TestCompileToConfigRoundTrip(t *testing.T) {
	snap := modeledSnapshot()
	cfg, err := ToConfig(snap)
	if err != nil {
		t.Fatalf("ToConfig: %v", err)
	}
	desired, err := Compile(cfg)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if drift := Diff(desired, snap); len(drift) != 0 {
		for _, m := range drift {
			t.Errorf("drift: %s want %v got %v", m.Path, m.Want, m.Got)
		}
	}
}

// TestToConfigOutputLoads confirms a dumped config passes Load's validation
// (odd-only link/stereo, in-range dB), so `dump --as-config | verify` works.
func TestToConfigOutputLoads(t *testing.T) {
	cfg, err := ToConfig(modeledSnapshot())
	if err != nil {
		t.Fatalf("ToConfig: %v", err)
	}
	if err := validate(cfg); err != nil {
		t.Fatalf("dumped config fails validate: %v", err)
	}
}

func TestToConfigLinkOnlyOnOddMaster(t *testing.T) {
	// A link flag on an even channel must not become link:true (Load would reject
	// it); ToConfig mirrors Compile's odd-master contract.
	cfg, err := ToConfig(map[string]any{"line/ch2/link": 1.0})
	if err != nil {
		t.Fatalf("ToConfig: %v", err)
	}
	if ch := cfg.Channels[2]; ch.Link != nil {
		t.Errorf("ch2 link = %v, want nil (even channel is a slave)", ch.Link)
	}
}

// TestHPFInversion checks that a high-pass comes back as the frequency a human
// would recognize, so `dump --as-config` produces a config apply and verify can
// round-trip. The positions are read from a real 32R.
func TestHPFInversion(t *testing.T) {
	off, err := ToConfig(map[string]any{"line/ch1/filter/hpf": 0.0})
	if err != nil {
		t.Fatalf("ToConfig: %v", err)
	}
	if h := off.Channels[1].HPF; h == nil || !h.Off {
		t.Errorf("hpf 0.0 = %v, want off", h)
	}

	cases := []struct {
		pos float64
		hz  float64
	}{
		{0.35438603162765503, 90},
		{0.13696199655532837, 40},
		{0.38263556361198425, 100},
		{1.0, 1000},
	}
	for _, tc := range cases {
		cfg, err := ToConfig(map[string]any{"line/ch1/filter/hpf": tc.pos})
		if err != nil {
			t.Fatalf("ToConfig: %v", err)
		}
		h := cfg.Channels[1].HPF
		if h == nil || h.Hz == nil {
			t.Fatalf("hpf %v = %v, want Hz %v", tc.pos, h, tc.hz)
		}
		if *h.Hz != tc.hz {
			t.Errorf("hpf %v = %v Hz, want %v", tc.pos, *h.Hz, tc.hz)
		}
	}
}

// TestHPFRoundTripsThroughCompile is the round-trip the issue asks for: a
// position on the board inverts to Hz, and compiling that Hz back lands on the
// same position, so a dumped config applies and verifies clean.
func TestHPFRoundTripsThroughCompile(t *testing.T) {
	for _, pos := range []float64{0.0, 0.13696199655532837, 0.35438603162765503, 0.38263556361198425, 1.0} {
		cfg, err := ToConfig(map[string]any{"line/ch1/filter/hpf": pos})
		if err != nil {
			t.Fatalf("ToConfig: %v", err)
		}
		desired, err := Compile(cfg)
		if err != nil {
			t.Fatalf("Compile: %v", err)
		}
		drift := Diff(desired, map[string]any{"line/ch1/filter/hpf": pos})
		if len(drift) != 0 {
			t.Errorf("pos %v round-tripped with drift: %+v", pos, drift)
		}
	}
}

// TestToConfigEmitsCanonicalColor is the issue #30 round-trip half: a color in
// the snapshot (the ABGR-packed integer a real board reports) must come back as
// canonical hex, so `dump --as-config` then `verify` is clean on a board with
// colors set. Master drops color entirely, so this fails there.
func TestToConfigEmitsCanonicalColor(t *testing.T) {
	cfg, err := ToConfig(modeledSnapshot())
	if err != nil {
		t.Fatalf("ToConfig: %v", err)
	}
	ch := cfg.Channels[1]
	if ch.Color == nil {
		t.Fatal("channel 1 color = nil, want the canonical hex for 4288910991")
	}
	if *ch.Color != "8f96a3ff" {
		t.Errorf("channel 1 color = %q, want \"8f96a3ff\"", *ch.Color)
	}
}

// TestToConfigColorSurvivesYAML pins the `dump --as-config | verify` path end to
// end for color: the dumped config must marshal to YAML, load back, and compile
// to the same wire value — including an all-numeric color, which YAML would
// otherwise read back as an integer.
func TestToConfigColorSurvivesYAML(t *testing.T) {
	for _, packed := range []int64{4288910991, 0, 0x78563412} {
		snap := map[string]any{"line/ch1/color": packed}
		cfg, err := ToConfig(snap)
		if err != nil {
			t.Fatalf("ToConfig: %v", err)
		}
		out, err := yaml.Marshal(cfg)
		if err != nil {
			t.Fatalf("yaml.Marshal: %v", err)
		}
		loaded, err := Load(out)
		if err != nil {
			t.Fatalf("Load(%q): %v", out, err)
		}
		desired, err := Compile(loaded)
		if err != nil {
			t.Fatalf("Compile: %v", err)
		}
		if drift := Diff(desired, snap); len(drift) != 0 {
			t.Errorf("packed %d drifted after a YAML round-trip: %+v", packed, drift)
		}
	}
}
