package ucmix

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/steveclarke/ucmix/internal/proto"
	"github.com/steveclarke/ucmix/internal/schema"
	"github.com/steveclarke/ucmix/internal/state"
	"github.com/steveclarke/ucmix/internal/transport"
)

// dialTransport is the test seam for opening a transport. Production uses
// transport.Dial (real TCP); unit tests swap it for a func returning an
// in-memory Transport fake. Never reassigned outside tests.
var dialTransport = transport.Dial

// commitSleep is the test seam for the commit barrier's hold. Production sleeps
// for d bounded by ctx; tests swap it for a recorder that returns immediately so
// the barrier's behavior (how many times it fires, and for how long) is asserted
// without real waits. Never reassigned outside tests.
var commitSleep = func(ctx context.Context, d time.Duration) error {
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ErrConnectionClosed is returned when the transport closes before the ZB
// snapshot arrives during Connect.
var ErrConnectionClosed = errors.New("ucmix: connection closed before snapshot")

// Client is a connected session with one mixer. It owns a transport, a live
// state tree fed by a background merge loop, and the write path that encodes
// values back to the board. A Client is safe for concurrent use.
type Client struct {
	t    transport.Transport
	tree *state.Tree

	commitDelay time.Duration // post-write hold applied by Set/SetMany; 0 = no barrier

	ckAsm proto.ChunkAssembler // reassembles a CK-chunked snapshot; used only from the read goroutine

	wg        sync.WaitGroup
	closeOnce sync.Once
	closeErr  error

	mu          sync.Mutex      // guards listWaiters
	listWaiters []chan []string // registered ListProjects reply waiters
}

// Connect dials addr, sends the Subscribe handshake, and blocks until the board
// replies with the ZB snapshot (loaded into the state tree). It then runs the
// delta-merge loop in the background and returns a ready Client.
//
// The handshake matches the real featherbear connect(): a single JM Subscribe
// frame, then a wait for ZB. No Hello frame is sent — Hello (UM) is bound to
// UDP metering, which is out of scope for v1.
func Connect(ctx context.Context, addr string, opts ...Option) (*Client, error) {
	cfg := resolve(opts)

	waitCtx := ctx
	if cfg.connectTimeout > 0 {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, cfg.connectTimeout)
		defer cancel()
	}

	t, err := dialTransport(ctx, addr, cfg.transportOpts)
	if err != nil {
		return nil, err
	}

	c := &Client{t: t, tree: state.NewTree(), commitDelay: cfg.commitDelay}

	sub := proto.Frame{Code: proto.CodeJM, Payload: proto.MarshalJM(cfg.subscribeCmd())}
	if err := t.Send(waitCtx, sub); err != nil {
		_ = t.Close()
		return nil, fmt.Errorf("ucmix: sending Subscribe: %w", err)
	}

	// Block until the ZB snapshot arrives. Any deltas that precede it are
	// applied too (harmless — ZB is a full replace), so the same handler runs
	// here and in the background loop.
	frames := t.Frames()
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				_ = t.Close()
				return nil, ErrConnectionClosed
			}
			if c.handle(f) {
				// ZB seen: hand the channel off to the background loop.
				c.wg.Add(1)
				go c.mergeLoop()
				return c, nil
			}
		case <-waitCtx.Done():
			_ = t.Close()
			return nil, waitCtx.Err()
		}
	}
}

// mergeLoop applies inbound frames until the transport closes Frames().
func (c *Client) mergeLoop() {
	defer c.wg.Done()
	for f := range c.t.Frames() {
		c.handle(f)
	}
}

// handle applies one inbound frame to the tree and reports whether it was a ZB
// snapshot (the signal Connect blocks on). PV/PS/PC become deltas; ZB replaces
// the whole tree (a fresh ZB also arrives after recall/reset); JM replies are
// routed to any pending request waiter.
func (c *Client) handle(f proto.Frame) (isZB bool) {
	switch f.Code {
	case proto.CodePV:
		if k, v, err := proto.UnmarshalPV(f.Payload); err == nil {
			c.tree.Apply(k, v)
		}
	case proto.CodePS:
		if k, v, err := proto.UnmarshalPS(f.Payload); err == nil {
			c.tree.Apply(k, v)
		}
	case proto.CodePC:
		if k, raw, err := proto.UnmarshalPC(f.Payload); err == nil {
			c.tree.Apply(k, raw)
		}
	case proto.CodeZB:
		if m, err := proto.ParseZB(f.Payload); err == nil {
			c.tree.LoadSnapshot(m)
			return true
		}
	case proto.CodeCK:
		// Real boards chunk the snapshot across CK frames; reassemble, then
		// decode the completed blob exactly as a ZB.
		chunk, err := proto.ParseCK(f.Payload)
		if err != nil {
			return false
		}
		blob, complete := c.ckAsm.Add(chunk)
		if !complete {
			return false
		}
		if m, err := proto.ParseZB(blob); err == nil {
			c.tree.LoadSnapshot(m)
			return true
		}
	case proto.CodeJM:
		c.handleJMReply(f.Payload)
	}
	return false
}

