package ucmix

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/steveclarke/ucmix/internal/color"
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
	fdAsm proto.ChunkAssembler // reassembles an FD-chunked preset list; used only from the read goroutine

	wg        sync.WaitGroup
	closeOnce sync.Once
	closeErr  error

	mu          sync.Mutex                // guards listWaiters, jmSubs and nextReqID
	listWaiters []chan []proto.PresetFile // registered preset-list reply waiters
	jmSubs      []chan proto.JMMessage    // registered inbound-JM subscribers
	nextReqID   uint16                    // FR request id, incremented per list request
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
// the whole tree (a fresh ZB also arrives after recall/reset); FD chunks
// reassemble into a preset-list reply routed to any pending list waiter; JM
// carries the board's command acknowledgments, fanned out to any subscriber.
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
			c.tree.Apply(k, canonicalChars(k, raw))
		}
	case proto.CodeZB:
		if m, err := proto.ParseZB(f.Payload); err == nil {
			c.tree.LoadSnapshot(canonicalSnapshot(m))
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
			c.tree.LoadSnapshot(canonicalSnapshot(m))
			return true
		}
	case proto.CodeFD:
		// The board answers a preset-list request (FR) with FD chunks;
		// reassemble, then parse the completed JSON body and deliver it to the
		// pending list waiter. fdAsm assumes one outstanding FR per connection —
		// the CLI dials a fresh client per list, so only one reply is ever in
		// flight. Concurrent lists on one Client would interleave and corrupt it.
		chunk, err := proto.ParseFD(f.Payload)
		if err != nil {
			return false
		}
		body, complete := c.fdAsm.Add(chunk.Chunk)
		if !complete {
			return false
		}
		if len(body) == 0 {
			// An empty FD is a bare command acknowledgment, not a listing — a
			// rename answers this way. Delivering it would hand an empty list to
			// whichever list request is in flight.
			return false
		}
		files, err := proto.ParsePresetList(body)
		if err != nil {
			return false
		}
		c.deliverList(files)
	case proto.CodeJM:
		// The board acknowledges commands with JM frames (e.g. StoredPreset
		// after a preset write). Fan every one out to subscribers so a caller
		// can block until its own command is confirmed.
		if m, err := proto.ParseJM(f.Payload); err == nil {
			c.deliverJM(m)
		}
	}
	return false
}

// deliverJM fans an inbound JM message out to every subscriber. Sends are
// non-blocking: a subscriber whose buffer is full drops the message rather than
// stalling the merge loop.
func (c *Client) deliverJM(m proto.JMMessage) {
	c.mu.Lock()
	subs := make([]chan proto.JMMessage, len(c.jmSubs))
	copy(subs, c.jmSubs)
	c.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- m:
		default:
		}
	}
}

// subscribeJM registers a buffered subscriber for inbound JM messages and
// returns it with a cancel that unregisters it. Callers must cancel.
func (c *Client) subscribeJM(buf int) (<-chan proto.JMMessage, func()) {
	ch := make(chan proto.JMMessage, buf)
	c.mu.Lock()
	c.jmSubs = append(c.jmSubs, ch)
	c.mu.Unlock()
	return ch, func() {
		c.mu.Lock()
		for i, s := range c.jmSubs {
			if s == ch {
				c.jmSubs = append(c.jmSubs[:i], c.jmSubs[i+1:]...)
				break
			}
		}
		c.mu.Unlock()
	}
}

// deliverList hands a completed preset list to every pending waiter and clears
// them.
func (c *Client) deliverList(files []proto.PresetFile) {
	c.mu.Lock()
	waiters := c.listWaiters
	c.listWaiters = nil
	c.mu.Unlock()
	for _, w := range waiters {
		w <- files // buffered size 1, never blocks
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
// Go bools; strings and colors pass through (a color is canonicalized on the way
// into the tree, so it is already the canonical RGBA hex string). Unknown keys
// return the raw stored value. The second result reports whether the path is
// present.
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
		return raw, true
	default:
		return raw, true
	}
}

// canonicalSnapshot rewrites every KindChars value in a freshly parsed snapshot
// to its canonical form before it reaches the tree, so the tree holds exactly
// one representation of a color no matter how it arrived. Only integer and byte
// values are candidates (a snapshot is overwhelmingly floats and strings), which
// keeps the schema lookup off the hot path for the rest of the tree.
func canonicalSnapshot(m map[string]any) map[string]any {
	for k, v := range m {
		switch v.(type) {
		case int, int32, int64, uint32, uint64, []byte:
			m[k] = canonicalChars(k, v)
		}
	}
	return m
}

// canonicalChars normalizes a value bound for a KindChars key to the canonical
// color rendering — 8 lowercase RGBA hex digits. A board reports a color as an
// ABGR-packed integer in a snapshot and as raw RGBA bytes in a live PC delta;
// both land in the tree as the same string. Values on other keys, and chars
// values that are not colors, pass through untouched.
func canonicalChars(path string, v any) any {
	spec, known := schema.Lookup(path)
	if !known || spec.Kind != schema.KindChars {
		return v
	}
	if h, ok := color.Canonical(v); ok {
		return h
	}
	return v
}

// GetRaw returns the stored wire value at path with no schema humanizing.
// Colors are the one value normalized on the way in: the tree holds the
// canonical RGBA hex string rather than whichever of the board's two encodings
// delivered it, so raw and humanized reads of a color agree.
func (c *Client) GetRaw(path string) (any, bool) {
	return c.tree.Get(path)
}

// Snapshot returns a deep copy of the whole tree as wire values (colors as
// canonical RGBA hex — see [Client.GetRaw]).
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

// toCharsVal coerces a KindChars value to its wire bytes: []byte passes through
// verbatim; a string is parsed as a color hex, gaining the opaque alpha when it
// is 6 digits (e.g. "4ed2ff" → 4e d2 ff ff). Anything else is an error.
func toCharsVal(v any) ([]byte, error) {
	switch b := v.(type) {
	case []byte:
		return b, nil
	case string:
		raw, err := color.Parse(b)
		if err != nil {
			return nil, fmt.Errorf("chars value: %w", err)
		}
		return raw, nil
	default:
		return nil, fmt.Errorf("chars value must be []byte or hex string, got %T", v)
	}
}
