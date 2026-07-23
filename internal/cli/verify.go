package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/steveclarke/ucmix/internal/boardconfig"
	"github.com/steveclarke/ucmix/internal/errs"
	"github.com/steveclarke/ucmix/internal/ui"
)

// newVerifyCmd builds `verify <config.yml>`: load and compile the declarative
// config, connect fresh, snapshot, and diff. It is read-only. Exit codes: 0 when
// every declared setting matches, 1 when any drift is found, 2 on a load,
// compile, or connect error.
func newVerifyCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "verify <config.yml>",
		Short: "Check the board against a config (read-only; exit 1 on drift)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			desired, err := loadAndCompile(args[0])
			if err != nil {
				return &exitError{code: 2, err: err}
			}

			c, err := g.dialClient(cmd.Context())
			if err != nil {
				return &exitError{code: 2, err: err}
			}
			defer func() { _ = c.Close() }()

			drift := boardconfig.Diff(desired, c.Snapshot())
			return reportDiff(g, "verify", len(desired), drift)
		},
	}
}

// loadAndCompile reads a config file, loads/validates it, and compiles it to the
// ordered desired set. Errors are wrapped as CLIErrors with actionable hints.
func loadAndCompile(path string) ([]boardconfig.Desired, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errs.CLIError{
			Message: fmt.Sprintf("could not read config %s: %v", path, err),
			Hint:    "check the path to the board config file",
		}
	}
	cfg, err := boardconfig.Load(data)
	if err != nil {
		return nil, errs.CLIError{Message: err.Error(), Hint: "fix the config and try again"}
	}
	desired, err := boardconfig.Compile(cfg)
	if err != nil {
		return nil, errs.CLIError{Message: err.Error(), Hint: "fix the config and try again"}
	}
	return desired, nil
}

// reportDiff prints a drift report (or a clean line) and returns the exit code:
// nil (0) when clean, exitError(1) when any path drifts. action names the caller
// ("verify" or "apply") for the JSON envelope and messages; checked is the count
// of declared settings.
func reportDiff(g *globals, action string, checked int, drift []boardconfig.Mismatch) error {
	if g.json {
		if err := printJSON(map[string]any{
			"action":  action,
			"checked": checked,
			"clean":   len(drift) == 0,
			"drift":   driftJSON(drift),
		}); err != nil {
			return err
		}
	} else if len(drift) == 0 {
		fmt.Println(ui.Success(fmt.Sprintf("%s clean: %d settings match", action, checked)))
	} else {
		fmt.Println(ui.Header(fmt.Sprintf("drift: %d of %d settings differ", len(drift), checked)))
		fmt.Println(renderDrift(drift))
	}
	if len(drift) > 0 {
		return &exitError{code: 1}
	}
	return nil
}

// driftJSON turns mismatches into JSON-friendly rows with hex-normalized values.
func driftJSON(drift []boardconfig.Mismatch) []map[string]any {
	rows := make([]map[string]any, len(drift))
	for i, m := range drift {
		rows[i] = map[string]any{"path": m.Path, "want": jsonValue(m.Want), "got": jsonValue(m.Got)}
	}
	return rows
}

// renderDrift formats a path/want/got table, one row per mismatch. Values are
// humanized by Diff; a nil Got (path absent) renders as "(absent)".
func renderDrift(drift []boardconfig.Mismatch) string {
	rows := make([][2]string, len(drift))
	for i, m := range drift {
		rows[i] = [2]string{m.Path, fmt.Sprintf("want %s  got %s", displayValue(m.Want), gotOrAbsent(m.Got))}
	}
	return ui.Table(rows)
}

func gotOrAbsent(v any) string {
	if v == nil {
		return "(absent)"
	}
	return displayValue(v)
}

// verifyOnFreshConn opens a new connection, snapshots, and diffs — the required
// fresh-connection verify used by apply after it writes and closes connection A.
func verifyOnFreshConn(ctx context.Context, g *globals, desired []boardconfig.Desired) ([]boardconfig.Mismatch, error) {
	c, err := g.dialClient(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = c.Close() }()
	return boardconfig.Diff(desired, c.Snapshot()), nil
}
