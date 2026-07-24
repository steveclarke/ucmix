package fakeboard

import (
	"bufio"
	"net"
	"runtime"
	"testing"
	"time"

	"github.com/steveclarke/ucmix/internal/proto"
)

// testClient is a raw proto-level client over a real TCP connection.
type testClient struct {
	nc net.Conn
	r  *bufio.Reader
}

func dial(t *testing.T, addr string) *testClient {
	t.Helper()
	nc, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { nc.Close() })
	return &testClient{nc: nc, r: bufio.NewReader(nc)}
}

func (c *testClient) send(t *testing.T, f proto.Frame) {
	t.Helper()
	if _, err := c.nc.Write(proto.Encode(f)); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func (c *testClient) readFrame(t *testing.T, timeout time.Duration) (proto.Frame, error) {
	t.Helper()
	c.nc.SetReadDeadline(time.Now().Add(timeout))
	f, err := proto.ReadFrame(c.r)
	c.nc.SetReadDeadline(time.Time{})
	return f, err
}

// subscribe sends the Subscribe handshake and returns the ZB frame the board
// replies with.
func (c *testClient) subscribe(t *testing.T) proto.Frame {
	t.Helper()
	c.send(t, proto.Frame{Code: proto.CodeJM, Payload: proto.MarshalJM(proto.DefaultSubscribeCmd())})
	f, err := c.readFrame(t, 2*time.Second)
	if err != nil {
		t.Fatalf("reading ZB after subscribe: %v", err)
	}
	if f.Code != proto.CodeZB {
		t.Fatalf("handshake reply code = %q, want ZB", f.Code)
	}
	return f
}

func startBoard(t *testing.T, b *Board) string {
	t.Helper()
	addr, err := b.Start()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { b.Close() })
	return addr
}

func TestHandshakeSendsSeededZB(t *testing.T) {
	seed := map[string]any{
		"line/ch1/name":     "Kick",
		"line/ch1/mute":     float32(1.0),
		"global/mixer_name": "Board42",
	}
	b := New(seed)
	addr := startBoard(t, b)

	c := dial(t, addr)
	zb := c.subscribe(t)

	flat, err := proto.ParseZB(zb.Payload)
	if err != nil {
		t.Fatalf("ParseZB: %v", err)
	}
	if flat["line/ch1/name"] != "Kick" {
		t.Errorf("line/ch1/name = %v, want Kick", flat["line/ch1/name"])
	}
	if _, ok := flat["global/mixer_name"]; !ok {
		t.Errorf("ZB missing seeded key global/mixer_name")
	}
}

