package daw

import (
	"strings"
	"testing"
)

func TestCleanStemName(t *testing.T) {
	cases := map[string]string{
		"01_Kick_Final.wav":               "Kick",
		"02 Bass DI.wav":                  "Bass DI",
		"03-Pad (Stereo) v2.wav":          "Pad",
		"04_Lead_Vox_24bit.aif":           "Lead Vox",
		"/stems/05. Snare Top.wav":        "Snare Top",
		"Hi-Hat_48k.wav":                  "Hi-Hat",
		"OH L.wav":                        "OH L",
		"vox-lead-final.wav":              "Vox-lead",
		"kick.wav":                        "Kick",
		"Snare (Mic 2).wav":               "Snare Mic 2",
		"808 Sub_120bpm_2026-10-12.wav":   "808 Sub",
		"Final.wav":                       "Final",
		"01.wav":                          "01",
		"Gtr\x1b[31mRed.wav":              "Gtr 31mRed",
		"12 BGV Stack [Stem] (Print).wav": "BGV Stack",
		"Keys_Rhodes_STEREO_v03.wav":      "Keys Rhodes",
	}
	for in, want := range cases {
		if got := cleanStemName(in); got != want {
			t.Errorf("cleanStemName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := cleanStemName(strings.Repeat("Long ", 30) + ".wav"); len([]rune(got)) > 48 {
		t.Errorf("long name not capped: %q", got)
	}
}

func TestTrackNamesDropSharedPrefixAndNumberRepeats(t *testing.T) {
	got := trackNames([]string{"Amazing Grace_Kick.wav", "Amazing Grace_Snare.wav", "Amazing Grace_Bass.wav"})
	if strings.Join(got, ",") != "Kick,Snare,Bass" {
		t.Errorf("shared prefix: %v", got)
	}
	got = trackNames([]string{"Gtr_Final.wav", "Gtr_v2.wav", "Gtr L.wav"})
	if strings.Join(got, ",") != "Gtr,Gtr 2,Gtr L" {
		t.Errorf("repeats: %v", got)
	}
	got = trackNames([]string{"Vox.wav"})
	if strings.Join(got, ",") != "Vox" {
		t.Errorf("one file: %v", got)
	}
}
