package ucmix

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/steveclarke/ucmix/internal/proto"
)

// listTimeout bounds how long a preset-list call waits for the board's reply
// when the caller's context carries no deadline. A genuinely silent board (e.g.
// a firmware that never answers) would otherwise hang the wait forever.
// Package-level so tests can shorten it. Never reassigned outside tests.
var listTimeout = 5 * time.Second

// ErrListTimeout is returned by ListProjects and ListScenes when the board does
// not answer the preset-list request within the bound. It is distinct from a
// caller cancel so the CLI can surface the gap.
var ErrListTimeout = errors.New("ucmix: timed out waiting for the board's preset list")

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

// Project names one project returned by ListProjects. Name is the board's
// preset-file name (e.g. "01.Sevenview Live.proj"), the identifier ListScenes
// takes; Title is the display name shown on the board.
type Project struct {
	Name  string
	Title string
}

// Scene names one scene returned by ListScenes. Name is the board's preset-file
// name (e.g. "01.SV Live in Studio.scn"); Title is the display name.
type Scene struct {
	Name  string
	Title string
}

// ListProjects requests the board's project list (FR Listpresets/proj) and
// returns the occupied projects. The board reports a fixed roster of 100 slots;
// only occupied ones are folders (dir:true), so empty slots are dropped.
func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	files, err := c.listPresets(ctx, "Listpresets/proj")
	if err != nil {
		return nil, err
	}
	var projects []Project
	for _, f := range files {
		if !f.Dir {
			continue // empty slot or non-folder entry
		}
		projects = append(projects, Project{Name: f.Name, Title: f.Title})
	}
	return projects, nil
}

// ListScenes requests the scene list for a project (FR Listpresets/proj/<name>)
// and returns the occupied scenes. project is a project Name from ListProjects
// (e.g. "01.Sevenview Live.proj"). The reply leads with the project's config
// file and pads to a fixed roster of slots; the config entry and empty slots are
// dropped, leaving the real scenes.
func (c *Client) ListScenes(ctx context.Context, project string) ([]Scene, error) {
	files, err := c.listPresets(ctx, "Listpresets/proj/"+project)
	if err != nil {
		return nil, err
	}
	var scenes []Scene
	for _, f := range files {
		if !strings.HasSuffix(f.Name, ".scn") {
			continue // the leading .cnfg project-config entry, not a scene
		}
		if f.Title == proto.EmptyPresetTitle {
			continue // unused slot
		}
		scenes = append(scenes, Scene{Name: f.Name, Title: f.Title})
	}
	return scenes, nil
}

// listPresets sends an FR request for resource and blocks until the board's FD
// reply reassembles or the wait is done. The reply is routed out of the
// background merge loop through a buffered waiter.
func (c *Client) listPresets(ctx context.Context, resource string) ([]proto.PresetFile, error) {
	// Bound the wait when the caller gives no deadline of its own: a silent
	// board would otherwise hang an unbounded receive.
	bounded := ctx
	timed := false
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		bounded, cancel = context.WithTimeout(ctx, listTimeout)
		defer cancel()
		timed = true
	}

	reply := make(chan []proto.PresetFile, 1)
	c.mu.Lock()
	c.nextReqID++
	id := c.nextReqID
	c.listWaiters = append(c.listWaiters, reply)
	c.mu.Unlock()

	req := proto.Frame{Code: proto.CodeFR, Payload: proto.MarshalFR(id, resource, "")}
	if err := c.t.Send(bounded, req); err != nil {
		c.dropWaiter(reply)
		return nil, err
	}

	select {
	case files := <-reply:
		return files, nil
	case <-bounded.Done():
		c.dropWaiter(reply)
		// A timeout we imposed (caller had no deadline) surfaces as
		// ErrListTimeout; a caller-driven cancel/deadline passes through.
		if timed && ctx.Err() == nil {
			return nil, ErrListTimeout
		}
		return nil, bounded.Err()
	}
}

// dropWaiter removes a waiter that is no longer being read (send failed or ctx
// expired) so deliverList never blocks on it.
func (c *Client) dropWaiter(reply chan []proto.PresetFile) {
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
