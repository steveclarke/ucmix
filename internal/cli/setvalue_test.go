package cli

import (
	"reflect"
	"testing"

	"github.com/steveclarke/ucmix/internal/schema"
)

func TestParseSetValueKnownKeys(t *testing.T) {
	tests := []struct {
		name string
		path string
		raw  string
		want any
	}{
		{"bool on", "line/ch1/mute", "on", true},
		{"bool off", "line/ch1/mute", "off", false},
		{"bool true", "line/ch1/mute", "true", true},
		{"fader dB", "line/ch1/volume", "-6dB", -6.0},
		{"fader bare float", "line/ch1/volume", "0.5", 0.5},
		{"hpf Hz", "line/ch1/filter/hpf", "100Hz", 100.0},
		{"string name", "line/ch1/username", "Kick", "Kick"},
		{"string quoted", "line/ch1/username", `"Kick Drum"`, "Kick Drum"},
		// Colors parse to the canonical 8-digit RGBA hex — the one rendering
		// ucmix writes, prints, and compares (issue #30).
		{"color rgb gets alpha", "line/ch1/color", "4ed2ff", "4ed2ffff"},
		{"color hash prefix", "line/ch1/color", "#4ed2ff", "4ed2ffff"},
		{"color rgba kept", "line/ch1/color", "4ed2ff80", "4ed2ff80"},
		{"color uppercase lowercases", "line/ch1/color", "4ED2FF", "4ed2ffff"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec, known := schema.Lookup(tt.path)
			if !known {
				t.Fatalf("schema.Lookup(%q) not known — fix the test path", tt.path)
			}
			got, err := parseSetValue(spec, known, tt.raw)
			if err != nil {
				t.Fatalf("parseSetValue: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseSetValue(%q) = %#v, want %#v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestParseSetValueErrors(t *testing.T) {
	tests := []struct {
		name string
		path string
		raw  string
	}{
		{"bad bool", "line/ch1/mute", "maybe"},
		{"bad float", "line/ch1/volume", "loud"},
		{"bad color hex", "line/ch1/color", "zzz"},
		{"bad color length", "line/ch1/color", "4ed2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec, known := schema.Lookup(tt.path)
			if _, err := parseSetValue(spec, known, tt.raw); err == nil {
				t.Errorf("parseSetValue(%q) = nil error, want error", tt.raw)
			}
		})
	}
}

func TestParseSetValueUnknownKey(t *testing.T) {
	spec, known := schema.Lookup("some/unknown/key")
	if known {
		t.Fatal("expected some/unknown/key to be unknown")
	}
	tests := []struct {
		raw  string
		want any
	}{
		{"on", true},
		{"off", false},
		{"3.5", 3.5},
		{"hello", "hello"},
	}
	for _, tt := range tests {
		got, err := parseSetValue(spec, known, tt.raw)
		if err != nil {
			t.Fatalf("parseSetValue(%q): %v", tt.raw, err)
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("parseSetValue(%q) = %#v, want %#v", tt.raw, got, tt.want)
		}
	}
}
