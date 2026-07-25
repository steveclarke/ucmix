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
//
// project and scene must be the board's slot names, not display titles — they
// carry a slot prefix and an extension ("03.135 Main Live.proj",
// "04.Opening.scn"). A path built from bare titles is not a path the board
// honors. ResolveProject and NextSceneSlot turn titles into slot names.
func buildPresetFile(project, scene string) string {
	if project == "" {
		return "presets/proj/" + scene
	}
	return "presets/proj/" + project + "/" + scene
}

// presetAckTimeout bounds how long a preset command waits for the board's
// acknowledgment. A real 32R answers a store in a few seconds. Package-level so
// tests can shorten it; never reassigned outside tests.
var presetAckTimeout = 10 * time.Second

// ErrStoreTimeout is returned by StoreScene when the board never acknowledges
// the write. The store must be treated as NOT having happened: the board sends
// StoredPreset only once the scene is on disk.
var ErrStoreTimeout = errors.New("ucmix: timed out waiting for the board to confirm the store")

// ErrRecallTimeout is returned by RecallScene when the board never acknowledges
// the recall.
var ErrRecallTimeout = errors.New("ucmix: timed out waiting for the board to confirm the recall")

// ErrDeleteTimeout is returned by DeleteScene when the board never acknowledges
// the delete. The scene may or may not be gone; re-list to find out.
var ErrDeleteTimeout = errors.New("ucmix: timed out waiting for the board to confirm the delete")

// ErrSlotOccupied is returned when a store target names a slot that already
// holds a scene. Overwriting is destructive and never implicit.
var ErrSlotOccupied = errors.New("ucmix: that scene slot is already in use")

// ErrNoFreeSlot is returned when a project has no empty scene slot left.
var ErrNoFreeSlot = errors.New("ucmix: the project has no free scene slot")

// RecallScene recalls a stored scene and waits for the board to confirm it (JM
// RestorePreset out, JM RecalledPreset back). The board also pushes a fresh ZB
// snapshot, which the merge loop loads.
//
// project and scene are slot names (see buildPresetFile). Returns
// ErrRecallTimeout if no acknowledgment arrives; as with a store, the request
// alone is fire-and-forget and a caller that hangs up first loses it.
func (c *Client) RecallScene(ctx context.Context, project, scene string) error {
	file := buildPresetFile(project, scene)
	return c.presetCommand(ctx, file,
		proto.RestorePresetCmd{PresetFile: file},
		proto.JMRecalledPreset, ErrRecallTimeout)
}

// StoreScene stores the current mixer state as a scene and waits for the board
// to confirm it (JM StorePreset out, JM StoredPreset back).
//
// project and scene are slot names (see buildPresetFile). The wait is what makes
// the store real: the request alone is fire-and-forget, and a caller that closes
// the connection before the board finishes the write loses it. Returns
// ErrStoreTimeout if no acknowledgment arrives — never report success without
// one.
func (c *Client) StoreScene(ctx context.Context, project, scene string) error {
	file := buildPresetFile(project, scene)
	return c.presetCommand(ctx, file,
		proto.StorePresetCmd{PresetFile: file},
		proto.JMStoredPreset, ErrStoreTimeout)
}

// presetCommand sends a JM preset command and blocks until the board confirms
// it. Store and recall take this path; delete sends an FR instead and calls
// awaitPresetAck directly.
func (c *Client) presetCommand(ctx context.Context, presetFile string, cmd any, ackID string, timeoutErr error) error {
	return c.awaitPresetAck(ctx, presetFile, ackID, timeoutErr, func(ctx context.Context) error {
		return c.sendJM(ctx, cmd)
	})
}

// awaitPresetAck runs send and blocks until the board answers with ackID for
// this exact presetFile. Matching on the file matters: the board announces every
// client's preset operations on every connection, so an unmatched ack would
// confirm somebody else's work.
//
// send runs after the subscription is registered, so an acknowledgment cannot
// arrive before anyone is listening for it.
//
// When the caller supplies no deadline, the wait is bounded and an expiry
// surfaces as timeoutErr; a caller-driven cancel passes through unchanged.
func (c *Client) awaitPresetAck(ctx context.Context, presetFile, ackID string, timeoutErr error, send func(context.Context) error) error {
	acks, unsubscribe := c.subscribeJM(8)
	defer unsubscribe()

	bounded := ctx
	timed := false
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		bounded, cancel = context.WithTimeout(ctx, presetAckTimeout)
		defer cancel()
		timed = true
	}

	if err := send(bounded); err != nil {
		return err
	}

	for {
		select {
		case m := <-acks:
			if m.ID != ackID {
				continue
			}
			ack, err := proto.UnmarshalPresetAck(m.Body)
			if err != nil {
				continue
			}
			if ack.PresetFile == presetFile {
				return nil
			}
		case <-bounded.Done():
			if timed && ctx.Err() == nil {
				return timeoutErr
			}
			return bounded.Err()
		}
	}
}

