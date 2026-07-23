package errs

import (
	"errors"
	"fmt"
	"testing"
)

func TestCLIErrorMessage(t *testing.T) {
	e := CLIError{Message: "boom", Hint: "try again"}
	if e.Error() != "boom" {
		t.Errorf("Error() = %q, want %q", e.Error(), "boom")
	}
}

func TestCLIErrorUnwrapsFromWrapped(t *testing.T) {
	wrapped := fmt.Errorf("context: %w", CLIError{Message: "boom", Hint: "hint"})
	var ce CLIError
	if !errors.As(wrapped, &ce) {
		t.Fatal("errors.As did not find CLIError in a wrapped chain")
	}
	if ce.Hint != "hint" {
		t.Errorf("Hint = %q, want %q", ce.Hint, "hint")
	}
}
