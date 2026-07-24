package ucmix

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/steveclarke/ucmix/internal/proto"
	"github.com/steveclarke/ucmix/internal/transport"
)

// fakeTransport is an in-memory transport.Transport for client-logic tests. It
// records sent frames and delivers inbound frames the test injects via deliver.
type fakeTransport struct {
	mu   sync.Mutex
	sent []proto.Frame

	in        chan proto.Frame
	closeOnce sync.Once
	closed    chan struct{}
}

func newFakeTransport() *fakeTransport {
	return &fakeTransport{in: make(chan proto.Frame), closed: make(chan struct{})}
}

func (f *fakeTransport) Send(ctx context.Context, fr proto.Frame) error {
	select {
	case <-f.closed:
		return transport.ErrClosed
	default:
	}
	f.mu.Lock()
	f.sent = append(f.sent, fr)
	f.mu.Unlock()
	return nil
}

func (f *fakeTransport) Frames() <-chan proto.Frame { return f.in }

func (f *fakeTransport) Close() error {
	f.closeOnce.Do(func() {
		close(f.closed)
		close(f.in)
	})
	return nil
}

// deliver pushes an inbound frame to the client. Safe until Close.
func (f *fakeTransport) deliver(fr proto.Frame) { f.in <- fr }

func (f *fakeTransport) sentFrames() []proto.Frame {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]proto.Frame(nil), f.sent...)
}

// lastFrame returns the most recently sent frame, or fails.
func (f *fakeTransport) lastFrame(t *testing.T) proto.Frame {
	t.Helper()
	fs := f.sentFrames()
	if len(fs) == 0 {
		t.Fatal("no frames sent")
	}
	return fs[len(fs)-1]
}

// commitRecorder captures every commit-barrier hold the client performs, so
// tests can assert how many times the barrier fired (one per Set/SetMany) and
// for how long. It replaces the real time-based hold via the commitSleep seam.
type commitRecorder struct {
	mu    sync.Mutex
	holds []time.Duration
}

func (r *commitRecorder) record(d time.Duration) {
	r.mu.Lock()
	r.holds = append(r.holds, d)
	r.mu.Unlock()
}

func (r *commitRecorder) reset() {
	r.mu.Lock()
	r.holds = nil
	r.mu.Unlock()
}

func (r *commitRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.holds)
}

func (r *commitRecorder) last() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.holds) == 0 {
		return 0
	}
	return r.holds[len(r.holds)-1]
}

// testCommits records commit-barrier holds for the whole package. TestMain wires
// the commitSleep seam to it so no test ever waits real time for a barrier;
// barrier tests reset it and assert its contents.
var testCommits = &commitRecorder{}

// TestMain replaces the commit-barrier hold with an instant recording stub for
// the whole package (Set/SetMany otherwise hold commitDelay of real time).
func TestMain(m *testing.M) {
	commitSleep = func(ctx context.Context, d time.Duration) error {
		testCommits.record(d)
		return ctx.Err()
	}
	os.Exit(m.Run())
}

// useFake installs a dialTransport seam returning ft and restores it on cleanup.
func useFake(t *testing.T, ft *fakeTransport) {
	t.Helper()
	orig := dialTransport
	dialTransport = func(context.Context, string, transport.Options) (transport.Transport, error) {
		return ft, nil
	}
	t.Cleanup(func() { dialTransport = orig })
}

// zbFrame builds a ZB frame carrying seed.
func zbFrame(t *testing.T, seed map[string]any) proto.Frame {
	t.Helper()
	blob, err := proto.BuildZB(seed)
	if err != nil {
		t.Fatalf("BuildZB: %v", err)
	}
	return proto.Frame{Code: proto.CodeZB, Payload: blob}
}

