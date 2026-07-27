package daw

import (
	"bytes"
	"context"
	"errors"
	"net"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/steveclarke/ucmix/internal/proto"
	"github.com/steveclarke/ucmix/internal/transport"
)

type fakeTransport struct {
	mu      sync.Mutex
	sent    []proto.Frame
	in      chan proto.Frame
	done    chan struct{}
	onSend  func(proto.Frame)
	closeMu sync.Once
}

func newFakeTransport() *fakeTransport {
	return &fakeTransport{
		in:   make(chan proto.Frame, 32),
		done: make(chan struct{}),
	}
}

func (f *fakeTransport) Send(ctx context.Context, frame proto.Frame) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-f.done:
		return transport.ErrClosed
	default:
	}
	f.mu.Lock()
	f.sent = append(f.sent, frame)
	hook := f.onSend
	f.mu.Unlock()
	if hook != nil {
		hook(frame)
	}
	return nil
}

func (f *fakeTransport) Frames() <-chan proto.Frame { return f.in }

func (f *fakeTransport) Close() error {
	f.closeMu.Do(func() {
		close(f.done)
		close(f.in)
	})
	return nil
}

func (f *fakeTransport) deliver(frame proto.Frame) { f.in <- frame }

func (f *fakeTransport) sentFrames() []proto.Frame {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]proto.Frame(nil), f.sent...)
}

func (f *fakeTransport) closed() bool {
	select {
	case <-f.done:
		return true
	default:
		return false
	}
}

type fakeMeteringConn struct {
	local   *net.UDPAddr
	done    chan struct{}
	closeMu sync.Once
}

func newFakeMeteringConn(port int) *fakeMeteringConn {
	return &fakeMeteringConn{
		local: &net.UDPAddr{IP: net.IPv4zero, Port: port},
		done:  make(chan struct{}),
	}
}

func (f *fakeMeteringConn) ReadFrom([]byte) (int, net.Addr, error) {
	return 0, nil, net.ErrClosed
}

func (f *fakeMeteringConn) WriteTo(p []byte, _ net.Addr) (int, error) {
	return len(p), nil
}

func (f *fakeMeteringConn) Close() error {
	f.closeMu.Do(func() { close(f.done) })
	return nil
}

func (f *fakeMeteringConn) LocalAddr() net.Addr              { return f.local }
func (f *fakeMeteringConn) SetDeadline(time.Time) error      { return nil }
func (f *fakeMeteringConn) SetReadDeadline(time.Time) error  { return nil }
func (f *fakeMeteringConn) SetWriteDeadline(time.Time) error { return nil }

func (f *fakeMeteringConn) closed() bool {
	select {
	case <-f.done:
		return true
	default:
		return false
	}
}

func useClientFakes(
	t *testing.T,
	tcp *fakeTransport,
	udp *fakeMeteringConn,
) *string {
	t.Helper()
	oldDial, oldListen := dialTransport, listenMetering
	var dialed string
	dialTransport = func(
		_ context.Context,
		address string,
		_ transport.Options,
	) (transport.Transport, error) {
		dialed = address
		return tcp, nil
	}
	listenMetering = func() (net.PacketConn, error) { return udp, nil }
	t.Cleanup(func() {
		dialTransport, listenMetering = oldDial, oldListen
	})
	return &dialed
}

