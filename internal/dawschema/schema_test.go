package dawschema

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadMini(t *testing.T) *Catalog {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "mini.xml"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	c, err := Parse(f)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return c
}

func TestParseExpandsPaths(t *testing.T) {
	c := loadMini(t)
	want := []string{
		"transport/start",
		"transport/primaryTime",
		"transport/tempo",
		"transport/timeSignatureNum",
		"transport/timeSignatureDenom",
		"mixer/channel/count",
		"mixer/channel/ch1/label",
		"mixer/channel/ch3/volume",
		"mixer/channel/ch2/inserts/count",
		"mixer/channel/ch2/inserts/slot2/deviceName",
		"mixer/channel/ch1/extra/title",
		"hw/ch2/slot1/deviceName",
		"hw/ch1/slot2/deviceName",
		"document/title",
	}
	for _, path := range want {
		if _, ok := c.Lookup(path); !ok {
			t.Errorf("missing %s", path)
		}
	}
	for _, path := range []string{"mixer/channel/ch4/label", "hw/ch3/slot1/deviceName", "mixer/channel/ch1/inserts/slot3/deviceName"} {
		if _, ok := c.Lookup(path); ok {
			t.Errorf("unexpected %s", path)
		}
	}
}

func TestParamTypes(t *testing.T) {
	c := loadMini(t)
	tests := []struct {
		path     string
		typ      Type
		min, max float64
		readonly bool
	}{
		{"transport/start", Toggle, 0, 1, false},
		{"transport/tempo", Float, 10, 400, false},
		{"transport/timeSignatureNum", Int, 1, 32, false},
		{"transport/timeSignatureDenom", List, 0, 2, false},
		{"mixer/channel/count", Int, 0, 1000, true},
		{"mixer/channel/ch1/gain", Float, 0, 1.5, false},
		{"mixer/channel/ch1/label", String, 0, 0, false},
	}
	for _, tt := range tests {
		p, ok := c.Lookup(tt.path)
		if !ok {
			t.Fatalf("missing %s", tt.path)
		}
		if p.Type != tt.typ || p.Min != tt.min || p.Max != tt.max || p.ReadOnly() != tt.readonly {
			t.Errorf("%s = %+v", tt.path, p)
		}
	}
	p, _ := c.Lookup("transport/primaryTime")
	if !p.Private() {
		t.Error("primaryTime should be private")
	}
	p, _ = c.Lookup("mixer/channel/ch2/volume")
	if p.Curve != "fadercurve" || p.Units != "gain.db.value" {
		t.Errorf("volume range not applied: %+v", p)
	}
	if p.Pattern != "mixer/channel/ch<n>/volume" {
		t.Errorf("pattern = %q", p.Pattern)
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-4 }

func TestWireConversion(t *testing.T) {
	c := loadMini(t)
	tempo, _ := c.Lookup("transport/tempo")
	if got := tempo.FromWire(0.2820512950420379638671875); !near(got, 120) {
		t.Errorf("tempo FromWire = %v", got)
	}
	w, err := tempo.ToWire(120)
	if err != nil || !near(w, 110.0/390.0) {
		t.Errorf("tempo ToWire = %v, %v", w, err)
	}
	if _, err := tempo.ToWire(500); err == nil {
		t.Error("tempo 500 should be out of range")
	}
	num, _ := c.Lookup("transport/timeSignatureNum")
	if got := num.FromWire(0.0967741906642913818359375); got != 4 {
		t.Errorf("num FromWire = %v", got)
	}
	count, _ := c.Lookup("mixer/channel/count")
	if got := count.FromWire(0.001); got != 1 {
		t.Errorf("count FromWire = %v", got)
	}
	denom, _ := c.Lookup("transport/timeSignatureDenom")
	if got := denom.Format(0.5); got != "4" {
		t.Errorf("denom Format = %q", got)
	}
	if w, err := denom.ParseHuman("8"); err != nil || w != 1 {
		t.Errorf("denom ParseHuman(8) = %v, %v", w, err)
	}
	start, _ := c.Lookup("transport/start")
	for in, want := range map[string]float64{"on": 1, "off": 0, "1": 1, "true": 1, "0": 0} {
		if w, err := start.ParseHuman(in); err != nil || w != want {
			t.Errorf("toggle ParseHuman(%q) = %v, %v", in, w, err)
		}
	}
	if w, err := tempo.ParseHuman("96.5"); err != nil || !near(tempo.FromWire(w), 96.5) {
		t.Errorf("tempo ParseHuman = %v, %v", w, err)
	}
	if got := tempo.Format(tempo.mustWire(t, 120)); got != "120 bpm" {
		t.Errorf("tempo Format = %q", got)
	}
}

func (p Param) mustWire(t *testing.T, v float64) float64 {
	t.Helper()
	w, err := p.ToWire(v)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestFilterCollapsesPatterns(t *testing.T) {
	c := loadMini(t)
	got := c.Patterns("deviceName")
	if len(got) != 2 {
		t.Fatalf("Patterns = %v", got)
	}
	joined := got[0].Pattern + " " + got[1].Pattern
	if !strings.Contains(joined, "mixer/channel/ch<n>/inserts/slot<n>/deviceName") ||
		!strings.Contains(joined, "hw/ch<n>/slot<n>/deviceName") {
		t.Errorf("patterns = %s", joined)
	}
	if all := c.Filter("deviceName"); len(all) != 10 {
		t.Errorf("Filter = %d params, want 10", len(all))
	}
}