// connectWithZB connects a client to ft, delivering seed as the ZB reply.
func connectWithZB(t *testing.T, ft *fakeTransport, seed map[string]any) *Client {
	t.Helper()
	useFake(t, ft)
	done := make(chan *Client, 1)
	go func() {
		c, err := Connect(context.Background(), "fake")
		if err != nil {
			t.Errorf("Connect: %v", err)
			done <- nil
			return
		}
		done <- c
	}()
	ft.deliver(zbFrame(t, seed))
	c := <-done
	if c == nil {
		t.Fatal("Connect failed")
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// connectWithBlob connects a client to ft, delivering a pre-built ZB blob (e.g.
// a captured real-hardware snapshot) as the ZB reply.
func connectWithBlob(t *testing.T, ft *fakeTransport, blob []byte) *Client {
	t.Helper()
	useFake(t, ft)
	done := make(chan *Client, 1)
	go func() {
		c, err := Connect(context.Background(), "fake")
		if err != nil {
			t.Errorf("Connect: %v", err)
			done <- nil
			return
		}
		done <- c
	}()
	ft.deliver(proto.Frame{Code: proto.CodeZB, Payload: blob})
	c := <-done
	if c == nil {
		t.Fatal("Connect failed")
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

func TestConnectSendsSubscribeFirstThenBlocksUntilZB(t *testing.T) {
	ft := newFakeTransport()
	useFake(t, ft)

	done := make(chan *Client, 1)
	go func() {
		c, err := Connect(context.Background(), "fake")
		if err != nil {
			t.Errorf("Connect: %v", err)
		}
		done <- c
	}()

	// Subscribe must be the first (and, pre-ZB, only) frame sent.
	waitFor(t, func() bool { return len(ft.sentFrames()) >= 1 })
	first := ft.sentFrames()[0]
	if first.Code != proto.CodeJM {
		t.Fatalf("first frame code = %q, want JM (Subscribe)", first.Code)
	}
	if id, _ := jmField(first.Payload, "id"); id != "Subscribe" {
		t.Fatalf("first frame id = %q, want Subscribe", id)
	}

	// Connect must not return before the ZB is delivered.
	select {
	case c := <-done:
		t.Fatalf("Connect returned before ZB delivered: %v", c)
	case <-time.After(50 * time.Millisecond):
	}

	ft.deliver(zbFrame(t, map[string]any{"line/ch1/username": "Kick"}))

	select {
	case c := <-done:
		if c == nil {
			t.Fatal("Connect returned nil client")
		}
		if v, ok := c.Get("line/ch1/username"); !ok || v != "Kick" {
			t.Fatalf("after connect Get(username) = %v (ok=%v), want Kick", v, ok)
		}
		c.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("Connect did not return after ZB delivered")
	}
}

func TestConnectFailsIfClosedBeforeZB(t *testing.T) {
	ft := newFakeTransport()
	useFake(t, ft)
	go ft.Close()
	_, err := Connect(context.Background(), "fake")
	if err == nil {
		t.Fatal("Connect should fail when transport closes before ZB")
	}
}

func TestDeltaMergeAppliesPVPSPC(t *testing.T) {
	ft := newFakeTransport()
	c := connectWithZB(t, ft, map[string]any{"line/ch1/username": "Orig"})

	ft.deliver(proto.Frame{Code: proto.CodePV, Payload: proto.MarshalPV("line/ch1/pan", 0.25)})
	ft.deliver(proto.Frame{Code: proto.CodePS, Payload: proto.MarshalPS("line/ch1/username", "New")})
	ft.deliver(proto.Frame{Code: proto.CodePC, Payload: proto.MarshalPC("line/ch1/color", []byte{0x4e, 0xd2, 0xff, 0xff})})

	// Color is delivered last; the merge loop applies frames in order on one
	// goroutine, so waiting for color guarantees pan and username are in too.
	waitFor(t, func() bool {
		_, ok := c.GetRaw("line/ch1/color")
		return ok
	})
	if v, ok := c.GetRaw("line/ch1/username"); !ok || v != "New" {
		t.Errorf("username = %v (ok=%v), want New", v, ok)
	}
	if v, ok := c.GetRaw("line/ch1/pan"); !ok || v.(float32) != 0.25 {
		t.Errorf("pan = %v (ok=%v), want 0.25", v, ok)
	}
	raw, ok := c.GetRaw("line/ch1/color")
	if !ok {
		t.Fatal("color missing")
	}
	if b, isBytes := raw.([]byte); !isBytes || len(b) != 4 || b[0] != 0x4e {
		t.Errorf("color raw = %v, want []byte{4e d2 ff ff}", raw)
	}
}

func TestSetEncodesFaderPatchMuteNameColor(t *testing.T) {
	ft := newFakeTransport()
	c := connectWithZB(t, ft, map[string]any{})
	ctx := context.Background()

	// Fader -6 dB -> wire 0.746 (NOT 74.6: ReadScale is read-only).
	if err := c.SetFaderDB(ctx, Line, 1, -6); err != nil {
		t.Fatal(err)
	}
	key, val := decodePV(t, c, ft)
	if key != "line/ch1/volume" {
		t.Fatalf("fader key = %q", key)
	}
	if math.Abs(float64(val)-0.746) > 1e-3 {
		t.Fatalf("fader -6 dB -> %v, want ~0.746", val)
	}

	// Input patch 25 -> 25/32 = 0.78125.
	if err := c.SetInputPatch(ctx, Line, 1, 25); err != nil {
		t.Fatal(err)
	}
	key, val = decodePV(t, c, ft)
	if key != "line/ch1/adc_src" || math.Abs(float64(val)-0.78125) > 1e-6 {
		t.Fatalf("patch 25 -> %s=%v, want line/ch1/adc_src=0.78125", key, val)
	}

	// Mute on -> PV 1.0.
	if err := c.Mute(ctx, Line, 3, true); err != nil {
		t.Fatal(err)
	}
	key, val = decodePV(t, c, ft)
	if key != "line/ch3/mute" || val != 1.0 {
		t.Fatalf("mute -> %s=%v, want line/ch3/mute=1", key, val)
	}

	// Name -> PS.
	if err := c.SetName(ctx, Line, 2, "Vox"); err != nil {
		t.Fatal(err)
	}
	f := ft.lastFrame(t)
	if f.Code != proto.CodePS {
		t.Fatalf("name frame code = %q, want PS", f.Code)
	}
	k, s, err := proto.UnmarshalPS(f.Payload)
	if err != nil || k != "line/ch2/username" || s != "Vox" {
		t.Fatalf("name PS = %s=%q (err=%v)", k, s, err)
	}

	// Color -> PC with alpha appended.
	if err := c.SetColor(ctx, Line, 4, "4ed2ff"); err != nil {
		t.Fatal(err)
	}
	f = ft.lastFrame(t)
	if f.Code != proto.CodePC {
		t.Fatalf("color frame code = %q, want PC", f.Code)
	}
	k, raw, err := proto.UnmarshalPC(f.Payload)
	if err != nil || k != "line/ch4/color" {
		t.Fatalf("color key = %q (err=%v)", k, err)
	}
	if len(raw) != 4 || raw[0] != 0x4e || raw[1] != 0xd2 || raw[2] != 0xff || raw[3] != 0xff {
		t.Fatalf("color bytes = % x, want 4e d2 ff ff", raw)
	}
}

func TestSetSendDBUsesSendTaper(t *testing.T) {
	ft := newFakeTransport()
	c := connectWithZB(t, ft, map[string]any{})
	if err := c.SetSendDB(context.Background(), Line, 5, 3, -6); err != nil {
		t.Fatal(err)
	}
	key, val := decodePV(t, c, ft)
	if key != "line/ch5/aux3" || math.Abs(float64(val)-0.746) > 1e-3 {
		t.Fatalf("send -6 dB -> %s=%v, want line/ch5/aux3=~0.746", key, val)
	}
}

func TestSetOverRangeReturnsError(t *testing.T) {
	ft := newFakeTransport()
	c := connectWithZB(t, ft, map[string]any{})
	if err := c.SetFaderDB(context.Background(), Line, 1, 50); err == nil {
		t.Fatal("SetFaderDB(+50) should error (over range)")
	}
}

func TestGetHumanizesVolume(t *testing.T) {
	ft := newFakeTransport()
	// The board returns the plain 0..1 wire position: 0.746 = -6 dB. A read-scale
	// divide here would corrupt it (0.746/100 -> ~-83 dB).
	c := connectWithZB(t, ft, map[string]any{"line/ch1/volume": float32(0.746)})
	v, ok := c.Get("line/ch1/volume")
	if !ok {
		t.Fatal("volume missing")
	}
	if math.Abs(v.(float64)-(-6)) > 0.2 {
		t.Fatalf("Get(volume) = %v dB, want ~-6", v)
	}
	// Raw stays as stored (ZB floats decode to float64).
	rv, _ := c.GetRaw("line/ch1/volume")
	rf, _ := toFloat64(rv)
	if math.Abs(rf-0.746) > 1e-3 {
		t.Fatalf("GetRaw(volume) = %v, want 0.746", rv)
	}
}

// TestGetHumanizesDCAMasterVolume is the regression guard for the shipped gap:
// the DCA master fader (filtergroup/chN/volume) read back the raw wire value
// (~0.746) instead of dB. Through Client.Get it must humanize to -6 dB, exactly
// like line/aux volume — the wire-level snapshot test never exercised the taper.
func TestGetHumanizesDCAMasterVolume(t *testing.T) {
	ft := newFakeTransport()
	c := connectWithZB(t, ft, map[string]any{"filtergroup/ch1/volume": float32(0.746)})
	v, ok := c.Get("filtergroup/ch1/volume")
	if !ok {
		t.Fatal("filtergroup volume missing")
	}
	if math.Abs(v.(float64)-(-6)) > 0.2 {
		t.Fatalf("Get(filtergroup/ch1/volume) = %v dB, want ~-6", v)
	}
	// Raw stays as stored.
	rv, _ := c.GetRaw("filtergroup/ch1/volume")
	rf, _ := toFloat64(rv)
	if math.Abs(rf-0.746) > 1e-3 {
		t.Fatalf("GetRaw(filtergroup/ch1/volume) = %v, want 0.746", rv)
	}
}

// TestExtendedBusFamiliesHumanize walks one representative of each newly
// humanized family through Get and asserts the human unit, not the raw wire
// value, comes back. Wire anchors: 0.746 = -6 dB (fader/send), 0.03125 = input 1
// (adc_src), 1.0 = true (toggle), 0.5 = 400 ms (limiter release).
func TestExtendedBusFamiliesHumanize(t *testing.T) {
	ft := newFakeTransport()
	c := connectWithZB(t, ft, map[string]any{
		"sub/ch1/volume":             float32(0.746),
		"main/ch2/volume":            float32(0.746),
		"talkback/ch1/aux3":          float32(0.746),
		"filtergroup/ch1/fx2":        float32(0.746),
		"aux/ch1/adc_src":            float32(0.03125),
		"sub/ch1/mute":               float32(1),
		"geq/ch1/on":                 float32(1),
		"main/ch1/limit/release":     float32(0.5),
		"return/ch1/limit/threshold": float32(1),
	})
	cases := []struct {
		path string
		want float64
		tol  float64
	}{
		{"sub/ch1/volume", -6, 0.2},
		{"main/ch2/volume", -6, 0.2},
		{"talkback/ch1/aux3", -6, 0.2},
		{"filtergroup/ch1/fx2", -6, 0.2},
		{"aux/ch1/adc_src", 1, 1e-6},           // input number
		{"main/ch1/limit/release", 400, 1},     // ms
		{"return/ch1/limit/threshold", 0, 0.1}, // dB (1.0 = 0 dB)
	}
	for _, tc := range cases {
		v, ok := c.Get(tc.path)
		if !ok {
			t.Errorf("Get(%q) missing", tc.path)
			continue
		}
		f, isFloat := v.(float64)
		if !isFloat {
			t.Errorf("Get(%q) = %T, want humanized float64", tc.path, v)
			continue
		}
		if math.Abs(f-tc.want) > tc.tol {
			t.Errorf("Get(%q) = %v, want ~%v", tc.path, f, tc.want)
		}
	}
	// Toggles humanize to bool, not a raw float.
	for _, path := range []string{"sub/ch1/mute", "geq/ch1/on"} {
		v, ok := c.Get(path)
		if !ok {
			t.Errorf("Get(%q) missing", path)
			continue
		}
		if b, isBool := v.(bool); !isBool || !b {
			t.Errorf("Get(%q) = %v (%T), want true", path, v, v)
		}
	}
}

// TestSetDCAMasterVolumeRoundTrips confirms the write path for the extended
// buses: a dB fader write tapers to the wire, and reading the stored wire value
// back humanizes to the same dB. Mirrors the line/aux fader round-trip.
func TestSetDCAMasterVolumeRoundTrips(t *testing.T) {
	ft := newFakeTransport()
	c := connectWithZB(t, ft, map[string]any{})
	ctx := context.Background()

	if err := c.Set(ctx, "filtergroup/ch1/volume", -6.0); err != nil {
		t.Fatal(err)
	}
	key, val := decodePV(t, c, ft)
	if key != "filtergroup/ch1/volume" || math.Abs(float64(val)-0.746) > 1e-3 {
		t.Fatalf("Set(filtergroup/ch1/volume, -6) -> %s=%v, want filtergroup/ch1/volume=~0.746", key, val)
	}
	// Feed the wire value back as board state and read it as dB.
	c2 := connectWithZB(t, newFakeTransport(), map[string]any{"filtergroup/ch1/volume": val})
	v, ok := c2.Get("filtergroup/ch1/volume")
	if !ok || math.Abs(v.(float64)-(-6)) > 0.2 {
		t.Fatalf("round-trip Get = %v (ok=%v), want ~-6 dB", v, ok)
	}
}

func TestGetHumanizesColorFromSnapshot(t *testing.T) {
	ft := newFakeTransport()
	// A ZB stores color ABGR-packed into an integer: the little-endian read of the
	// wire bytes 94 78 ce ff. Humanized read unpacks it back to the RGBA bytes,
	// symmetric with the write (which parses "9478ce" into those bytes).
	c := connectWithZB(t, ft, map[string]any{"line/ch1/color": int64(0xffce7894)})
	v, ok := c.Get("line/ch1/color")
	if !ok {
		t.Fatal("color missing")
	}
	b, isBytes := v.([]byte)
	if !isBytes {
		t.Fatalf("Get(color) = %T (%v), want humanized []byte", v, v)
	}
	if got := hex.EncodeToString(b); got != "9478ceff" {
		t.Errorf("Get(color) = %s, want 9478ceff (unpacked RGBA)", got)
	}
	// --raw keeps the packed integer.
	if rv, _ := c.GetRaw("line/ch1/color"); rv != int64(0xffce7894) {
		t.Errorf("GetRaw(color) = %v, want the packed int 0xffce7894", rv)
	}
}

func TestGetHumanizesColorFromDelta(t *testing.T) {
	ft := newFakeTransport()
	c := connectWithZB(t, ft, map[string]any{})
	// A live PC delta already carries the RGBA bytes; Get passes them through.
	ft.deliver(proto.Frame{Code: proto.CodePC, Payload: proto.MarshalPC("line/ch1/color", []byte{0x4e, 0xd2, 0xff, 0xff})})
	waitFor(t, func() bool { _, ok := c.GetRaw("line/ch1/color"); return ok })
	v, ok := c.Get("line/ch1/color")
	if !ok {
		t.Fatal("color missing")
	}
	b, _ := v.([]byte)
	if got := hex.EncodeToString(b); got != "4ed2ffff" {
		t.Errorf("Get(color) = %s, want 4ed2ffff (PC delta bytes pass through)", got)
	}
}

func TestColorReadInvertsWritePacking(t *testing.T) {
	// The write parses "9478ce" to the wire bytes below; a snapshot stores them
	// little-endian packed. humanizeColor must invert the packing so a set value
	// reads back identically (bug #14: read must round-trip the write).
	wire := []byte{0x94, 0x78, 0xce, 0xff}
	packed := int64(uint32(wire[0]) | uint32(wire[1])<<8 | uint32(wire[2])<<16 | uint32(wire[3])<<24)
	got, ok := humanizeColor(packed).([]byte)
	if !ok || !bytes.Equal(got, wire) {
		t.Errorf("humanizeColor(packed) = % x, want % x", got, wire)
	}
}

// TestRealSnapshotColorHumanizes reads the captured 32R snapshot end to end
// through the client and checks a known channel color humanizes to hex.
// line/ch1 in the capture is 0x4ed2ffff (cyan); the read must return that, not
// the raw packed integer (bug #14).
func TestRealSnapshotColorHumanizes(t *testing.T) {
	blob, err := os.ReadFile("internal/proto/testdata/real-snapshot-32r.zb")
	if err != nil {
		t.Skipf("no real-hardware fixture: %v", err)
	}
	ft := newFakeTransport()
	c := connectWithBlob(t, ft, blob)
	v, ok := c.Get("line/ch1/color")
	if !ok {
		t.Fatal("line/ch1/color missing from snapshot")
	}
	b, isBytes := v.([]byte)
	if !isBytes {
		t.Fatalf("Get(color) = %T (%v), want humanized []byte", v, v)
	}
	if got := hex.EncodeToString(b); got != "4ed2ffff" {
		t.Errorf("Get(line/ch1/color) = %s, want 4ed2ffff", got)
	}
}

func TestJMSceneCommandBodies(t *testing.T) {
	ft := newFakeTransport()
	c := connectWithZB(t, ft, map[string]any{})
	ctx := context.Background()

	if err := c.RecallScene(ctx, "MainLive", "Intro"); err != nil {
		t.Fatal(err)
	}
	f := ft.lastFrame(t)
	if id, _ := jmField(f.Payload, "id"); id != "RestorePreset" {
		t.Fatalf("recall id = %q, want RestorePreset", id)
	}
	if pf, _ := jmField(f.Payload, "presetFile"); pf != "presets/proj/MainLive/Intro" {
		t.Fatalf("recall presetFile = %q", pf)
	}

	if err := c.StoreScene(ctx, "MainLive", "Intro"); err != nil {
		t.Fatal(err)
	}
	if id, _ := jmField(ft.lastFrame(t).Payload, "id"); id != "StorePreset" {
		t.Fatalf("store id = %q, want StorePreset", id)
	}

	if err := c.ResetMixer(ctx, ResetScope{Scene: true, Project: true}); err != nil {
		t.Fatal(err)
	}
	f = ft.lastFrame(t)
	if id, _ := jmField(f.Payload, "id"); id != "ResetMixer" {
		t.Fatalf("reset id = %q, want ResetMixer", id)
	}
	if s, _ := jmNumField(f.Payload, "resetSceneSettings"); s != 1 {
		t.Fatalf("resetSceneSettings = %v, want 1", s)
	}
}

func TestListProjectsRoutesReplyOutOfMergeLoop(t *testing.T) {
	ft := newFakeTransport()
	c := connectWithZB(t, ft, map[string]any{})

	done := make(chan []Project, 1)
	go func() {
		p, err := c.ListProjects(context.Background())
		if err != nil {
			t.Errorf("ListProjects: %v", err)
		}
		done <- p
	}()

	// Wait for the request to be sent, then deliver the JM reply.
	waitFor(t, func() bool {
		for _, f := range ft.sentFrames() {
			if id, _ := jmField(f.Payload, "id"); id == "Listpresets" {
				return true
			}
		}
		return false
	})
	reply := struct {
		ID      string   `json:"id"`
		Presets []string `json:"presets"`
	}{"Listpresets", []string{"MainLive", "Rehearsal"}}
	ft.deliver(proto.Frame{Code: proto.CodeJM, Payload: proto.MarshalJM(reply)})

	select {
	case p := <-done:
		if len(p) != 2 || p[0].Name != "MainLive" || p[1].Name != "Rehearsal" {
			t.Fatalf("projects = %+v", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ListProjects did not return")
	}
}

func TestSetUnknownKeyRawEncode(t *testing.T) {
	ft := newFakeTransport()
	c := connectWithZB(t, ft, map[string]any{})
	ctx := context.Background()

	// Unknown float key sends RAW (no taper, no scale) — unlike a known volume.
	if err := c.Set(ctx, "global/foobar", 0.42); err != nil {
		t.Fatal(err)
	}
	key, val := decodePV(t, c, ft)
	if key != "global/foobar" || math.Abs(float64(val)-0.42) > 1e-6 {
		t.Fatalf("unknown float -> %s=%v, want global/foobar=0.42 (raw)", key, val)
	}

	// Unknown bool -> PV 1/0.
	if err := c.Set(ctx, "global/flag", true); err != nil {
		t.Fatal(err)
	}
	key, val = decodePV(t, c, ft)
	if key != "global/flag" || val != 1.0 {
		t.Fatalf("unknown bool -> %s=%v, want global/flag=1", key, val)
	}

	// Unknown string -> PS.
	if err := c.Set(ctx, "global/label", "hi"); err != nil {
		t.Fatal(err)
	}
	f := ft.lastFrame(t)
	if f.Code != proto.CodePS {
		t.Fatalf("unknown string frame code = %q, want PS", f.Code)
	}
	if k, s, err := proto.UnmarshalPS(f.Payload); err != nil || k != "global/label" || s != "hi" {
		t.Fatalf("unknown string PS = %s=%q (err=%v)", k, s, err)
	}
}

func TestVerifyAgainstReportsMismatches(t *testing.T) {
	ft := newFakeTransport()
	c := connectWithZB(t, ft, map[string]any{
		"line/ch1/pan":  float32(0.5),
		"line/ch1/mute": float32(1.0),
	})
	miss := c.VerifyAgainst(map[string]any{
		"line/ch1/pan":     0.5,        // matches within tolerance
		"line/ch1/mute":    0.0,        // differs (1 vs 0)
		"line/ch1/missing": "whatever", // absent
	})
	if len(miss) != 2 {
		t.Fatalf("mismatches = %d (%+v), want 2", len(miss), miss)
	}
	byPath := map[string]Mismatch{}
	for _, m := range miss {
		byPath[m.Path] = m
	}
	if _, ok := byPath["line/ch1/mute"]; !ok {
		t.Error("expected mute mismatch")
	}
	if m, ok := byPath["line/ch1/missing"]; !ok || m.Got != nil {
		t.Errorf("expected missing-path mismatch with Got=nil, got %+v", m)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	ft := newFakeTransport()
	c := connectWithZB(t, ft, map[string]any{})
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// connectWithZBOpts connects a client to ft with connect options, delivering
// seed as the ZB reply. Used to exercise WithCommitDelay.
func connectWithZBOpts(t *testing.T, ft *fakeTransport, seed map[string]any, opts ...Option) *Client {
	t.Helper()
	useFake(t, ft)
	done := make(chan *Client, 1)
	go func() {
		c, err := Connect(context.Background(), "fake", opts...)
		if err != nil {
			t.Errorf("Connect: %v", err)
			done <- nil
			return
		}
		done <- c
	}()
	ft.deliver(zbFrame(t, seed))
	c := <-done
	if c == nil {
		t.Fatal("Connect failed")
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestSetHoldsOneCommitBarrier(t *testing.T) {
	ft := newFakeTransport()
	c := connectWithZB(t, ft, map[string]any{}) // default commitDelay
	testCommits.reset()

	if err := c.Set(context.Background(), "line/ch1/mute", true); err != nil {
		t.Fatal(err)
	}
	// A single write commits exactly once, for the default hold.
	if got := testCommits.count(); got != 1 {
		t.Fatalf("commit barriers = %d, want 1", got)
	}
	if got := testCommits.last(); got != defaultCommitDelay {
		t.Fatalf("commit hold = %v, want %v", got, defaultCommitDelay)
	}
}

func TestSetManyStreamsAllWritesThenOneBarrier(t *testing.T) {
	ft := newFakeTransport()
	c := connectWithZB(t, ft, map[string]any{})
	testCommits.reset()

	settings := []Setting{
		{Path: "line/ch1/mute", Value: true},
		{Path: "line/ch2/mute", Value: false},
		{Path: "line/ch3/username", Value: "Vox"},
	}
	if err := c.SetMany(context.Background(), settings); err != nil {
		t.Fatal(err)
	}

	// Every write goes out over the one transport, in order (Subscribe is frame
	// 0), and the whole burst commits exactly once — not once per write.
	sent := ft.sentFrames()
	if len(sent) != 1+len(settings) {
		t.Fatalf("sent %d frames, want %d (Subscribe + %d writes)", len(sent), 1+len(settings), len(settings))
	}
	if k, _, err := proto.UnmarshalPV(sent[1].Payload); err != nil || k != "line/ch1/mute" {
		t.Fatalf("first write = %s (err=%v), want line/ch1/mute", k, err)
	}
	if k, _, err := proto.UnmarshalPV(sent[2].Payload); err != nil || k != "line/ch2/mute" {
		t.Fatalf("second write = %s (err=%v), want line/ch2/mute", k, err)
	}
	if k, s, err := proto.UnmarshalPS(sent[3].Payload); err != nil || k != "line/ch3/username" || s != "Vox" {
		t.Fatalf("third write = %s=%q (err=%v), want line/ch3/username=Vox", k, s, err)
	}
	if got := testCommits.count(); got != 1 {
		t.Fatalf("commit barriers = %d, want 1 for the whole batch", got)
	}
}

func TestCommitDelayZeroSkipsBarrier(t *testing.T) {
	ft := newFakeTransport()
	c := connectWithZBOpts(t, ft, map[string]any{}, WithCommitDelay(0))
	testCommits.reset()

	// A commitDelay-0 client (what fade uses) streams writes with no per-write
	// hold, so a per-step write must never fire the barrier.
	for i := 0; i < 5; i++ {
		if err := c.Set(context.Background(), "line/ch1/volume", 0.5); err != nil {
			t.Fatal(err)
		}
	}
	if got := testCommits.count(); got != 0 {
		t.Fatalf("commit barriers = %d, want 0 with commitDelay 0", got)
	}
}

// --- test helpers ---

// decodePV grabs the last sent frame, asserts PV, and returns its key/value.
func decodePV(t *testing.T, _ *Client, ft *fakeTransport) (string, float32) {
	t.Helper()
	f := ft.lastFrame(t)
	if f.Code != proto.CodePV {
		t.Fatalf("frame code = %q, want PV", f.Code)
	}
	k, v, err := proto.UnmarshalPV(f.Payload)
	if err != nil {
		t.Fatalf("UnmarshalPV: %v", err)
	}
	return k, v
}

// jmField strips the 4-byte JM length prefix and returns a string field.
func jmField(payload []byte, field string) (string, bool) {
	m := jmBody(payload)
	s, ok := m[field].(string)
	return s, ok
}

func jmNumField(payload []byte, field string) (float64, bool) {
	m := jmBody(payload)
	f, ok := m[field].(float64)
	return f, ok
}

func jmBody(payload []byte) map[string]any {
	body := payload
	if len(body) >= 4 {
		body = body[4:]
	}
	var m map[string]any
	json.Unmarshal(body, &m)
	return m
}
