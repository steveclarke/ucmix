package ucmix

import "testing"

// Placeholder so the test pipeline has something to run until Phase 1 lands.
func TestVersionSet(t *testing.T) {
	if Version == "" {
		t.Fatal("Version must not be empty")
	}
}
