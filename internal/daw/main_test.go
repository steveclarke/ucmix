package daw

import (
	"os"
	"testing"
)

// TestMain keeps tests away from the user's own files: imports a test makes
// are logged to a temporary file, never ~/Library/Application Support.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "ucdaw-test-")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("UCDAW_IMPORTS", dir+"/imports.json")
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
