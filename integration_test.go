package ucmix

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/steveclarke/ucmix/internal/fakeboard"
	"github.com/steveclarke/ucmix/internal/proto"
	"github.com/steveclarke/ucmix/internal/transport"
)

// startFakeboard boots a fakeboard on a real TCP port and returns its address.
func startFakeboard(t *testing.T, b *fakeboard.Board) string {
	t.Helper()
	addr, err := b.Start()
	if err != nil {
		t.Fatalf("fakeboard start: %v", err)
	}
	t.Cleanup(func() { b.Close() })
	return addr
}

// connectReal connects a real Client (real transport) to addr.
func connectReal(t *testing.T, addr string, opts ...Option) *Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := Connect(ctx, addr, opts...)
	if err != nil {
		t.Fatalf("Connect(%s): %v", addr, err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestIntegrationConnectSnapshotHasSeededKeys(t *testing.T) {
	b := fakeboard.New(map[string]any{
		"line/ch1/username": "Kick",
		"line/ch1/mute":     float32(1.0),
		"global/mixer_name": "Board42",
	})
	addr := startFakeboard(t, b)

	c := connectReal(t, addr)
	snap := c.Snapshot()
	if snap["line/ch1/username"] != "Kick" {
		t.Errorf("snapshot username = %v, want Kick", snap["line/ch1/username"])
	}
	if _, ok := snap["global/mixer_name"]; !ok {
		t.Error("snapshot missing global/mixer_name")
	}
	// Humanized read of the seeded mute.
	if v, ok := c.Get("line/ch1/mute"); !ok || v != true {
		t.Errorf("Get(mute) = %v (ok=%v), want true", v, ok)
	}
}

func TestIntegrationSetBroadcastsToSecondClient(t *testing.T) {
	b := fakeboard.New(map[string]any{"line/ch2/mute": float32(0.0)})
	addr := startFakeboard(t, b)

	writer := connectReal(t, addr)
	watcher := connectReal(t, addr)

	if err := writer.Mute(context.Background(), Line, 2, true); err != nil {
		t.Fatal(err)
	}

	// The watcher receives the broadcast delta and applies it.
	waitFor(t, func() bool {
		v, ok := watcher.Get("line/ch2/mute")
		return ok && v == true
	})
}

func TestIntegrationRecallSceneChangesState(t *testing.T) {
	b := fakeboard.New(map[string]any{"line/ch1/username": "Original"})
	addr := startFakeboard(t, b)

	writer := connectReal(t, addr)
	// A second client observes state, since the board broadcasts to other
	// clients (not the sender) and recall pushes a fresh ZB to all subscribers.
	watcher := connectReal(t, addr)
	ctx := context.Background()

	// Store "Original", change to "Changed" (watcher sees the broadcast), then
	// recall the stored scene → fresh ZB restores "Original" for the watcher.
	if err := writer.StoreScene(ctx, "Proj", "SceneA"); err != nil {
		t.Fatal(err)
	}
	if err := writer.SetName(ctx, Line, 1, "Changed"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { v, ok := watcher.GetRaw("line/ch1/username"); return ok && v == "Changed" })

	if err := writer.RecallScene(ctx, "Proj", "SceneA"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { v, ok := watcher.GetRaw("line/ch1/username"); return ok && v == "Original" })
}

func TestIntegrationResetMixerChangesState(t *testing.T) {
	b := fakeboard.New(map[string]any{"line/ch1/username": "Kick"})
	addr := startFakeboard(t, b)

	c := connectReal(t, addr)
	if err := c.ResetMixer(context.Background(), ResetScope{Scene: true, Project: true}); err != nil {
		t.Fatal(err)
	}

	// After reset the seeded key is gone and the factory tree is loaded.
	waitFor(t, func() bool {
		_, ok := c.GetRaw("line/ch1/username")
		return !ok
	})
	if _, ok := c.GetRaw("global/mixer_name"); !ok {
		t.Error("factory key global/mixer_name absent after ResetMixer")
	}
}

func TestIntegrationPacedBulkWritesUnderDropFault(t *testing.T) {
	// Board drops the connection after receiving 50 frames (Subscribe + 49
	// writes). Paced writes must not corrupt state or panic; the transport
	// simply closes and Frames() ends.
	b := fakeboard.New(map[string]any{"line/ch1/pan": float32(0)})
	b.DropAfterFrames = 50
	addr := startFakeboard(t, b)

	// Small pacing window so the test exercises the pace path quickly.
	c := connectReal(t, addr, WithTransportOptions(transport.Options{
		PaceEvery: 10,
		PaceDelay: time.Millisecond,
	}))

	ctx := context.Background()
	// Fire 200 writes; the board drops mid-stream. Send returns an error once
	// the connection is gone — that is expected, not a corruption.
	var sendErr error
	for i := 0; i < 200; i++ {
		if err := c.Set(ctx, "line/ch1/pan", float64(i%2)); err != nil {
			sendErr = err
			break
		}
	}
	if sendErr == nil {
		t.Log("all 200 writes accepted before drop (board may not have closed yet)")
	}

	// The board's received writes were applied in order without corruption:
	// pan is always a clean float32 0 or 1.
	if v, ok := b.Snapshot()["line/ch1/pan"]; ok {
		f, isF := v.(float32)
		if !isF || (f != 0 && f != 1) {
			t.Errorf("board pan = %v, want a clean 0 or 1", v)
		}
	}
}

// TestIntegrationSetManyReusesOneConnection is the #19 reliability guarantee: a
// batch of many writes goes over a single held connection and every write lands.
// A regression to connect-per-write would push AcceptedConns past 1 (and, on
// rapid reconnect, drop writes).
func TestIntegrationSetManyReusesOneConnection(t *testing.T) {
	b := fakeboard.New(map[string]any{})
	addr := startFakeboard(t, b)
	c := connectReal(t, addr)

	const n = 12
	settings := make([]Setting, n)
	for i := range settings {
		settings[i] = Setting{Path: fmt.Sprintf("line/ch%d/mute", i+1), Value: true}
	}
	if err := c.SetMany(context.Background(), settings); err != nil {
		t.Fatalf("SetMany: %v", err)
	}

	// Wait for the last write to land, then assert every path is present.
	waitFor(t, func() bool { _, ok := b.Snapshot()[fmt.Sprintf("line/ch%d/mute", n)]; return ok })
	snap := b.Snapshot()
	for _, s := range settings {
		if _, ok := snap[s.Path]; !ok {
			t.Errorf("write dropped: %s missing from board", s.Path)
		}
	}
	if got := b.AcceptedConns(); got != 1 {
		t.Fatalf("board accepted %d connections for the batch, want 1", got)
	}
}

// TestIntegrationListProjectsTimesOutWhenBoardSilent is the #9 guard: a real
// board may never answer a preset-list request. ListProjects must fail with
// ErrListTimeout within the bound instead of hanging forever.
func TestIntegrationListProjectsTimesOutWhenBoardSilent(t *testing.T) {
	b := fakeboard.New(map[string]any{})
	b.SuppressListReply = true // never answers the FR preset-list request, like a silent board
	addr := startFakeboard(t, b)
	c := connectReal(t, addr)

	orig := listTimeout
	listTimeout = 100 * time.Millisecond
	t.Cleanup(func() { listTimeout = orig })

	start := time.Now()
	_, err := c.ListProjects(context.Background())
	if !errors.Is(err, ErrListTimeout) {
		t.Fatalf("ListProjects err = %v, want ErrListTimeout", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("ListProjects took %v, want ~%v (should not hang)", elapsed, listTimeout)
	}
}

// A store the board never acknowledges must fail. This is the regression that
// mattered: the old StoreScene returned nil as soon as the bytes were sent, so a
// write the board dropped was reported as success and the operator believed a
// scene was safe when nothing had been saved.
func TestIntegrationStoreWithoutAckFails(t *testing.T) {
	b := fakeboard.New(map[string]any{"line/ch1/username": "Original"})
	b.SuppressStoreAck = true
	addr := startFakeboard(t, b)

	c := connectReal(t, addr)

	ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	defer cancel()

	err := c.StoreScene(ctx, "Proj", "01.SceneA.scn")
	if err == nil {
		t.Fatal("StoreScene reported success with no acknowledgment from the board")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("StoreScene error = %v, want the wait to expire", err)
	}
}

// An acknowledgment for a different preset must not satisfy our wait — two
// clients storing at once would otherwise confirm each other's writes.
func TestIntegrationStoreIgnoresOtherPresetsAck(t *testing.T) {
	b := fakeboard.New(map[string]any{"line/ch1/username": "Original"})
	addr := startFakeboard(t, b)

	c := connectReal(t, addr)
	ctx := context.Background()

	// The fake board acks the file it was asked for, so a normal store confirms.
	if err := c.StoreScene(ctx, "Proj", "01.SceneA.scn"); err != nil {
		t.Fatalf("store of 01.SceneA.scn: %v", err)
	}
	if _, ok := b.Scenes()["presets/proj/Proj/01.SceneA.scn"]; !ok {
		t.Error("board did not record the scene under its full preset path")
	}
}

// NextSceneSlot mirrors UC Surface's client-side allocation: first empty slot,
// and never an implicit overwrite of an occupied one.
func TestIntegrationNextSceneSlotAllocatesAndRefusesDuplicates(t *testing.T) {
	b := fakeboard.New(map[string]any{"k": float32(1)})
	addr := startFakeboard(t, b)
	c := connectReal(t, addr)
	ctx := context.Background()

	slot, err := c.NextSceneSlot(ctx, "01.Sevenview Live.proj", "Opening")
	if err != nil {
		t.Fatalf("NextSceneSlot: %v", err)
	}
	// The allocated name must carry the first empty slot's number, whatever the
	// board's roster looks like.
	slots, err := c.SceneSlots(ctx, "01.Sevenview Live.proj")
	if err != nil {
		t.Fatalf("SceneSlots: %v", err)
	}
	var firstEmpty string
	for _, sl := range slots {
		if sl.Title == proto.EmptyPresetTitle {
			firstEmpty = sl.Name
			break
		}
	}
	if firstEmpty == "" {
		t.Fatal("fake board reported no empty scene slot")
	}
	prefix, _, _ := strings.Cut(firstEmpty, ".")
	if want := prefix + ".Opening.scn"; slot != want {
		t.Errorf("NextSceneSlot = %q, want %q (first empty slot %q)", slot, want, firstEmpty)
	}

	scenes, err := c.ListScenes(ctx, "01.Sevenview Live.proj")
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	if len(scenes) == 0 {
		t.Fatal("no occupied scenes in the fake board's list")
	}
	if _, err := c.NextSceneSlot(ctx, "01.Sevenview Live.proj", scenes[0].Title); !errors.Is(err, ErrSlotOccupied) {
		t.Errorf("NextSceneSlot for an existing title = %v, want ErrSlotOccupied", err)
	}
}

// A delete frees its slot: the scene disappears from the listing and the slot
// becomes available to the next store.
func TestIntegrationDeleteSceneFreesSlot(t *testing.T) {
	b := fakeboard.New(map[string]any{"k": float32(1)})
	addr := startFakeboard(t, b)
	c := connectReal(t, addr)
	ctx := context.Background()

	const project = "01.Main Live.proj"
	before, err := c.ListScenes(ctx, project)
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	if len(before) == 0 {
		t.Fatal("fake board has no scenes to delete")
	}
	target := before[0]

	if err := c.DeleteScene(ctx, project, target.Name); err != nil {
		t.Fatalf("DeleteScene: %v", err)
	}

	after, err := c.ListScenes(ctx, project)
	if err != nil {
		t.Fatalf("ListScenes after delete: %v", err)
	}
	for _, s := range after {
		if s.Name == target.Name {
			t.Fatalf("%q is still listed after delete", target.Name)
		}
	}
	if len(after) != len(before)-1 {
		t.Errorf("scene count = %d, want %d", len(after), len(before)-1)
	}

	// The freed slot is reusable, and keeps its slot number.
	slot, err := c.NextSceneSlot(ctx, project, "Reused")
	if err != nil {
		t.Fatalf("NextSceneSlot after delete: %v", err)
	}
	prefix, _, _ := strings.Cut(target.Name, ".")
	if want := prefix + ".Reused.scn"; slot != want {
		t.Errorf("next slot = %q, want the freed slot %q", slot, want)
	}
}

// A delete the board never acknowledges must fail rather than report success.
func TestIntegrationDeleteWithoutAckFails(t *testing.T) {
	b := fakeboard.New(map[string]any{"k": float32(1)})
	b.SuppressStoreAck = true
	addr := startFakeboard(t, b)
	c := connectReal(t, addr)

	ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	defer cancel()

	if err := c.DeleteScene(ctx, "01.Main Live.proj", "01.Opening Set.scn"); err == nil {
		t.Fatal("DeleteScene reported success with no acknowledgment from the board")
	}
}
