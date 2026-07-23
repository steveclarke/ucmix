package transport

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/steveclarke/ucmix/internal/proto"
)

// fakeSleep is the sleep-seam stub. It distinguishes pacing sleeps from
// keep-alive sleeps by duration: a PaceDelay sleep is recorded and returns
// immediately; a KeepAlive sleep parks until the test releases one cycle via
// token (gating KA cadence) or until stop is closed at cleanup (releasing a
// parked KA goroutine so it can exit). Distinct ka/pace durations per test keep
// the two paths from colliding.
type fakeSleep struct {
	ka    time.Duration
	pace  time.Duration
	token chan struct{}
	stop  chan struct{}

	mu        sync.Mutex
	paceCalls []time.Duration
}

func (s *fakeSleep) sleep(d time.Duration) {
	switch d {
	case s.ka:
		select {
		case <-s.token:
		case <-s.stop:
		}
	case s.pace:
		s.mu.Lock()
		s.paceCalls = append(s.paceCalls, d)
		s.mu.Unlock()
	}
}

func (s *fakeSleep) paces() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Duration(nil), s.paceCalls...)
}

// install wires the dial and sleep seams to the test's pipe end and stub, and
// restores them (plus releases any parked KA) at cleanup.
func install(t *testing.T, s *fakeSleep, clientEnd net.Conn) {
	t.Helper()
	if s.token == nil {
		s.token = make(chan struct{})
	}
	if s.stop == nil {
		s.stop = make(chan struct{})
	}
	origDial, origSleep := dial, sleep
	dial = func(network, address string, timeout time.Duration) (net.Conn, error) {
		return clientEnd, nil
	}
	sleep = s.sleep
	t.Cleanup(func() {
		dial, sleep = origDial, origSleep
	})
}

// stopAndWait closes the transport, releases the parked keep-alive goroutine,
// and waits for both background loops to exit — so the deferred seam restore in
// install runs only after nothing reads the sleep/dial vars. Deferred in each
// test that dials, ahead of install's t.Cleanup (defers run before Cleanups).
func stopAndWait(t *testing.T, tr Transport, fs *fakeSleep) {
	t.Helper()
	tr.Close()
	close(fs.stop)
	tr.(*transport).wait()
}

