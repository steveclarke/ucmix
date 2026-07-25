package cli

import (
	"errors"
	"testing"

	"github.com/spf13/cobra"
	"github.com/steveclarke/ucmix/internal/errs"
)

func TestRequireArgs(t *testing.T) {
	tests := []struct {
		name      string
		validator cobra.PositionalArgs
		hint      string
		args      []string
		wantErr   bool
		wantHint  string
	}{
		{
			name:      "valid count passes through",
			validator: cobra.ExactArgs(1),
			hint:      "run `ucmix ls projects` to see them",
			args:      []string{"proj"},
		},
		{
			name:      "missing arg names what is wanted",
			validator: cobra.ExactArgs(1),
			hint:      "run `ucmix ls projects` to see them",
			args:      nil,
			wantErr:   true,
			wantHint:  "run `ucmix ls projects` to see them",
		},
		{
			name:      "too many args also gets the message",
			validator: cobra.ExactArgs(1),
			hint:      "run `ucmix ls projects` to see them",
			args:      []string{"a", "b"},
			wantErr:   true,
			wantHint:  "run `ucmix ls projects` to see them",
		},
		{
			name:      "empty hint falls back to the usage line",
			validator: cobra.ExactArgs(1),
			args:      nil,
			wantErr:   true,
			wantHint:  "usage: demo <thing>",
		},
	}

	const message = "demo needs a thing"
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: "demo <thing>"}
			cmd.Flags().Bool("loud", false, "")
			cmd.Args = requireArgs(tt.validator, message, tt.hint)

			err := cmd.Args(cmd, tt.args)
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("Args() = %v, want nil", err)
				}
				return
			}

			var ce errs.CLIError
			if !errors.As(err, &ce) {
				t.Fatalf("Args() = %v, want an errs.CLIError", err)
			}
			if ce.Message != message {
				t.Errorf("Message = %q, want %q", ce.Message, message)
			}
			if ce.Hint != tt.wantHint {
				t.Errorf("Hint = %q, want %q", ce.Hint, tt.wantHint)
			}
		})
	}
}
