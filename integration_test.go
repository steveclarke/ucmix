package ucmix

import (
	"context"
	"testing"
	"time"

	"github.com/steveclarke/ucmix/internal/fakeboard"
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
