// Package fakeboard is an in-process UCNET server that stands in for a real
// StudioLive Series III mixer, so the client, CLI, and integration tests can run
// with no hardware.
//
// It reuses internal/proto for framing/codecs and internal/state for the live
// path→value tree, so the fake and the real client can never drift on the wire
// format. A Board listens on an ephemeral TCP port, answers the Hello/Subscribe
// handshake with a zlib ZB snapshot built from its seed tree, applies and
// broadcasts PV/PC/PS deltas, and implements simplified JM scene commands.
//
// It is a test double, not a simulator: the JM semantics, factory tree, and
// preset store are deliberately small and generic. See the per-method docs for
// exactly what each command does versus a real board.
package fakeboard

import (
	"bufio"
	"encoding/json"
	"net"
	"strings"
	"sync"

	"github.com/steveclarke/ucmix/internal/proto"
	"github.com/steveclarke/ucmix/internal/state"
)

// Board is an in-process fake UCNET mixer. The fault/quirk fields may be set
// before Start (or between connections); each defaults off.
type Board struct {
	// DropAfterFrames, when > 0, closes a connection after it has RECEIVED that
	// many frames (exercises client pacing/reconnect). The Subscribe frame counts.
	DropAfterFrames int
	// VolumeReadScale, when true, surfaces values under keys whose last path
	// segment is "volume" multiplied by 100 in the ZB snapshot, faithful to the
	// real board's read quirk. It affects the ZB only, not broadcast deltas.
	VolumeReadScale bool
	// StaleAfter, when > 0, stops delivering live delta frames to a connection
	// once that many deltas have been SENT to it (stale-subscription).
	StaleAfter int

	mu     sync.Mutex // guards tree access grouping, scenes, conns, subscribed
	tree   *state.Tree
	scenes map[string]map[string]any
	conns  map[*conn]struct{}

	ln        net.Listener
	done      chan struct{}
	wg        sync.WaitGroup
	closeOnce sync.Once
}

// conn is one accepted client connection.
type conn struct {
	nc         net.Conn
	writeMu    sync.Mutex // guards nc writes and deltasSent
	deltasSent int        // delta frames sent to this conn (guarded by writeMu)
	subscribed bool       // guarded by Board.mu
}

// New returns a Board seeded from a flat "/"-path map. Bool seed values are
// coerced to float32 1.0/0.0 (the board represents bools as PV floats, and the
// ZB encoder has no bool marker). The caller may mutate seed afterwards.
func New(seed map[string]any) *Board {
	b := &Board{
		tree:   state.NewTree(),
		scenes: make(map[string]map[string]any),
		conns:  make(map[*conn]struct{}),
		done:   make(chan struct{}),
	}
	b.tree.LoadSnapshot(coerceSeed(seed))
	return b
}

// coerceSeed returns a copy of seed with bool values replaced by float32
// 1.0/0.0 so the ZB encoder can represent every value.
func coerceSeed(seed map[string]any) map[string]any {
	out := make(map[string]any, len(seed))
	for k, v := range seed {
		if bv, ok := v.(bool); ok {
			if bv {
				out[k] = float32(1.0)
			} else {
				out[k] = float32(0.0)
			}
			continue
		}
		out[k] = v
	}
	return out
}

// Start binds an ephemeral local port and begins accepting connections. It
// returns the bound address (e.g. "127.0.0.1:54321").
func (b *Board) Start() (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	b.ln = ln
	b.wg.Add(1)
	go b.acceptLoop()
	return ln.Addr().String(), nil
}

// Close stops the accept loop, closes every live connection (unblocking their
// reads), and waits for all goroutines to exit. Safe to call more than once.
func (b *Board) Close() error {
	b.closeOnce.Do(func() {
		close(b.done)
		if b.ln != nil {
			b.ln.Close()
		}
		b.mu.Lock()
		for c := range b.conns {
			c.nc.Close()
		}
		b.mu.Unlock()
		b.wg.Wait()
	})
	return nil
}

// Snapshot returns a deep copy of the board's current tree (test helper).
func (b *Board) Snapshot() map[string]any { return b.tree.Snapshot() }

