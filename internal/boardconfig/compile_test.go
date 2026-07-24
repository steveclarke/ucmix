package boardconfig

import (
	"math"
	"testing"
)

// TestCompileOrder pins the full ordered path sequence for the golden config,
// asserting the fixed compiler ordering (identity → links → patch →
// preamp/48V/HPF → assigns → levels/sends → mix masters → limiters → FX type →
// FX returns → raw) and that every sugar expansion lands where expected.
func TestCompileOrder(t *testing.T) {
	cfg, err := Load([]byte(validConfig))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	desired, err := Compile(cfg)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	want := []string{
		// identity
		"line/ch1/username",
		"line/ch1/iconid",
		"line/ch1/color",
		"aux/ch1/username",
		"aux/ch5/username",
		"fxreturn/ch1/username",
		// links (channel triple, then stereo-mix triple)
		"line/ch1/link",
		"line/ch1/linkmaster",
		"line/ch1/panlinkstate",
		"aux/ch1/link",
		"aux/ch1/linkmaster",
		"aux/ch1/panlinkstate",
		// patch
		"line/ch1/adc_src",
		"line/ch2/adc_src",
		// preamp / 48V / HPF
		"line/ch1/48v",
		"line/ch1/filter/hpf",
		// assigns
		"line/ch1/lr",
		"line/ch1/assign_fx1",
		// levels / sends
		"line/ch1/volume",
		"line/ch1/mute",
		"line/ch1/aux5", // Guitars → mix key 5 (direct; 2M-1 would wrongly give aux9)
		"line/ch1/aux1", // Steve → mix key 1
		"line/ch1/aux3", // raw auxN form
		"line/ch1/FXA",
		// mix masters
		"aux/ch1/volume",
		"aux/ch1/auxpremode",
		// limiters
		"aux/ch1/limit/limiteron",
		"aux/ch1/limit/threshold",
		"aux/ch1/limit/release",
		// FX type
		"fx/ch1/type",
		// FX returns
		"fxreturn/ch1/aux1",
		"fxreturn/ch1/mute",
		// raw
		"line/ch18/somekey",
	}

	got := make([]string, len(desired))
	for i, d := range desired {
		got[i] = d.Path
	}
	if len(got) != len(want) {
		t.Fatalf("desired paths count = %d, want %d\n got: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("path[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestCompileSugar asserts each sugar expansion produced the right wire/human
// values, not just the right paths.
func TestCompileSugar(t *testing.T) {
	cfg, err := Load([]byte(validConfig))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	desired, err := Compile(cfg)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	byPath := index(desired)

	// link: true → three bool wire keys, all true.
	for _, p := range []string{"line/ch1/link", "line/ch1/linkmaster", "line/ch1/panlinkstate"} {
		if d, ok := byPath[p]; !ok || d.WireValue != true {
			t.Errorf("%s = %+v, want wire true", p, d)
		}
	}
	// stereo: true → aux-pair link keys, all true.
	for _, p := range []string{"aux/ch1/link", "aux/ch1/linkmaster", "aux/ch1/panlinkstate"} {
		if d, ok := byPath[p]; !ok || d.WireValue != true {
			t.Errorf("%s = %+v, want wire true", p, d)
		}
	}
	// mix-name send: Steve resolves to mix key 1 → aux1, human -6, wire ~0.746.
	if d := byPath["line/ch1/aux1"]; d.HumanValue != -6.0 {
		t.Errorf("line/ch1/aux1 human = %v, want -6", d.HumanValue)
	} else if approx(toF(d.WireValue), 0.746, 1e-6) == false {
		t.Errorf("line/ch1/aux1 wire = %v, want ~0.746", d.WireValue)
	}
	// mix-name send that discriminates direct mapping from 2M-1: Guitars is mix
	// key 5, so a direct map emits aux5; 2M-1 would wrongly emit aux9.
	if _, ok := byPath["line/ch1/aux5"]; !ok {
		t.Errorf("Guitars send did not land at line/ch1/aux5 (mix key = aux index)")
	}
	if _, ok := byPath["line/ch1/aux9"]; ok {
		t.Errorf("line/ch1/aux9 present — send used 2M-1, not the direct mix-key mapping")
	}
	// fx A on a channel → FXA level (human -20) AND assign_fx1 on.
	if d, ok := byPath["line/ch1/FXA"]; !ok || d.HumanValue != -20.0 {
		t.Errorf("line/ch1/FXA = %+v, want human -20", d)
	}
	if d, ok := byPath["line/ch1/assign_fx1"]; !ok || d.WireValue != true {
		t.Errorf("line/ch1/assign_fx1 = %+v, want wire true", d)
	}
	// color: 6-hex input gains opaque alpha on the wire.
	if d := byPath["line/ch1/color"]; d.WireValue != "4ed2ffff" {
		t.Errorf("line/ch1/color wire = %v, want 4ed2ffff", d.WireValue)
	}
	// patch: input 25 → 25/32 = 0.78125.
	if d := byPath["line/ch1/adc_src"]; approx(toF(d.WireValue), 25.0/32.0, 1e-9) == false {
		t.Errorf("line/ch1/adc_src wire = %v, want 0.78125", d.WireValue)
	}
	// volume: -6 dB → pos 0.746 × ReadScale 1 = 0.746 (plain-wire snapshot scale).
	if d := byPath["aux/ch1/volume"]; approx(toF(d.WireValue), 0.746, 1e-6) == false {
		t.Errorf("aux/ch1/volume wire = %v, want ~0.746", d.WireValue)
	}
	// pre2 → auxpremode 0.5 enum float, human kept as the name.
	if d := byPath["aux/ch1/auxpremode"]; toF(d.WireValue) != 0.5 || d.HumanValue != "pre2" {
		t.Errorf("aux/ch1/auxpremode = %+v, want wire 0.5 human pre2", d)
	}
	// FX type name → calibrated enum float.
	if d := byPath["fx/ch1/type"]; toF(d.WireValue) != 0.375 || d.HumanValue != "vintage-plate" {
		t.Errorf("fx/ch1/type = %+v, want wire 0.375 human vintage-plate", d)
	}
	// mains: muted → fxreturn mute on.
	if d := byPath["fxreturn/ch1/mute"]; d.WireValue != true {
		t.Errorf("fxreturn/ch1/mute wire = %v, want true", d.WireValue)
	}
	// raw passthrough: verbatim path and value, no taper.
	if d, ok := byPath["line/ch18/somekey"]; !ok || d.WireValue != 0.5 {
		t.Errorf("raw line/ch18/somekey = %+v, want wire 0.5", d)
	}
}

// TestCompileErrors covers the taper/enum errors Compile surfaces.
func TestCompileErrors(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"unknown mix name send", mustLoad(t, "channels:\n  1:\n    sends:\n      Ghost: -6\n")},
		{"unknown fx type", mustLoad(t, "fx:\n  A:\n    type: space-echo\n")},
		{"unknown pre mode", mustLoad(t, "mixes:\n  1:\n    pre: pre9\n")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Compile(tc.cfg); err == nil {
				t.Fatalf("Compile(%s) = nil error; want error", tc.name)
			}
		})
	}
}

func mustLoad(t *testing.T, y string) Config {
	t.Helper()
	cfg, err := Load([]byte(y))
	if err != nil {
		t.Fatalf("Load(%q): %v", y, err)
	}
	return cfg
}

func index(ds []Desired) map[string]Desired {
	m := make(map[string]Desired, len(ds))
	for _, d := range ds {
		m[d.Path] = d
	}
	return m
}

func toF(v any) float64 {
	f, _ := asFloat(v)
	return f
}

func approx(a, b, tol float64) bool { return math.Abs(a-b) <= tol }