// handleJMReply parses a JM reply body and delivers list results to any waiter.
func (c *Client) handleJMReply(payload []byte) {
	body := payload
	if len(body) >= 4 {
		body = body[4:]
	}
	var reply struct {
		ID      string   `json:"id"`
		Presets []string `json:"presets"`
	}
	if err := json.Unmarshal(body, &reply); err != nil {
		return
	}
	if reply.ID != "Listpresets" {
		return
	}
	c.mu.Lock()
	waiters := c.listWaiters
	c.listWaiters = nil
	c.mu.Unlock()
	for _, w := range waiters {
		w <- reply.Presets // buffered size 1, never blocks
	}
}

// Close stops the background loop and closes the transport. It is idempotent.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		c.closeErr = c.t.Close()
		c.wg.Wait()
	})
	return c.closeErr
}

// Get returns the value at path, humanized through the schema: floats are
// divided by ReadScale and passed through the taper (dB/Hz/input); bools become
// Go bools; strings and raw chars pass through. Unknown keys return the raw
// stored value. The second result reports whether the path is present.
func (c *Client) Get(path string) (any, bool) {
	raw, ok := c.tree.Get(path)
	if !ok {
		return nil, false
	}
	spec, known := schema.Lookup(path)
	if !known {
		return raw, true
	}
	switch spec.Kind {
	case schema.KindBool:
		f, _ := toFloat64(raw)
		return f != 0, true
	case schema.KindFloat:
		f, okf := toFloat64(raw)
		if !okf {
			return raw, true
		}
		pos := f
		if spec.ReadScale != 0 {
			pos = f / spec.ReadScale
		}
		if spec.Taper != nil {
			return spec.Taper.FromWire(pos), true
		}
		return pos, true
	case schema.KindChars:
		return humanizeColor(raw), true
	default:
		return raw, true
	}
}

// humanizeColor normalizes a stored color to its RGBA byte form so the read is
// symmetric with the write (which parses an RGB(A) hex string into these bytes).
// A snapshot stores the color ABGR-packed into an integer — the little-endian
// read of the wire bytes R,G,B,A — so it is unpacked back to [R,G,B,A]; a live
// PC delta already carries the bytes and passes through. The CLI renders the
// bytes as hex (e.g. []byte{0x4e,0xd2,0xff,0xff} → "4ed2ffff").
func humanizeColor(raw any) any {
	if p, ok := toUint32(raw); ok {
		return []byte{byte(p), byte(p >> 8), byte(p >> 16), byte(p >> 24)}
	}
	return raw
}

// toUint32 coerces an integer color value (as ZB decodes it — int or int64) to
// a uint32. Non-integer kinds (e.g. a []byte delta) report false.
func toUint32(v any) (uint32, bool) {
	switch n := v.(type) {
	case int:
		return uint32(n), true
	case int64:
		return uint32(n), true
	case int32:
		return uint32(n), true
	default:
		return 0, false
	}
}

// GetRaw returns the raw wire value at path with no schema humanizing.
func (c *Client) GetRaw(path string) (any, bool) {
	return c.tree.Get(path)
}

// Snapshot returns a deep copy of the whole tree as raw wire values.
func (c *Client) Snapshot() map[string]any {
	return c.tree.Snapshot()
}

// Setting is one path/value write for SetMany. Order is preserved so callers
// that depend on write order (a compiled board config) keep it.
type Setting struct {
	Path  string
	Value any
}

// Set writes v to path and holds a commit barrier before returning, so a caller
// that closes right after gets a committed write (the board's per-connection
// reader consumes the frame before the connection drops). The barrier duration
// is the Client's commitDelay; a Client built WithCommitDelay(0) writes without
// holding.
//
// Set consults the schema to pick the wire message and encoding: KindBool → PV
// 1/0, KindFloat → PV via the taper (ToWire), KindString → PS, KindChars → PC.
// Unknown keys fall back to a best-guess encode from v's Go type
// (bool/string/float/[]byte).
//
// Float writes send the 0..1 wire position only; ReadScale is a read-side
// inflation and is never applied on write (the board wants 0..1 — a raw 74.6
// pins the fader to the top). SetFaderDB(-6) therefore sends 0.746, not 74.6.
func (c *Client) Set(ctx context.Context, path string, v any) error {
	if err := c.writeValue(ctx, path, v); err != nil {
		return err
	}
	return c.commit(ctx)
}