func TestFrameReassemblyAcrossSplitReads(t *testing.T) {
	clientEnd, board := net.Pipe()
	// KA far larger than anything the test cares about; parked for the test.
	fs := &fakeSleep{ka: time.Hour, pace: 25 * time.Millisecond}
	install(t, fs, clientEnd)

	tr, err := Dial(context.Background(), "board:53000", Options{KeepAlive: time.Hour})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer stopAndWait(t, tr, fs)

	want := proto.Frame{Code: proto.CodeZB, ConnID: proto.DefaultConnID, Payload: []byte{0x01, 0x02, 0x03}}
	wire := proto.Encode(want)

	// Write the frame in two chunks, splitting mid-length-field, so ReadFrame
	// must reassemble across reads.
	go func() {
		board.Write(wire[:5])
		board.Write(wire[5:])
	}()

	select {
	case got := <-tr.Frames():
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("frame mismatch:\n got %+v\nwant %+v", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for reassembled frame")
	}
}

func TestPacingSleepsPerWindow(t *testing.T) {
	clientEnd, board := net.Pipe()
	fs := &fakeSleep{ka: time.Hour, pace: 5 * time.Millisecond}
	install(t, fs, clientEnd)

	// Drain the board side so Send's Write (unbuffered pipe) never blocks.
	go io.Copy(io.Discard, board)

	tr, err := Dial(context.Background(), "board:53000", Options{PaceEvery: 3, PaceDelay: 5 * time.Millisecond, KeepAlive: time.Hour})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer stopAndWait(t, tr, fs)

	const n = 7 // windows close at frame 3 and 6 -> 2 pace sleeps
	for i := 0; i < n; i++ {
		if err := tr.Send(context.Background(), proto.Frame{Code: proto.CodePV}); err != nil {
			t.Fatalf("Send %d: %v", i, err)
		}
	}

	paces := fs.paces()
	if len(paces) != n/3 {
		t.Fatalf("pace sleeps = %d, want %d", len(paces), n/3)
	}
	for i, d := range paces {
		if d != 5*time.Millisecond {
			t.Fatalf("pace sleep %d = %v, want 5ms", i, d)
		}
	}
}

func TestKeepAliveCadence(t *testing.T) {
	clientEnd, board := net.Pipe()
	// PaceEvery high so KA frames never trigger a pace sleep and muddy timing.
	fs := &fakeSleep{ka: 1 * time.Second, pace: 25 * time.Millisecond}
	install(t, fs, clientEnd)

	tr, err := Dial(context.Background(), "board:53000", Options{KeepAlive: 1 * time.Second, PaceEvery: 1000})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer stopAndWait(t, tr, fs)

	br := bufio.NewReader(board)

	// Each released KA cycle must produce exactly one KA frame on the wire.
	for i := 0; i < 3; i++ {
		select {
		case fs.token <- struct{}{}:
		case <-time.After(2 * time.Second):
			t.Fatalf("KA loop not waiting on the sleep seam (cycle %d)", i)
		}
		f, err := proto.ReadFrame(br)
		if err != nil {
			t.Fatalf("reading KA frame %d: %v", i, err)
		}
		if f.Code != proto.CodeKA {
			t.Fatalf("frame %d code = %q, want KA", i, f.Code)
		}
		if len(f.Payload) != 0 {
			t.Fatalf("KA frame %d payload = % x, want empty", i, f.Payload)
		}
	}
}

func TestFramesClosesOnPeerClose(t *testing.T) {
	clientEnd, board := net.Pipe()
	fs := &fakeSleep{ka: time.Hour, pace: 25 * time.Millisecond}
	install(t, fs, clientEnd)

	tr, err := Dial(context.Background(), "board:53000", Options{KeepAlive: time.Hour})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer stopAndWait(t, tr, fs)

	go func() {
		board.Write(proto.Encode(proto.Frame{Code: proto.CodeZB, Payload: []byte{0x09}}))
		board.Close()
	}()

	if _, ok := <-tr.Frames(); !ok {
		t.Fatal("expected one frame before close")
	}
	select {
	case _, ok := <-tr.Frames():
		if ok {
			t.Fatal("Frames() delivered a frame after peer close; want closed channel")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Frames() did not close after peer closed the connection")
	}
}

func TestSendAfterCloseErrors(t *testing.T) {
	clientEnd, board := net.Pipe()
	fs := &fakeSleep{ka: time.Hour, pace: 25 * time.Millisecond}
	install(t, fs, clientEnd)
	go io.Copy(io.Discard, board)

	tr, err := Dial(context.Background(), "board:53000", Options{KeepAlive: time.Hour})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer stopAndWait(t, tr, fs)

	if err := tr.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := tr.Close(); err != nil {
		t.Fatalf("second Close should be a no-op, got: %v", err)
	}

	if err := tr.Send(context.Background(), proto.Frame{Code: proto.CodePV}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Send after Close = %v, want ErrClosed", err)
	}

	// Close stops the read loop, which closes Frames().
	select {
	case _, ok := <-tr.Frames():
		if ok {
			t.Fatal("Frames() should be closed after Close")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Frames() did not close after Close")
	}
}

func TestConcurrentSendIsSafe(t *testing.T) {
	clientEnd, board := net.Pipe()
	fs := &fakeSleep{ka: time.Hour, pace: 5 * time.Millisecond}
	install(t, fs, clientEnd)
	go io.Copy(io.Discard, board)

	tr, err := Dial(context.Background(), "board:53000", Options{PaceEvery: 4, PaceDelay: 5 * time.Millisecond, KeepAlive: time.Hour})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer stopAndWait(t, tr, fs)

	const goroutines, each = 8, 25
	errc := make(chan error, goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			for i := 0; i < each; i++ {
				if err := tr.Send(context.Background(), proto.Frame{Code: proto.CodePV}); err != nil {
					errc <- err
					return
				}
			}
			errc <- nil
		}()
	}
	for g := 0; g < goroutines; g++ {
		if err := <-errc; err != nil {
			t.Fatalf("concurrent Send: %v", err)
		}
	}
}

func TestDialContextCancelled(t *testing.T) {
	clientEnd, _ := net.Pipe()
	install(t, &fakeSleep{ka: time.Hour, pace: 25 * time.Millisecond}, clientEnd)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Dial(ctx, "board:53000", Options{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Dial with cancelled ctx = %v, want context.Canceled", err)
	}
}