// RenameScene renames a stored scene's display title (FR with the Rena verb).
// project and scene are slot names; title is the new display title. The board
// sends no confirmation for a rename, so this re-lists the project and reports
// an error if the new title is not present afterwards.
//
// The slot name keeps its original file name; only the title changes.
func (c *Client) RenameScene(ctx context.Context, project, scene, title string) error {
	resource := proto.VerbRena + buildPresetFile(project, scene)

	c.mu.Lock()
	c.nextReqID++
	id := c.nextReqID
	c.mu.Unlock()

	req := proto.Frame{Code: proto.CodeFR, Payload: proto.MarshalFR(id, resource, title)}
	if err := c.t.Send(ctx, req); err != nil {
		return err
	}

	scenes, err := c.ListScenes(ctx, project)
	if err != nil {
		return fmt.Errorf("ucmix: renamed, but could not confirm it: %w", err)
	}
	for _, s := range scenes {
		if s.Title == title {
			return nil
		}
	}
	return fmt.Errorf("ucmix: the board did not apply the rename to %q", title)
}

// DeleteScene deletes a stored scene and waits for the board to confirm it (FR
// with the Dele verb out, JM DeletedPreset back). The slot becomes empty and is
// reusable by the next store.
//
// project and scene are slot names. This is destructive and irreversible on the
// board; the CLI confirms before calling it.
func (c *Client) DeleteScene(ctx context.Context, project, scene string) error {
	file := buildPresetFile(project, scene)

	return c.awaitPresetAck(ctx, file, proto.JMDeletedPreset, ErrDeleteTimeout,
		func(ctx context.Context) error {
			c.mu.Lock()
			c.nextReqID++
			id := c.nextReqID
			c.mu.Unlock()
			return c.t.Send(ctx, proto.Frame{
				Code:    proto.CodeFR,
				Payload: proto.MarshalFR(id, proto.VerbDele+file, ""),
			})
		})
}

// ResolveProject turns a project title or slot name into the board's slot name.
// An exact slot-name match wins; otherwise the display title is matched. This is
// what lets a caller say "135 Main Live" instead of "03.135 Main Live.proj".
func (c *Client) ResolveProject(ctx context.Context, project string) (Project, error) {
	projects, err := c.ListProjects(ctx)
	if err != nil {
		return Project{}, err
	}
	for _, p := range projects {
		if p.Name == project {
			return p, nil
		}
	}
	for _, p := range projects {
		if p.Title == project {
			return p, nil
		}
	}
	return Project{}, fmt.Errorf("ucmix: no project named %q on the board", project)
}

// ResolveScene turns a scene title or slot name into the board's scene entry.
// An exact slot-name match wins; otherwise the display title is matched.
func (c *Client) ResolveScene(ctx context.Context, project, scene string) (Scene, error) {
	scenes, err := c.ListScenes(ctx, project)
	if err != nil {
		return Scene{}, err
	}
	for _, s := range scenes {
		if s.Name == scene {
			return s, nil
		}
	}
	for _, s := range scenes {
		if s.Title == scene {
			return s, nil
		}
	}
	return Scene{}, fmt.Errorf("ucmix: no scene named %q in %s", scene, project)
}

// SceneSlots returns every scene slot in a project, occupied and empty alike, in
// board order. ListScenes hides empty slots; slot allocation needs to see them.
// The leading project-config entry is dropped.
func (c *Client) SceneSlots(ctx context.Context, project string) ([]proto.PresetFile, error) {
	files, err := c.listPresets(ctx, proto.VerbList+"presets/proj/"+project)
	if err != nil {
		return nil, err
	}
	slots := make([]proto.PresetFile, 0, len(files))
	for _, f := range files {
		if !strings.HasSuffix(f.Name, ".scn") {
			continue // the .cnfg project-config entry, not a scene slot
		}
		slots = append(slots, f)
	}
	return slots, nil
}

// NextSceneSlot builds the slot name a new scene called title should be stored
// as: the first empty slot's number, then the title, then ".scn" — the same
// allocation UC Surface performs client-side.
//
// It refuses when the project already holds a scene with that title, so a store
// cannot silently overwrite one.
func (c *Client) NextSceneSlot(ctx context.Context, project, title string) (string, error) {
	slots, err := c.SceneSlots(ctx, project)
	if err != nil {
		return "", err
	}
	for _, s := range slots {
		if s.Title == title {
			return "", fmt.Errorf("%w: %q already exists in this project", ErrSlotOccupied, title)
		}
	}
	for _, s := range slots {
		if s.Title != proto.EmptyPresetTitle {
			continue
		}
		// Slot names are "NN.<title>.scn"; reuse the empty slot's NN prefix.
		prefix, _, ok := strings.Cut(s.Name, ".")
		if !ok {
			continue
		}
		return prefix + "." + title + ".scn", nil
	}
	return "", ErrNoFreeSlot
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