// SetMany writes every setting over this one held-open connection in order, then
// holds a single commit barrier — one connection and one barrier for the whole
// burst, instead of a connect-and-commit per write. This is the reliable path
// for configuring many values at once (a channel strip, a DCA's membership, a
// whole board): rapid connect-per-write reconnects drop writes silently.
func (c *Client) SetMany(ctx context.Context, settings []Setting) error {
	for _, s := range settings {
		if err := c.writeValue(ctx, s.Path, s.Value); err != nil {
			return err
		}
	}
	return c.commit(ctx)
}

// commit holds the connection open for commitDelay so the board consumes the
// preceding writes before the caller closes. A zero commitDelay is a no-op (the
// caller commits itself). The hold is bounded by ctx.
func (c *Client) commit(ctx context.Context) error {
	if c.commitDelay <= 0 {
		return nil
	}
	return commitSleep(ctx, c.commitDelay)
}

// writeValue encodes v for path and sends it, with no commit barrier. It is the
// shared write primitive under Set (single write + barrier) and SetMany (many
// writes + one barrier).
func (c *Client) writeValue(ctx context.Context, path string, v any) error {
	spec, known := schema.Lookup(path)
	if !known {
		return c.setUnknown(ctx, path, v)
	}
	switch spec.Kind {
	case schema.KindBool:
		return c.sendPV(ctx, path, boolToFloat(truthy(v)))
	case schema.KindFloat:
		human, ok := toFloat64(v)
		if !ok {
			return fmt.Errorf("ucmix: %s expects a number, got %T", path, v)
		}
		wire := human
		if spec.Taper != nil {
			w, err := spec.Taper.ToWire(human)
			if err != nil {
				return fmt.Errorf("ucmix: %s: %w", path, err)
			}
			wire = w
		}
		return c.sendPV(ctx, path, float32(wire))
	case schema.KindString:
		s, ok := toStringVal(v)
		if !ok {
			return fmt.Errorf("ucmix: %s expects a string, got %T", path, v)
		}
		return c.sendPS(ctx, path, s)
	case schema.KindChars:
		raw, err := toCharsVal(v)
		if err != nil {
			return fmt.Errorf("ucmix: %s: %w", path, err)
		}
		return c.sendPC(ctx, path, raw)
	default:
		return fmt.Errorf("ucmix: %s has unhandled kind %d", path, spec.Kind)
	}
}

// setUnknown encodes an unknown-key write from v's Go type.
func (c *Client) setUnknown(ctx context.Context, path string, v any) error {
	switch vv := v.(type) {
	case bool:
		return c.sendPV(ctx, path, boolToFloat(vv))
	case string:
		return c.sendPS(ctx, path, vv)
	case []byte:
		return c.sendPC(ctx, path, vv)
	default:
		if f, ok := toFloat64(v); ok {
			return c.sendPV(ctx, path, float32(f))
		}
		return fmt.Errorf("ucmix: cannot encode %T for unknown key %s", v, path)
	}
}

func (c *Client) sendPV(ctx context.Context, key string, val float32) error {
	return c.t.Send(ctx, proto.Frame{Code: proto.CodePV, Payload: proto.MarshalPV(key, val)})
}

func (c *Client) sendPS(ctx context.Context, key, val string) error {
	return c.t.Send(ctx, proto.Frame{Code: proto.CodePS, Payload: proto.MarshalPS(key, val)})
}

func (c *Client) sendPC(ctx context.Context, key string, raw []byte) error {
	return c.t.Send(ctx, proto.Frame{Code: proto.CodePC, Payload: proto.MarshalPC(key, raw)})
}

func (c *Client) sendJM(ctx context.Context, v any) error {
	return c.t.Send(ctx, proto.Frame{Code: proto.CodeJM, Payload: proto.MarshalJM(v)})
}

// --- value coercion helpers ---

func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}

func truthy(v any) bool {
	switch b := v.(type) {
	case bool:
		return b
	default:
		if f, ok := toFloat64(v); ok {
			return f != 0
		}
		return false
	}
}

func boolToFloat(b bool) float32 {
	if b {
		return 1.0
	}
	return 0.0
}

func toStringVal(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

// toCharsVal coerces a KindChars value: []byte passes through verbatim; a string
// is decoded as hex (e.g. a color "4ed2ff"). Anything else is an error.
func toCharsVal(v any) ([]byte, error) {
	switch b := v.(type) {
	case []byte:
		return b, nil
	case string:
		raw, err := hex.DecodeString(b)
		if err != nil {
			return nil, fmt.Errorf("chars value %q is not hex: %w", b, err)
		}
		return raw, nil
	default:
		return nil, fmt.Errorf("chars value must be []byte or hex string, got %T", v)
	}
}
