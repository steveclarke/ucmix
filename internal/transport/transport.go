// Package transport is the TCP transport for UCNET: it dials a mixer, runs a
// background read loop that delivers whole frames, paces outbound frames to keep
// the board's TCP connection alive under bursts, and sends periodic keep-alives.
//
// It is the client's only dependency on networking. Framing is delegated to
// internal/proto; this package adds the I/O, pacing, and keep-alive on top.
//
// Two package-level func variables are the test seams (per outport convention):
// dial (default net.DialTimeout) is swapped for a net.Pipe-backed func so
// framing, pacing, and keep-alive run over real bytes with no network; sleep
// (default time.Sleep) lets tests control pacing and keep-alive timing.
package transport

import (
	"bufio"
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/steveclarke/ucmix/internal/proto"
)

// Test seams. Never reassigned outside tests.
var (
	dial  = net.DialTimeout
	sleep = time.Sleep
)

// Defaults applied to zero-valued Options fields.
const (
	defaultPaceEvery = 40
	defaultPaceDelay = 25 * time.Millisecond
	defaultKeepAlive = 1 * time.Second
)

// ErrClosed is returned by Send after the transport has been closed.
var ErrClosed = errors.New("transport: closed")

// Transport delivers whole frames. It is the client's only dependency on
// networking.
type Transport interface {
	// Send encodes and writes f. It is paced (token-bucket over frame count)
	// and safe for concurrent callers. It returns ErrClosed after Close.
	Send(ctx context.Context, f proto.Frame) error
	// Frames delivers inbound frames. It is closed on connection loss.
	Frames() <-chan proto.Frame
	// Close stops the read loop and keep-alive and closes the connection.
	Close() error
}

// Options configure a Transport. Zero-valued fields take package defaults;
// DialTimeout of zero means no dial timeout.
type Options struct {
	PaceEvery   int           // frames per pacing window (default 40)
	PaceDelay   time.Duration // sleep per window (default 25ms)
	KeepAlive   time.Duration // keep-alive interval (default 1s)
	DialTimeout time.Duration // dial timeout (0 = none)
}

func (o Options) withDefaults() Options {
	if o.PaceEvery <= 0 {
		o.PaceEvery = defaultPaceEvery
	}
	if o.PaceDelay <= 0 {
		o.PaceDelay = defaultPaceDelay
	}
	if o.KeepAlive <= 0 {
		o.KeepAlive = defaultKeepAlive
	}
	return o
}

type transport struct {
	conn   net.Conn
	opts   Options
	frames chan proto.Frame
	done   chan struct{} // closed by Close to stop the keep-alive loop
	wg     sync.WaitGroup

	mu        sync.Mutex // guards conn.Write + sendCount (serializes writers)
	sendCount int

	closeOnce sync.Once
	closed    atomic.Bool // set before conn.Close so Send fails fast after Close
}

// Dial connects to addr via the dial seam and returns a running Transport: a
// background read loop delivers frames on Frames(), and a keep-alive loop sends
// a KA frame every KeepAlive. Reconnect is not automatic in v1 — Frames()
// closing signals connection loss.
func Dial(ctx context.Context, addr string, opts Options) (Transport, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	opts = opts.withDefaults()
	conn, err := dial("tcp", addr, opts.DialTimeout)
	if err != nil {
		return nil, err
	}
	t := &transport{
		conn:   conn,
		opts:   opts,
		frames: make(chan proto.Frame),
		done:   make(chan struct{}),
	}
	t.wg.Add(2)
	go t.readLoop()
	go t.keepAliveLoop(ctx)
	return t, nil
}

// wait blocks until the background loops have exited. Test-only helper.
func (t *transport) wait() { t.wg.Wait() }

// readLoop reads frames until the connection errors (peer close, Close, or a
// framing error) and delivers them on frames. It owns frames' close: the defer
// closes it exactly once, whether the loop exits via a read error or via done.
func (t *transport) readLoop() {
	defer t.wg.Done()
	defer close(t.frames)
	r := bufio.NewReader(t.conn)
	for {
		f, err := proto.ReadFrame(r)
		if err != nil {
			return
		}
		select {
		case t.frames <- f:
		case <-t.done:
			return
		}
	}
}

// keepAliveLoop sends a KA frame every KeepAlive. It is driven by the sleep seam
// rather than a time.Ticker so tests control cadence deterministically. Sleeping
// first delays the first KA by one interval (correct) and parks the loop before
// it can send, which keeps it out of the way of pacing tests.
func (t *transport) keepAliveLoop(ctx context.Context) {
	defer t.wg.Done()
	ka := proto.Frame{Code: proto.CodeKA}
	for {
		sleep(t.opts.KeepAlive)
		select {
		case <-t.done:
			return
		default:
		}
		if err := t.Send(ctx, ka); err != nil {
			return
		}
	}
}

func (t *transport) Frames() <-chan proto.Frame { return t.frames }

// Send encodes f and writes it to the connection, pacing after every PaceEvery
// frames. The write and counter are mutex-guarded so concurrent callers are
// safe; the pace sleep runs after unlocking so it does not hold off other work.
func (t *transport) Send(ctx context.Context, f proto.Frame) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if t.closed.Load() {
		return ErrClosed
	}

	t.mu.Lock()
	if t.closed.Load() {
		t.mu.Unlock()
		return ErrClosed
	}
	_, err := t.conn.Write(proto.Encode(f))
	if err != nil {
		t.mu.Unlock()
		return err
	}
	t.sendCount++
	shouldPace := t.sendCount%t.opts.PaceEvery == 0
	t.mu.Unlock()

	if shouldPace {
		sleep(t.opts.PaceDelay)
	}
	return nil
}

// Close stops the keep-alive loop and closes the connection. It does not take
// the write mutex: closing the conn unblocks any Send parked in Write (and
// unblocks ReadFrame, so the read loop exits and closes Frames()). Close is
// idempotent.
func (t *transport) Close() error {
	var err error
	t.closeOnce.Do(func() {
		t.closed.Store(true)
		close(t.done)
		err = t.conn.Close()
	})
	return err
}