func (b *Board) acceptLoop() {
	defer b.wg.Done()
	for {
		nc, err := b.ln.Accept()
		if err != nil {
			return // listener closed on Close, or fatal accept error
		}
		c := &conn{nc: nc}
		b.mu.Lock()
		b.conns[c] = struct{}{}
		b.mu.Unlock()
		b.wg.Add(1)
		go b.serve(c)
	}
}

// serve reads frames from one connection until EOF, an error, or a configured
// drop. DropAfterFrames counts frames received here (single reader goroutine, so
// no lock is needed on the counter).
func (b *Board) serve(c *conn) {
	defer b.wg.Done()
	defer b.removeConn(c)
	defer c.nc.Close()

	r := bufio.NewReader(c.nc)
	received := 0
	for {
		f, err := proto.ReadFrame(r)
		if err != nil {
			return
		}
		received++
		b.handleFrame(c, f)
		if b.DropAfterFrames > 0 && received >= b.DropAfterFrames {
			return
		}
	}
}

func (b *Board) removeConn(c *conn) {
	b.mu.Lock()
	delete(b.conns, c)
	b.mu.Unlock()
}

// handleFrame dispatches one received frame.
func (b *Board) handleFrame(c *conn, f proto.Frame) {
	switch f.Code {
	case proto.CodePV, proto.CodePC, proto.CodePS:
		b.applyDelta(f)
		b.broadcast(c, proto.Encode(f))
	case proto.CodeJM:
		b.handleJM(c, f)
	case proto.CodeKA:
		// Keep-alive: accepted silently, no response.
	default:
		// Unknown/pre-handshake frames (e.g. Hello) are accepted and ignored;
		// the ZB is sent in response to Subscribe, not to any earlier frame.
	}
}

// applyDelta decodes a PV/PC/PS frame and applies it to the tree. Values are
// stored in ZB-encodable kinds: PV as float32, PS as string, PC color bytes as
// a string (cosmetic — broadcasts still carry the exact PC bytes).
func (b *Board) applyDelta(f proto.Frame) {
	switch f.Code {
	case proto.CodePV:
		if k, v, err := proto.UnmarshalPV(f.Payload); err == nil {
			b.tree.Apply(k, v)
		}
	case proto.CodePS:
		if k, v, err := proto.UnmarshalPS(f.Payload); err == nil {
			b.tree.Apply(k, v)
		}
	case proto.CodePC:
		if k, raw, err := proto.UnmarshalPC(f.Payload); err == nil {
			b.tree.Apply(k, string(raw))
		}
	}
}

// broadcast sends wire (an already-encoded delta frame) to every OTHER
// subscribed connection. The conn set is copied under b.mu, then writes happen
// with the lock released so a slow client cannot stall the board.
func (b *Board) broadcast(sender *conn, wire []byte) {
	b.mu.Lock()
	targets := make([]*conn, 0, len(b.conns))
	for c := range b.conns {
		if c != sender && c.subscribed {
			targets = append(targets, c)
		}
	}
	b.mu.Unlock()

	for _, c := range targets {
		c.sendDelta(b.StaleAfter, wire)
	}
}

// handleJM parses the JM JSON body's "id" and runs the simplified command.
func (b *Board) handleJM(c *conn, f proto.Frame) {
	id, body := jmID(f.Payload)
	switch id {
	case "Subscribe":
		b.subscribe(c)
	case "ResetMixer":
		var cmd proto.ResetMixerCmd
		json.Unmarshal(body, &cmd)
		if cmd.ResetScene != 0 || cmd.ResetProject != 0 {
			b.tree.LoadSnapshot(factoryTree())
			b.pushZBToAll()
		}
	case "RestorePreset":
		var cmd struct {
			PresetFile string `json:"presetFile"`
		}
		json.Unmarshal(body, &cmd)
		b.restorePreset(cmd.PresetFile)
	case "StorePreset":
		var cmd struct {
			PresetFile string `json:"presetFile"`
		}
		json.Unmarshal(body, &cmd)
		b.storePreset(cmd.PresetFile)
	case "Listpresets":
		b.listPresets(c)
	}
}

// subscribe marks the connection subscribed and sends it the current ZB
// snapshot (the initial full-state download).
func (b *Board) subscribe(c *conn) {
	b.mu.Lock()
	c.subscribed = true
	b.mu.Unlock()
	c.write(b.buildZBFrame())
}