func TestBroadcastDeltaToOtherClient(t *testing.T) {
	b := New(map[string]any{"line/ch2/volume": float32(0.5)})
	addr := startBoard(t, b)

	a := dial(t, addr)
	a.subscribe(t)
	bc := dial(t, addr)
	bc.subscribe(t)

	// A writes a PV; B (already subscribed) should receive the broadcast.
	a.send(t, proto.Frame{Code: proto.CodePV, Payload: proto.MarshalPV("line/ch2/volume", 0.9)})

	f, err := bc.readFrame(t, 2*time.Second)
	if err != nil {
		t.Fatalf("B did not receive broadcast: %v", err)
	}
	if f.Code != proto.CodePV {
		t.Fatalf("broadcast code = %q, want PV", f.Code)
	}
	key, val, err := proto.UnmarshalPV(f.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if key != "line/ch2/volume" || val != 0.9 {
		t.Errorf("broadcast delta = %s=%v, want line/ch2/volume=0.9", key, val)
	}

	// A must NOT receive its own delta echoed back.
	if _, err := a.readFrame(t, 300*time.Millisecond); err == nil {
		t.Error("sender A received its own delta; broadcast should skip the sender")
	}

	// The board's tree reflects the write.
	if v, ok := b.Snapshot()["line/ch2/volume"]; !ok || v != float32(0.9) {
		t.Errorf("tree line/ch2/volume = %v (ok=%v), want 0.9", v, ok)
	}
}

func TestResetMixerReplacesState(t *testing.T) {
	b := New(map[string]any{"line/ch1/name": "Kick"})
	addr := startBoard(t, b)

	c := dial(t, addr)
	c.subscribe(t)

	c.send(t, proto.Frame{
		Code:    proto.CodeJM,
		Payload: proto.MarshalJM(proto.ResetMixerCmd{ResetScene: 1, ResetProject: 1}),
	})

	// Board pushes a fresh ZB after the reset.
	zb, err := c.readFrame(t, 2*time.Second)
	if err != nil {
		t.Fatalf("no ZB after reset: %v", err)
	}
	flat, err := proto.ParseZB(zb.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := flat["line/ch1/name"]; ok {
		t.Error("seeded key line/ch1/name survived ResetMixer")
	}
	if _, ok := flat["global/mixer_name"]; !ok {
		t.Error("factory key global/mixer_name absent after ResetMixer")
	}
}

func TestStorePresetThenRestore(t *testing.T) {
	b := New(map[string]any{"line/ch1/name": "Original"})
	addr := startBoard(t, b)

	c := dial(t, addr)
	c.subscribe(t)

	// Store current state under a name.
	c.send(t, proto.Frame{Code: proto.CodeJM, Payload: proto.MarshalJM(proto.StorePresetCmd{PresetFile: "scene-a"})})

	// Change the tree.
	c.send(t, proto.Frame{Code: proto.CodePS, Payload: proto.MarshalPS("line/ch1/name", "Changed")})
	// Give the board a moment to apply (single conn, no echo to read).
	waitFor(t, func() bool { return b.Snapshot()["line/ch1/name"] == "Changed" })

	// Restore the stored scene → pushes a fresh ZB.
	c.send(t, proto.Frame{Code: proto.CodeJM, Payload: proto.MarshalJM(proto.RestorePresetCmd{PresetFile: "scene-a"})})
	zb, err := c.readFrame(t, 2*time.Second)
	if err != nil {
		t.Fatalf("no ZB after restore: %v", err)
	}
	flat, err := proto.ParseZB(zb.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if flat["line/ch1/name"] != "Original" {
		t.Errorf("after restore line/ch1/name = %v, want Original", flat["line/ch1/name"])
	}
}

func TestListProjectsFR(t *testing.T) {
	b := New(map[string]any{"k": float32(1)})
	addr := startBoard(t, b)
	c := dial(t, addr)
	c.subscribe(t)

	// FR Listpresets/proj is answered with FD chunks; reassemble and confirm the
	// canned projects come back.
	c.send(t, proto.Frame{Code: proto.CodeFR, Payload: proto.MarshalFR(1, "Listpresets/proj", "")})
	body := readFDBody(t, c)
	files, err := proto.ParsePresetList(body)
	if err != nil {
		t.Fatalf("parsing FD reply: %v", err)
	}
	if len(files) != 3 || files[0].Title != "Main Live" || !files[0].Dir {
		t.Fatalf("projects reply = %+v", files)
	}
}

func TestListScenesFR(t *testing.T) {
	b := New(map[string]any{"k": float32(1)})
	addr := startBoard(t, b)
	c := dial(t, addr)
	c.subscribe(t)

	c.send(t, proto.Frame{Code: proto.CodeFR, Payload: proto.MarshalFR(1, "Listpresets/proj/01.Main Live.proj", "")})
	body := readFDBody(t, c)
	files, err := proto.ParsePresetList(body)
	if err != nil {
		t.Fatalf("parsing FD reply: %v", err)
	}
	// Leading .cnfg entry + two scenes + one empty slot.
	if len(files) != 4 || files[1].Title != "Opening Set" {
		t.Fatalf("scenes reply = %+v", files)
	}
}

func TestListRequestSuppressed(t *testing.T) {
	b := New(map[string]any{"k": float32(1)})
	b.SuppressListReply = true
	addr := startBoard(t, b)
	c := dial(t, addr)
	c.subscribe(t)

	c.send(t, proto.Frame{Code: proto.CodeFR, Payload: proto.MarshalFR(1, "Listpresets/proj", "")})
	if _, err := c.readFrame(t, 200*time.Millisecond); err == nil {
		t.Fatal("suppressed board sent a reply, want none")
	}
}

// readFDBody reads FD frames from c and reassembles them into the full body.
func readFDBody(t *testing.T, c *testClient) []byte {
	t.Helper()
	var asm proto.ChunkAssembler
	for {
		f, err := c.readFrame(t, 2*time.Second)
		if err != nil {
			t.Fatalf("reading FD reply: %v", err)
		}
		if f.Code != proto.CodeFD {
			continue
		}
		chunk, err := proto.ParseFD(f.Payload)
		if err != nil {
			t.Fatalf("parsing FD chunk: %v", err)
		}
		if body, done := asm.Add(chunk.Chunk); done {
			return body
		}
	}
}

func TestSnapshotVolumeIsPlainWire(t *testing.T) {
	b := New(map[string]any{"line/ch1/volume": float32(0.746)})
	addr := startBoard(t, b)

	c := dial(t, addr)
	zb := c.subscribe(t)
	flat, err := proto.ParseZB(zb.Payload)
	if err != nil {
		t.Fatal(err)
	}
	// The ZB surfaces the seed value unscaled — a real 32R returns 0..1 on read.
	v, ok := flat["line/ch1/volume"].(float64)
	if !ok || v < 0.7459 || v > 0.7461 {
		t.Errorf("volume = %v (ok=%v), want ~0.746", flat["line/ch1/volume"], ok)
	}
}

func TestDropAfterFrames(t *testing.T) {
	b := New(map[string]any{"k": float32(1)})
	b.DropAfterFrames = 2 // Subscribe (1) + one more (2) → drop
	addr := startBoard(t, b)

	c := dial(t, addr)
	c.subscribe(t) // frame 1

	c.send(t, proto.Frame{Code: proto.CodeKA}) // frame 2 → board closes
	if _, err := c.readFrame(t, 2*time.Second); err == nil {
		t.Error("expected connection to be dropped after DropAfterFrames")
	}
}

func TestStaleAfterStopsDeltas(t *testing.T) {
	b := New(map[string]any{"line/ch1/volume": float32(0)})
	b.StaleAfter = 2
	addr := startBoard(t, b)

	a := dial(t, addr)
	a.subscribe(t)
	bc := dial(t, addr)
	bc.subscribe(t)

	// A writes 4 deltas; B should receive only the first 2.
	for i := 0; i < 4; i++ {
		a.send(t, proto.Frame{Code: proto.CodePV, Payload: proto.MarshalPV("line/ch1/volume", float32(i))})
	}

	got := 0
	for {
		if _, err := bc.readFrame(t, 400*time.Millisecond); err != nil {
			break
		}
		got++
	}
	if got != 2 {
		t.Errorf("B received %d deltas, want 2 (StaleAfter)", got)
	}
}

func TestCloseStopsAcceptingAndNoLeak(t *testing.T) {
	before := runtime.NumGoroutine()

	b := New(map[string]any{"k": float32(1)})
	addr, err := b.Start()
	if err != nil {
		t.Fatal(err)
	}
	c := dial(t, addr)
	c.subscribe(t)

	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// After Close a fresh dial must fail (accept loop stopped).
	if nc, err := net.DialTimeout("tcp", addr, 300*time.Millisecond); err == nil {
		nc.Close()
		t.Error("dial succeeded after Close; accept loop still running")
	}

	// Goroutine count should return to ~baseline (allow slack for scheduler).
	waitFor(t, func() bool { return runtime.NumGoroutine() <= before+2 })
}

// waitFor polls cond up to ~2s.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !cond() {
		t.Fatal("condition not met within timeout")
	}
}
