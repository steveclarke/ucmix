package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriterAddAndUseCreatesFile(t *testing.T) {
	dir := t.TempDir()
	f := File{ConfigDir: dir, Env: func(string) string { return "" }}
	w := f.NewWriter()

	if err := w.AddProfile("foh", "192.168.1.50"); err != nil {
		t.Fatalf("AddProfile: %v", err)
	}
	if err := w.SetCurrent("foh"); err != nil {
		t.Fatalf("SetCurrent: %v", err)
	}

	got, err := f.Resolve("", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "192.168.1.50:53000" {
		t.Errorf("Resolve = %q, want 192.168.1.50:53000", got)
	}
}

func TestWriterPreservesCommentsAndUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	writeFile(t, path, "# my mixers\nversion: 1\nhost: legacyboard  # old single-host\n")

	w := File{ConfigDir: dir}.NewWriter()
	if err := w.AddProfile("foh", "192.168.1.50"); err != nil {
		t.Fatalf("AddProfile: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	for _, want := range []string{"# my mixers", "# old single-host", "version: 1", "legacyboard", "foh", "192.168.1.50"} {
		if !strings.Contains(out, want) {
			t.Errorf("rewritten config missing %q:\n%s", want, out)
		}
	}
}

func TestWriterRenameAndRemove(t *testing.T) {
	dir := t.TempDir()
	f := File{ConfigDir: dir, Env: func(string) string { return "" }}
	w := f.NewWriter()

	mustNoErr(t, w.AddProfile("foh", "a"))
	mustNoErr(t, w.AddProfile("monitor", "b"))
	mustNoErr(t, w.SetCurrent("foh"))

	// Rename current profile: the current pointer follows it.
	mustNoErr(t, w.RenameProfile("foh", "main"))
	cfg, err := f.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Profiles["main"]; !ok {
		t.Error("renamed profile 'main' missing")
	}
	if _, ok := cfg.Profiles["foh"]; ok {
		t.Error("old profile 'foh' still present")
	}
	if cfg.Current != "main" {
		t.Errorf("current = %q, want main", cfg.Current)
	}

	// Remove current profile: the current pointer is cleared.
	mustNoErr(t, w.RemoveProfile("main"))
	cfg, err = f.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Profiles["main"]; ok {
		t.Error("removed profile 'main' still present")
	}
	if cfg.Current != "" {
		t.Errorf("current = %q, want empty after removing current profile", cfg.Current)
	}
}

func TestWriterRemoveUnknownErrors(t *testing.T) {
	dir := t.TempDir()
	w := File{ConfigDir: dir}.NewWriter()
	if err := w.RemoveProfile("nope"); err == nil {
		t.Fatal("want error removing unknown profile, got nil")
	}
}

func TestWriterRenameToExistingErrors(t *testing.T) {
	dir := t.TempDir()
	w := File{ConfigDir: dir}.NewWriter()
	mustNoErr(t, w.AddProfile("foh", "a"))
	mustNoErr(t, w.AddProfile("monitor", "b"))
	if err := w.RenameProfile("foh", "monitor"); err == nil {
		t.Fatal("want error renaming onto an existing profile, got nil")
	}
}

func mustNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