// restorePreset loads a stored scene into the tree and pushes a fresh full ZB to
// all subscribers. Unknown preset names are a no-op. Simplification vs a real
// board: the fake pushes a whole ZB rather than computing per-parameter deltas.
func (b *Board) restorePreset(name string) {
	b.mu.Lock()
	scene, ok := b.scenes[name]
	b.mu.Unlock()
	if !ok {
		return
	}
	b.tree.LoadSnapshot(scene)
	b.pushZBToAll()
}

// storePreset snapshots the current tree into the scene store under name.
// Nothing is pushed to clients (matches "store only").
func (b *Board) storePreset(name string) {
	snap := b.tree.Snapshot()
	b.mu.Lock()
	b.scenes[name] = snap
	b.mu.Unlock()
}

// listPresets replies to the requesting connection with a JM frame listing the
// stored scene names: {"id":"Listpresets","presets":[…]}. Both ends of this
// contract live in-repo, so JM (not FD) is used for simplicity.
func (b *Board) listPresets(c *conn) {
	b.mu.Lock()
	names := make([]string, 0, len(b.scenes))
	for n := range b.scenes {
		names = append(names, n)
	}
	b.mu.Unlock()
	reply := struct {
		ID      string   `json:"id"`
		Presets []string `json:"presets"`
	}{"Listpresets", names}
	c.write(proto.Encode(proto.Frame{Code: proto.CodeJM, Payload: proto.MarshalJM(reply)}))
}

// pushZBToAll sends a fresh full ZB snapshot to every subscribed connection.
func (b *Board) pushZBToAll() {
	frame := b.buildZBFrame()
	b.mu.Lock()
	targets := make([]*conn, 0, len(b.conns))
	for c := range b.conns {
		if c.subscribed {
			targets = append(targets, c)
		}
	}
	b.mu.Unlock()
	for _, c := range targets {
		c.write(frame)
	}
}

// buildZBFrame builds a ZB frame from the current tree, applying the volume read
// scale quirk if enabled.
func (b *Board) buildZBFrame() []byte {
	snap := b.tree.Snapshot()
	if b.VolumeReadScale {
		for k, v := range snap {
			if lastSeg(k) == "volume" {
				if f, ok := toFloat(v); ok {
					snap[k] = f * 100
				}
			}
		}
	}
	blob, err := proto.BuildZB(snap)
	if err != nil {
		// Every value in the tree is stored in a ZB-encodable kind, so this
		// cannot happen; send an empty ZB rather than panic if it somehow does.
		blob, _ = proto.BuildZB(map[string]any{})
	}
	return proto.Encode(proto.Frame{Code: proto.CodeZB, Payload: blob})
}

// write sends raw bytes to the connection under its write mutex. Errors (e.g.
// the peer already closed) are ignored.
func (c *conn) write(b []byte) {
	c.writeMu.Lock()
	c.nc.Write(b)
	c.writeMu.Unlock()
}

// sendDelta sends a delta frame unless the connection has gone stale. The
// deltasSent counter is touched by every broadcasting goroutine, so it is
// guarded by writeMu.
func (c *conn) sendDelta(staleAfter int, wire []byte) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if staleAfter > 0 && c.deltasSent >= staleAfter {
		return
	}
	c.deltasSent++
	c.nc.Write(wire)
}

// jmID extracts the "id" field from a JM payload (4-byte LE length prefix +
// JSON body) and returns the id and the JSON body.
func jmID(payload []byte) (string, []byte) {
	body := payload
	if len(body) >= 4 {
		body = body[4:]
	}
	var probe struct {
		ID string `json:"id"`
	}
	json.Unmarshal(body, &probe)
	return probe.ID, body
}

// factoryTree is the small generic "factory default" state a ResetMixer loads.
func factoryTree() map[string]any {
	return map[string]any{
		"global/mixer_name": "StudioLive",
		"line/ch1/mute":     float32(0.0),
		"line/ch1/volume":   float32(0.0),
		"main/ch1/volume":   float32(0.75),
	}
}

// lastSeg returns the final "/"-delimited segment of path.
func lastSeg(path string) string {
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return path
}

// toFloat coerces float32/float64 tree values to float64 for scaling.
func toFloat(v any) (float64, bool) {
	switch f := v.(type) {
	case float64:
		return f, true
	case float32:
		return float64(f), true
	default:
		return 0, false
	}
}
