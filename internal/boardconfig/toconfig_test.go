package boardconfig

import (
	"testing"
)

// modeledSnapshot is a raw wire snapshot (read-scale form: */volume ×100) that
// exercises every family ToConfig models, plus unmodeled keys it must drop.
func modeledSnapshot() map[string]any {
	return map[string]any{
		// unmodeled — must be dropped
		"global/mixer_name":   "Board",
		"main/ch1/volume":     74.6,
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
		"line/ch1/volume":       74.6, // -6 dB
		"line/ch1/mute":         0.0,
		"line/ch1/aux5":         0.746,   // -6 dB send
		"line/ch1/adc_src":      0.78125, // input 25
		// mix 5 (odd stereo master) with a limiter
		"aux/ch5/username":        "Wedges",
		"aux/ch5/link":            1.0,
		"aux/ch5/linkmaster":      1.0,
		"aux/ch5/panlinkstate":    1.0,
		"aux/ch5/volume":          74.6, // -6 dB
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
	// them, so ch1 must not have gained a Color/HPF/etc from them.
	ch := cfg.Channels[1]
	if ch.Color != nil {
		t.Errorf("ch1 color = %v, want nil (color is not dumped)", *ch.Color)
	}
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

func TestHPFInversion(t *testing.T) {
	off, err := ToConfig(map[string]any{"line/ch1/filter/hpf": 0.0})
	if err != nil {
		t.Fatalf("ToConfig: %v", err)
	}
	if h := off.Channels[1].HPF; h == nil || !h.Off {
		t.Errorf("hpf 0.0 = %v, want off", h)
	}
	raw, _ := ToConfig(map[string]any{"line/ch1/filter/hpf": 0.06})
	if h := raw.Channels[1].HPF; h == nil || h.Raw == nil || *h.Raw != 0.06 {
		t.Errorf("hpf 0.06 = %v, want raw:0.06", h)
	}
}
