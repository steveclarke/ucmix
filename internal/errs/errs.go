// Package errs holds the CLI's user-facing error type. A CLIError carries a
// plain Message and an optional Hint; the root command's Execute renders both
// (the Hint dimmed) and exits non-zero. Library and internal errors that reach
// Execute without being a CLIError are printed as-is.
package errs

// CLIError is a user-facing command failure: Message is the one-line problem,
// Hint is an optional dimmed suggestion for how to fix it. Commands return a
// CLIError (optionally wrapped with %w) when they want a clean, actionable
// message instead of a raw Go error.
type CLIError struct {
	Message string
	Hint    string
}

// Error implements error. It returns only the Message; the Hint is rendered
// separately by Execute so it can be styled.
func (e CLIError) Error() string {
	return e.Message
}
