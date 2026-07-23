package ucmix

import (
	"context"
	"fmt"
	"sort"

	"github.com/steveclarke/ucmix/internal/proto"
)

// buildPresetFile joins a project and scene into the preset-file path the board
// keys scenes by. RecallScene and StoreScene must agree on this format so a
// stored scene can be recalled by the same (project, scene) pair.
func buildPresetFile(project, scene string) string {
	if project == "" {
		return "presets/proj/" + scene
	}
	return "presets/proj/" + project + "/" + scene
}

// RecallScene recalls a stored scene (JM RestorePreset). The board responds by
// pushing a fresh ZB snapshot, which the merge loop loads.
func (c *Client) RecallScene(ctx context.Context, project, scene string) error {
	return c.sendJM(ctx, proto.RestorePresetCmd{PresetFile: buildPresetFile(project, scene)})
}

// StoreScene stores the current mixer state as a scene (JM StorePreset).
func (c *Client) StoreScene(ctx context.Context, project, scene string) error {
	return c.sendJM(ctx, proto.StorePresetCmd{PresetFile: buildPresetFile(project, scene)})
}

// ResetScope selects what a ResetMixer clears. Scene clears scene-level
// settings; Project clears project-level settings. Either or both may be set.
type ResetScope struct {
	Scene   bool
	Project bool
}

// ResetMixer resets the mixer to factory defaults for the given scope (JM
// ResetMixer). The board responds with a fresh ZB snapshot.
func (c *Client) ResetMixer(ctx context.Context, scope ResetScope) error {
	return c.sendJM(ctx, proto.ResetMixerCmd{
		ResetScene:   boolToInt(scope.Scene),
		ResetProject: boolToInt(scope.Project),
	})
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Project names one preset/project returned by ListProjects.
type Project struct {
	Name string
}

// ListProjects requests the board's preset list (JM Listpresets) and returns
// the projects it names. The JM reply is routed out of the background merge loop
// through a buffered waiter, so this blocks until the reply arrives or ctx is
// done.
func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	reply := make(chan []string, 1)
	c.mu.Lock()
	c.listWaiters = append(c.listWaiters, reply)
	c.mu.Unlock()

	if err := c.sendJM(ctx, proto.ListPresetsCmd{URL: "presets/proj"}); err != nil {
		c.dropWaiter(reply)
		return nil, err
	}

	select {
	case names := <-reply:
		projects := make([]Project, len(names))
		for i, n := range names {
			projects[i] = Project{Name: n}
		}
		return projects, nil
	case <-ctx.Done():
		c.dropWaiter(reply)
		return nil, ctx.Err()
	}
}

// dropWaiter removes a waiter that is no longer being read (send failed or ctx
// expired) so handleJMReply never blocks on it.
func (c *Client) dropWaiter(reply chan []string) {
	c.mu.Lock()
	for i, w := range c.listWaiters {
		if w == reply {
			c.listWaiters = append(c.listWaiters[:i], c.listWaiters[i+1:]...)
			break
		}
	}
	c.mu.Unlock()
}

// Mismatch is one difference found by VerifyAgainst: the path, the desired
// value, and the value actually on the board (nil if the path is absent).
type Mismatch struct {
	Path string
	Want any
	Got  any
}

// VerifyAgainst compares desired raw wire values against the client's current
// Snapshot and returns every path that differs. Float values compare within a
// small tolerance (wire floats quantize); other kinds compare by value.
//
// This must run on a FRESH Client. The in-session read-back quirk means a value
// written earlier in this session may read back as NaN until a fresh ZB; the CLI
// opens a new connection to verify. Verifying on the same Client that performed
// the writes is unreliable by design.
func (c *Client) VerifyAgainst(desired map[string]any) []Mismatch {
	snap := c.Snapshot()

	paths := make([]string, 0, len(desired))
	for p := range desired {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var out []Mismatch
	for _, p := range paths {
		want := desired[p]
		got, ok := snap[p]
		if !ok {
			out = append(out, Mismatch{Path: p, Want: want, Got: nil})
			continue
		}
		if !valuesEqual(want, got) {
			out = append(out, Mismatch{Path: p, Want: want, Got: got})
		}
	}
	return out
}

// valuesEqual compares two raw values: floats within tolerance, everything else
// by string form (covers strings, []byte, and numeric mixes without panicking).
func valuesEqual(a, b any) bool {
	if fa, oka := toFloat64(a); oka {
		if fb, okb := toFloat64(b); okb {
			d := fa - fb
			if d < 0 {
				d = -d
			}
			return d <= 1e-3
		}
		return false
	}
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}
