package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestResolveHostPrecedence(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "config.yml"), "host: fileboard\n")

	env := map[string]string{}
	f := File{
		ConfigDir: dir,
		Env:       func(k string) string { return env[k] },
	}

	tests := []struct {
		name     string
		flagHost string
		envHost  string
		want     string
	}{
		{"flag wins over env and file", "flagboard", "envboard", "flagboard:53000"},
		{"env wins over file", "", "envboard", "envboard:53000"},
		{"file when no flag or env", "", "", "fileboard:53000"},
		{"flag keeps explicit port", "flagboard:9000", "", "flagboard:9000"},
		{"flag is trimmed", "  flagboard  ", "", "flagboard:53000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env[EnvHost] = tt.envHost
			got, err := f.ResolveHost(tt.flagHost)
			if err != nil {
				t.Fatalf("ResolveHost: %v", err)
			}
			if got != tt.want {
				t.Errorf("ResolveHost = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveHostErrorsWhenUnset(t *testing.T) {
	dir := t.TempDir() // empty: no config files
	f := File{ConfigDir: dir, Env: func(string) string { return "" }}
	_, err := f.ResolveHost("")
	if err == nil {
		t.Fatal("want error when no host configured, got nil")
	}
}

func TestResolveHostLocalOverride(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "config.yml"), "host: baseboard\n")
	writeFile(t, filepath.Join(dir, "config.local.yml"), "host: localboard\n")
	f := File{ConfigDir: dir, Env: func(string) string { return "" }}
	got, err := f.ResolveHost("")
	if err != nil {
		t.Fatalf("ResolveHost: %v", err)
	}
	if got != "localboard:53000" {
		t.Errorf("ResolveHost = %q, want localboard:53000", got)
	}
}

func TestDeepMerge(t *testing.T) {
	tests := []struct {
		name           string
		base, override map[string]any
		want           map[string]any
	}{
		{
			name:     "override scalar replaces",
			base:     map[string]any{"host": "a", "port": 1},
			override: map[string]any{"host": "b"},
			want:     map[string]any{"host": "b", "port": 1},
		},
		{
			name:     "nested maps merge",
			base:     map[string]any{"net": map[string]any{"host": "a", "pace": 10}},
			override: map[string]any{"net": map[string]any{"host": "b"}},
			want:     map[string]any{"net": map[string]any{"host": "b", "pace": 10}},
		},
		{
			name:     "override adds new key",
			base:     map[string]any{"a": 1},
			override: map[string]any{"b": 2},
			want:     map[string]any{"a": 1, "b": 2},
		},
		{
			name:     "empty override keeps base",
			base:     map[string]any{"a": 1},
			override: map[string]any{},
			want:     map[string]any{"a": 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deepMerge(tt.base, tt.override)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("deepMerge = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestWithPort(t *testing.T) {
	tests := []struct{ in, want string }{
		{"board", "board:53000"},
		{"board:9000", "board:9000"},
		{"127.0.0.1", "127.0.0.1:53000"},
		{"127.0.0.1:1234", "127.0.0.1:1234"},
		{"[::1]:1234", "[::1]:1234"},
	}
	for _, tt := range tests {
		if got := withPort(tt.in); got != tt.want {
			t.Errorf("withPort(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
