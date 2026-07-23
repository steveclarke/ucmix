package ui

import (
	"strings"
	"testing"
)

func TestInitDisablesStyling(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	Init(true) // --no-color
	defer Init(false)
	if Enabled() {
		t.Fatal("Enabled() = true after Init(true), want false")
	}
	// With styling off, helpers return plain text (no ANSI escapes).
	if got := Key("host"); got != "host" {
		t.Errorf("Key = %q, want plain %q", got, "host")
	}
	if got := Hint("check"); got != "check" {
		t.Errorf("Hint = %q, want plain %q", got, "check")
	}
}

func TestInitHonorsNoColorEnv(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	Init(false) // no --no-color flag, but env is set
	defer Init(false)
	if Enabled() {
		t.Fatal("Enabled() = true with NO_COLOR set, want false")
	}
}

func TestKeyValueAndTablePlain(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	Init(false)
	defer Init(false)

	if got := KeyValue("mute", true); got != "mute: true" {
		t.Errorf("KeyValue = %q, want %q", got, "mute: true")
	}

	table := SortedTable(map[string]string{"b": "2", "a": "1"})
	// Sorted ascending: 'a' row before 'b' row.
	if !strings.HasPrefix(table, "a") || strings.Index(table, "a") > strings.Index(table, "b") {
		t.Errorf("SortedTable not sorted: %q", table)
	}
}
