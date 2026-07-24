package cli

import "testing"

// TestChannelVerbPaths asserts each channel verb builds the line/ch{n} path it
// documents — the core promise of the veneer: `channel 3 color` → line/ch3/color.
func TestChannelVerbPaths(t *testing.T) {
	want := map[string]string{
		"name":    "line/ch3/username",
		"patch":   "line/ch3/adc_src",
		"phantom": "line/ch3/48v",
		"fader":   "line/ch3/volume",
		"mute":    "line/ch3/mute",
		"stereo":  "line/ch3/link",
		"color":   "line/ch3/color",
		"icon":    "line/ch3/iconid",
		"hpf":     "line/ch3/filter/hpf",
	}
	for verb, wantPath := range want {
		v, ok := channelVerbByName(verb)
		if !ok {
			t.Errorf("channelVerbByName(%q): not found", verb)
			continue
		}
		if got := channelPath(3, v.suffix); got != wantPath {
			t.Errorf("channel 3 %s → %q, want %q", verb, got, wantPath)
		}
	}
	if _, ok := channelVerbByName("bogus"); ok {
		t.Error("channelVerbByName(\"bogus\") = true, want false")
	}
}

// TestMixVerbPaths asserts the single-write mix verbs map to aux/ch{n} paths.
func TestMixVerbPaths(t *testing.T) {
	want := map[string]string{
		"name":   "aux/ch2/username",
		"stereo": "aux/ch2/link",
		"fader":  "aux/ch2/volume",
	}
	for verb, wantPath := range want {
		suffix, ok := mixSuffix[verb]
		if !ok {
			t.Errorf("mixSuffix[%q]: not found", verb)
			continue
		}
		if got := auxPath(2, suffix); got != wantPath {
			t.Errorf("mix 2 %s → %q, want %q", verb, got, wantPath)
		}
	}
}

func TestSendPath(t *testing.T) {
	if got := sendPath(3, 1); got != "line/ch3/aux1" {
		t.Errorf("sendPath(3,1) = %q, want line/ch3/aux1", got)
	}
}

func TestParseIndex(t *testing.T) {
	tests := []struct {
		in      string
		want    int
		wantErr bool
	}{
		{"1", 1, false},
		{"32", 32, false},
		{" 4 ", 4, false},
		{"0", 0, true},
		{"-1", 0, true},
		{"abc", 0, true},
	}
	for _, tt := range tests {
		got, err := parseIndex(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("parseIndex(%q): want error", tt.in)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("parseIndex(%q) = %d, %v; want %d, nil", tt.in, got, err, tt.want)
		}
	}
}

func TestResolveColor(t *testing.T) {
	tests := map[string]string{
		"blue":    "4ed2ff",
		"BLUE":    "4ed2ff",
		"red":     "ff0000",
		"green":   "52bc4d",
		"4ed2ff":  "4ed2ff",  // hex passes through
		"#abcdef": "#abcdef", // unknown passes through unchanged
	}
	for in, want := range tests {
		if got := resolveColor(in); got != want {
			t.Errorf("resolveColor(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveIcon(t *testing.T) {
	tests := map[string]string{
		"drums":         "drums/drumset",
		"bass":          "guitars/bass",
		"vocal":         "vocals/leadvocals",
		"vocals/custom": "vocals/custom", // raw id passes through
	}
	for in, want := range tests {
		if got := resolveIcon(in); got != want {
			t.Errorf("resolveIcon(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseLimiterOpts(t *testing.T) {
	tests := []struct {
		name          string
		args          []string
		wantThreshold string
		wantRelease   string
		wantErr       bool
	}{
		{"none", nil, "", "", false},
		{"threshold only", []string{"--threshold", "-6"}, "-6", "", false},
		{"release only", []string{"--release", "400"}, "", "400", false},
		{"both", []string{"--threshold", "-6", "--release", "400"}, "-6", "400", false},
		{"missing threshold value", []string{"--threshold"}, "", "", true},
		{"unexpected arg", []string{"loud"}, "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotT, gotR, err := parseLimiterOpts(tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatal("want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotT != tt.wantThreshold || gotR != tt.wantRelease {
				t.Errorf("parseLimiterOpts(%v) = %q, %q; want %q, %q", tt.args, gotT, gotR, tt.wantThreshold, tt.wantRelease)
			}
		})
	}
}
