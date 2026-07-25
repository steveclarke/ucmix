package boardconfig

import (
	"math"
	"strings"
	"testing"
)

// validConfig is the annotated §5.1 example (comments stripped), the golden
// target schema. It must parse clean and exercise every modeled section.
const validConfig = `
version: 1

mixer:
  model: StudioLive 32R

channels:
  1:
    name: Drums
    icon: drums/drumset
    color: "4ed2ff"
    link: true
    patch: 25
    phantom: true
    hpf: 100
    main: true
    fader: -6
    mute: false
    sends:
      Steve: -6
      Guitars: -6
      aux3: -6
    fx:
      A: -20
  2:
    patch: 26

mixes:
  1:
    name: Steve
    stereo: true
    fader: -6
    pre: pre2
    limiter:
      "on": true
      threshold: -6
      release: 400
  5:
    name: Guitars

fx:
  A:
    type: vintage-plate

fxreturns:
  A:
    name: FX Ret A
    sends: { Steve: -12 }
    mains: muted

raw:
  "line/ch18/somekey": 0.5
`

func TestLoadValid(t *testing.T) {
	cfg, err := Load([]byte(validConfig))
	if err != nil {
		t.Fatalf("Load(validConfig) error: %v", err)
	}
	if cfg.Version != 1 {
		t.Errorf("Version = %d, want 1", cfg.Version)
	}
	if cfg.Mixer.Model != "StudioLive 32R" {
		t.Errorf("Mixer.Model = %q, want %q", cfg.Mixer.Model, "StudioLive 32R")
	}
	ch1 := cfg.Channels[1]
	if ch1.Name == nil || *ch1.Name != "Drums" {
		t.Errorf("channels.1.name = %v, want Drums", ch1.Name)
	}
	if ch1.Link == nil || !*ch1.Link {
		t.Errorf("channels.1.link = %v, want true", ch1.Link)
	}
	if ch1.HPF == nil || ch1.HPF.Hz == nil || *ch1.HPF.Hz != 100 {
		t.Errorf("channels.1.hpf = %+v, want Hz 100", ch1.HPF)
	}
	if ch1.Mute == nil || *ch1.Mute {
		t.Errorf("channels.1.mute = %v, want false (declared)", ch1.Mute)
	}
	if lvl, ok := ch1.Sends["Steve"]; !ok || lvl.DB != -6 {
		t.Errorf("channels.1.sends.Steve = %+v, want -6", lvl)
	}
	if cfg.Mixes[1].Name == nil || *cfg.Mixes[1].Name != "Steve" {
		t.Errorf("mixes.1.name = %v, want Steve", cfg.Mixes[1].Name)
	}
	if cfg.Mixes[1].Limiter == nil || cfg.Mixes[1].Limiter.On == nil || !*cfg.Mixes[1].Limiter.On {
		t.Errorf("mixes.1.limiter.on not parsed true: %+v", cfg.Mixes[1].Limiter)
	}
	if cfg.FXReturns["A"].Mains == nil || *cfg.FXReturns["A"].Mains != "muted" {
		t.Errorf("fxreturns.A.mains = %v, want muted", cfg.FXReturns["A"].Mains)
	}
	if v, ok := cfg.Raw["line/ch18/somekey"]; !ok || v != 0.5 {
		t.Errorf("raw[line/ch18/somekey] = %v, want 0.5", v)
	}
}

// TestLoadInvalid is one case per validation failure mode. Each must error, and
// the error must name the offending field.
func TestLoadInvalid(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string // substring the error must contain
	}{
		{
			name: "unknown field",
			yaml: "channels:\n  1:\n    nam: Drums\n",
			want: "nam",
		},
		{
			name: "even channel link",
			yaml: "channels:\n  2:\n    link: true\n",
			want: "channels.2.link",
		},
		{
			name: "even mix stereo",
			yaml: "mixes:\n  2:\n    stereo: true\n",
			want: "mixes.2.stereo",
		},
		{
			name: "out of range dB",
			yaml: "channels:\n  1:\n    fader: 50\n",
			want: "channels.1.fader",
		},
		{
			name: "out of range Hz",
			yaml: "channels:\n  1:\n    hpf: 99999\n",
			want: "channels.1.hpf",
		},
		{
			name: "out of range patch",
			yaml: "channels:\n  1:\n    patch: 40\n",
			want: "channels.1.patch",
		},
		{
			name: "duplicate mix names",
			yaml: "mixes:\n  1:\n    name: Steve\n  3:\n    name: Steve\n",
			want: "duplicate mix name",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load([]byte(tc.yaml))
			if err == nil {
				t.Fatalf("Load(%q) = nil error; want error containing %q", tc.name, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q; want it to contain %q", err.Error(), tc.want)
			}
		})
	}
}

// TestHPFSpellings covers the three ways a config can name a filter that is off.
// They were three behaviors before the sweep was calibrated: `off` was accepted,
// `0` was rejected as an out-of-range frequency, and `raw:0.0` was what actually
// reached the board. All three are position 0 now.
func TestHPFSpellings(t *testing.T) {
	cases := []struct {
		name string
		yaml string
	}{
		{"off", "channels:\n  1:\n    hpf: off\n"},
		{"zero Hz", "channels:\n  1:\n    hpf: 0\n"},
		{"raw position", "channels:\n  1:\n    hpf: raw:0.0\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Load([]byte(tc.yaml))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			desired, err := Compile(cfg)
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			if len(desired) != 1 {
				t.Fatalf("compiled %d settings, want 1", len(desired))
			}
			if got, ok := desired[0].WireValue.(float64); !ok || got != 0 {
				t.Errorf("%s compiled to %v, want wire position 0", tc.name, desired[0].WireValue)
			}
		})
	}
}

// TestHPFRangeIsTheBoardSweep checks the config validates against the board's
// real 24 Hz – 1 kHz range rather than a generic audio band. A frequency the
// board cannot reach has to be named at load time; letting it through only pins
// the filter and reports success.
func TestHPFRangeIsTheBoardSweep(t *testing.T) {
	for _, hz := range []string{"20", "23", "1001", "20000"} {
		t.Run(hz, func(t *testing.T) {
			_, err := Load([]byte("channels:\n  1:\n    hpf: " + hz + "\n"))
			if err == nil {
				t.Fatalf("hpf: %s loaded, want an out-of-range error", hz)
			}
			if !strings.Contains(err.Error(), "24..1000") {
				t.Errorf("error = %v, want it to name the 24..1000 range", err)
			}
		})
	}
	for _, hz := range []string{"24", "80", "90", "1000"} {
		t.Run("accepts "+hz, func(t *testing.T) {
			if _, err := Load([]byte("channels:\n  1:\n    hpf: " + hz + "\n")); err != nil {
				t.Errorf("hpf: %s rejected: %v", hz, err)
			}
		})
	}
}

// TestHPFCompilesHzToPosition is the bug in one assertion: a Hz value reaches the
// board as a 0..1 position, not as the Hz number.
func TestHPFCompilesHzToPosition(t *testing.T) {
	cfg, err := Load([]byte("channels:\n  1:\n    hpf: 90\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	desired, err := Compile(cfg)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	got, ok := desired[0].WireValue.(float64)
	if !ok {
		t.Fatalf("wire value %v (%T), want a float position", desired[0].WireValue, desired[0].WireValue)
	}
	if math.Abs(got-0.35438603162765503) > 1e-5 {
		t.Errorf("hpf 90 Hz compiled to %v, want ~0.354386 (the position a 32R holds for 90 Hz)", got)
	}
}
