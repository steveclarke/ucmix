package dawplugins

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func load(t *testing.T) []Plugin {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "plugins.settings"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	all, err := Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	return all
}

func TestParseKeepsEffectsAndInstruments(t *testing.T) {
	all := load(t)
	if len(all) != 4 {
		t.Fatalf("plugins = %+v", all)
	}
	if all[0].Name != "Amp Thing 7" || all[0].Vendor != "Example Audio" || all[0].Format != "VST3" {
		t.Errorf("first = %+v", all[0])
	}
	for _, p := range all {
		if p.Name == "Pro Codec" {
			t.Error("codec kept")
		}
		if p.Name == "Mai Tai" && !p.Instrument() {
			t.Error("Mai Tai should be an instrument")
		}
	}
}

func TestFind(t *testing.T) {
	all := load(t)
	tests := []struct {
		name, want, err string
	}{
		{"pro eq", "Pro EQ", ""},
		{"amp", "Amp Thing 7", ""},
		{"thing", "Amp Thing 7", ""},
		{"legacy", "Pro EQ Legacy", ""},
		{"pro", "", "matches"},
		{"guitar rig", "", "no plug-in"},
	}
	for _, tt := range tests {
		got, err := Find(all, tt.name)
		if tt.err != "" {
			if err == nil || !strings.Contains(err.Error(), tt.err) {
				t.Errorf("Find(%q) err = %v", tt.name, err)
			}
			continue
		}
		if err != nil || got.Name != tt.want {
			t.Errorf("Find(%q) = %+v, %v", tt.name, got, err)
		}
	}
}

func TestFindRefusesSameNameFromDifferentVendors(t *testing.T) {
	all := []Plugin{{Name: "Comp", Vendor: "A", ClassID: "1"}, {Name: "Comp", Vendor: "B", ClassID: "2"}}
	if _, err := Find(all, "comp"); err == nil || !strings.Contains(err.Error(), "(B)") {
		t.Errorf("err = %v", err)
	}
	same := []Plugin{{Name: "Comp", Vendor: "A", Format: "VST3"}, {Name: "Comp", Vendor: "A", Format: "AU"}}
	if p, err := Find(same, "comp"); err != nil || p.Format != "VST3" {
		t.Errorf("same vendor = %+v, %v", p, err)
	}
}