func TestConnectPerformsCapturedHandshake(t *testing.T) {
	endpoint := Endpoint{
		IP: "192.0.2.20", ControlPort: 49601, ServerID: 0x6b,
	}
	tcp := newFakeTransport()
	udp := newFakeMeteringConn(58395)
	dialed := useClientFakes(t, tcp, udp)
	tcp.onSend = func(frame proto.Frame) {
		if frame.Code != proto.CodeJM {
			return
		}
		msg, _ := proto.ParseJM(frame.Payload)
		if msg.ID == "QueryUserLicense" {
			tcp.deliver(proto.Frame{
				Code: proto.CodeJM,
				Payload: proto.MarshalJM(map[string]string{
					"id": "UserLicenseReply",
				}),
			})
		}
	}

	client, err := Connect(
		context.Background(),
		endpoint,
		ClientIdentifier("00000000-0000-4000-8000-000000000000"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if *dialed != "192.0.2.20:49601" {
		t.Fatalf("dial address = %q", *dialed)
	}

	sent := tcp.sentFrames()
	if len(sent) != 3 {
		t.Fatalf("sent %d frames, want 3", len(sent))
	}
	if sent[0].Code != proto.CodeJM ||
		sent[0].ConnID != [4]byte{0x68, 0, 0x6b, 0} {
		t.Fatalf("license frame = %+v", sent[0])
	}
	msg, err := proto.ParseJM(sent[0].Payload)
	if err != nil || msg.ID != "QueryUserLicense" {
		t.Fatalf("license message = %+v, %v", msg, err)
	}
	if sent[1].Code != proto.CodeUM ||
		sent[1].ConnID != [4]byte{0, 0, 0x6b, 0} ||
		!bytes.Equal(sent[1].Payload, []byte{0x1b, 0xe4}) {
		t.Fatalf("UM frame = %+v", sent[1])
	}
	msg, err = proto.ParseJM(sent[2].Payload)
	if err != nil || msg.ID != "Subscribe" ||
		sent[2].ConnID != [4]byte{0x68, 0, 0x6b, 0} {
		t.Fatalf("subscribe frame = %+v, message = %+v, err = %v", sent[2], msg, err)
	}

	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if !tcp.closed() || !udp.closed() {
		t.Fatal("Close did not close both sockets")
	}
}

func TestConnectClosesSocketsWhenLicenseWaitIsCanceled(t *testing.T) {
	tcp := newFakeTransport()
	udp := newFakeMeteringConn(58395)
	useClientFakes(t, tcp, udp)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Connect(ctx, Endpoint{
		IP: "192.0.2.20", ControlPort: 49601, ServerID: 0x6b,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Connect = %v, want context.Canceled", err)
	}
	if !tcp.closed() || !udp.closed() {
		t.Fatal("failed Connect did not close both sockets")
	}
}

func TestNewIdentifierIsUUIDv4(t *testing.T) {
	got := newIdentifier()
	pattern := regexp.MustCompile(
		`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`,
	)
	if !pattern.MatchString(got) {
		t.Fatalf("newIdentifier = %q", got)
	}
}

func actionClient(t *testing.T) (*Client, *fakeTransport) {
	t.Helper()
	tcp := newFakeTransport()
	udp := newFakeMeteringConn(58395)
	client := &Client{
		endpoint: Endpoint{ServerID: 0x6b},
		t:        tcp,
		metering: udp,
	}
	t.Cleanup(func() { _ = client.Close() })
	return client, tcp
}

func TestTriggerPlaySendsCapturedSequenceAndWaitsForState(t *testing.T) {
	client, tcp := actionClient(t)
	tcp.onSend = confirmAction(tcp, "transport/start")

	if err := client.Trigger(context.Background(), Play); err != nil {
		t.Fatal(err)
	}
	assertActionFrames(t, tcp.sentFrames(), "transport/start")
}

func TestTriggerStopUsesDistinctPath(t *testing.T) {
	client, tcp := actionClient(t)
	tcp.onSend = confirmAction(tcp, "transport/stop")

	if err := client.Trigger(context.Background(), Stop); err != nil {
		t.Fatal(err)
	}
	assertActionFrames(t, tcp.sentFrames(), "transport/stop")
}

func confirmAction(tcp *fakeTransport, wantKey string) func(proto.Frame) {
	return func(frame proto.Frame) {
		if frame.Code != proto.CodePE {
			return
		}
		key, state, _ := proto.UnmarshalPE(frame.Payload)
		if key == wantKey && state == 0 {
			tcp.deliver(proto.Frame{
				Code:    proto.CodePV,
				Payload: proto.MarshalPV(wantKey, 1),
			})
		}
	}
}

func assertActionFrames(t *testing.T, frames []proto.Frame, key string) {
	t.Helper()
	if len(frames) != 3 {
		t.Fatalf("sent %d frames, want 3", len(frames))
	}
	wantCodes := [][2]byte{proto.CodePE, proto.CodePV, proto.CodePE}
	for i, want := range wantCodes {
		if frames[i].Code != want ||
			frames[i].ConnID != [4]byte{0x68, 0, 0x6b, 0} {
			t.Fatalf("frame %d = %+v", i, frames[i])
		}
	}
	if gotKey, state, err := proto.UnmarshalPE(frames[0].Payload); err != nil ||
		gotKey != key || state != 2 {
		t.Fatalf("press = %q, %d, %v", gotKey, state, err)
	}
	if gotKey, value, err := proto.UnmarshalPV(frames[1].Payload); err != nil ||
		gotKey != key || value != 1 {
		t.Fatalf("value = %q, %f, %v", gotKey, value, err)
	}
	if gotKey, state, err := proto.UnmarshalPE(frames[2].Payload); err != nil ||
		gotKey != key || state != 0 {
		t.Fatalf("release = %q, %d, %v", gotKey, state, err)
	}
}

func TestTriggerRejectsUnknownAction(t *testing.T) {
	client, tcp := actionClient(t)
	if err := client.Trigger(context.Background(), Action("record")); err == nil {
		t.Fatal("Trigger(record) succeeded")
	}
	if got := len(tcp.sentFrames()); got != 0 {
		t.Fatalf("Trigger(record) sent %d frames", got)
	}
}

func TestTriggerRequiresMatchingConfirmation(t *testing.T) {
	client, tcp := actionClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	tcp.onSend = func(frame proto.Frame) {
		if frame.Code != proto.CodePE {
			return
		}
		key, state, _ := proto.UnmarshalPE(frame.Payload)
		if key == "transport/start" && state == 0 {
			tcp.deliver(proto.Frame{
				Code:    proto.CodePV,
				Payload: proto.MarshalPV("transport/start", 0),
			})
			tcp.deliver(proto.Frame{
				Code:    proto.CodePV,
				Payload: proto.MarshalPV("unrelated", 1),
			})
			cancel()
		}
	}
	if err := client.Trigger(ctx, Play); !errors.Is(err, context.Canceled) {
		t.Fatalf("Trigger = %v, want context.Canceled", err)
	}
}

func TestTriggerReportsClosedConnection(t *testing.T) {
	client, tcp := actionClient(t)
	tcp.onSend = func(frame proto.Frame) {
		if frame.Code != proto.CodePE {
			return
		}
		_, state, _ := proto.UnmarshalPE(frame.Payload)
		if state == 0 {
			_ = tcp.Close()
		}
	}
	if err := client.Trigger(context.Background(), Play); !errors.Is(err, ErrConnectionClosed) {
		t.Fatalf("Trigger = %v, want ErrConnectionClosed", err)
	}
}
