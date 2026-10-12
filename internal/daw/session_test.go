package daw

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/steveclarke/ucmix/internal/proto"
	"github.com/steveclarke/ucmix/internal/transport"
)

// fakeStudio is an in-memory Studio Pro: it answers the handshake and echoes
// PV writes the way the real one does.
type fakeStudio struct {
	mu      sync.Mutex
	sent    []proto.Frame
	batches [][]proto.Frame
	frames  chan proto.Frame
	closed  bool
	echo    bool
	loop    bool
	// echoStrings echoes PS writes.
	echoStrings bool
	// loopRange is whether the song has a loop range (Studio Pro refuses loop without one).
	loopRange bool
	// ignoreStart drops Play presses unanswered.
	ignoreStart bool
	// stubbornStops is how many Stop presses to answer without stopping.
	stubbornStops int
	snapshot      []proto.Frame
	// onCommand answers an InvokeCommand with update frames.
	onCommand map[string][]proto.Frame
	// onConfirm answers a ConfirmDialog.
	onConfirm []proto.Frame
}

func (f *fakeStudio) Send(_ context.Context, frame proto.Frame) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return transport.ErrClosed
	}
	if frame.Code != proto.CodeKA {
		f.sent = append(f.sent, frame)
	}
	switch frame.Code {
	case proto.CodeJM:
		msg, _ := proto.ParseJM(frame.Payload)
		switch msg.ID {
		case "QueryUserLicense":
			f.frames <- jmFrame(`{"id": "UserLicenseReply"}`)
		case "Subscribe":
			for _, s := range f.snapshot {
				f.frames <- s
			}
			f.frames <- jmFrame(`{"id": "SubscriptionReply"}`)
		case "ConfirmDialog":
			for _, out := range f.onConfirm {
				f.frames <- out
			}
		case "InvokeCommand":
			var body struct {
				Category string `json:"commandCategory"`
				Name     string `json:"commandName"`
			}
			_ = json.Unmarshal(msg.Body, &body)
			for _, out := range f.onCommand[body.Category+"|"+body.Name] {
				f.frames <- out
			}
		}
	case proto.CodePV:
		if f.echo {
			f.frames <- frame
		}
	case proto.CodePS:
		if f.echoStrings {
			f.frames <- frame
		}
	}
	return nil
}

func (f *fakeStudio) SendBatch(ctx context.Context, frames []proto.Frame) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.batches = append(f.batches, frames)
	for _, frame := range frames {
		if frame.Code != proto.CodePV || f.closed {
			continue
		}
		key, _, _ := proto.UnmarshalPV(frame.Payload)
		switch key {
		case "transport/start":
			if f.ignoreStart {
				continue
			}
			f.frames <- pv("transport/start", 1)
			f.frames <- pv("transport/stop", 0)
		case "transport/stop":
			if f.stubbornStops > 0 {
				f.stubbornStops--
				f.frames <- pv("transport/stop", 1)
				f.frames <- pv("transport/stop", 0)
				continue
			}
			f.frames <- pv("transport/start", 0)
			f.frames <- pv("transport/stop", 1)
		case "transport/loop":
			_, v, _ := proto.UnmarshalPV(frame.Payload)
			f.loop = v == 1 && f.loopRange
			f.frames <- pv("transport/loop", float32(boolWire(f.loop)))
		}
	}
	return nil
}

func pv(key string, v float32) proto.Frame {
	return proto.Frame{Code: proto.CodePV, Payload: proto.MarshalPV(key, v)}
}

func ps(key, v string) proto.Frame {
	return proto.Frame{Code: proto.CodePS, Payload: proto.MarshalPS(key, v)}
}

func (f *fakeStudio) Frames() <-chan proto.Frame { return f.frames }

func (f *fakeStudio) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		f.closed = true
		close(f.frames)
	}
	return nil
}

func (f *fakeStudio) sentJM() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, frame := range f.sent {
		if frame.Code == proto.CodeJM {
			msg, _ := proto.ParseJM(frame.Payload)
			out = append(out, string(msg.Body))
		}
	}
	return out
}

func jmFrame(body string) proto.Frame {
	return proto.Frame{Code: proto.CodeJM, Payload: proto.MarshalJMRaw([]byte(body))}
}

func ckFrames(t *testing.T, body string, split int) []proto.Frame {
	t.Helper()
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	_, _ = w.Write([]byte(body))
	_ = w.Close()
	blob := buf.Bytes()
	var out []proto.Frame
	for off := 0; off < len(blob); off += split {
		end := off + split
		if end > len(blob) {
			end = len(blob)
		}
		header := make([]byte, 16)
		copy(header, []byte{0, 0, 'Z', 'M'})
		binary.LittleEndian.PutUint32(header[4:], uint32(off))
		binary.LittleEndian.PutUint32(header[8:], uint32(len(blob)))
		binary.LittleEndian.PutUint32(header[12:], uint32(end-off))
		out = append(out, proto.Frame{Code: proto.CodeCK, Payload: append(header, blob[off:end]...)})
	}
	return out
}

func dialFake(t *testing.T, fake *fakeStudio) *Session {
	t.Helper()
	return dialFakeTap(t, fake, nil)
}

func dialFakeTap(t *testing.T, fake *fakeStudio, tap func(proto.Frame)) *Session {
	t.Helper()
	origDial, origListen, origSleep := dialTransport, listenMetering, sleepFor
	t.Cleanup(func() { dialTransport, listenMetering, sleepFor = origDial, origListen, origSleep })
	dialTransport = func(context.Context, string, transport.Options) (transport.Transport, error) {
		return fake, nil
	}
	listenMetering = func() (net.PacketConn, error) { return net.ListenPacket("udp4", "127.0.0.1:0") }
	sleepFor = func(context.Context, time.Duration) error { return nil }
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s, err := DialTap(ctx, Endpoint{IP: "127.0.0.1", ControlPort: 1, ServerID: 0x6a}, tap)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func newFake(t *testing.T) *fakeStudio {
	commands := `{"id": "AdviseCommands","categories": [{"displayName": "Transport","commands": [` +
		`{"displayName": "Click","name": "Click","category": "Transport","englishName": "Click"}]}]}`
	snapshot := append(ckFrames(t, commands, 40), zm(t, syntheticSync))
	return &fakeStudio{frames: make(chan proto.Frame, 64), echo: true, snapshot: snapshot}
}

func TestDialLoadsSnapshotAndCommands(t *testing.T) {
	fake := newFake(t)
	s := dialFake(t, fake)
	if v, ok := s.Model().Num("metronome/clickOn"); !ok || v != 1 {
		t.Errorf("clickOn = %v, %v", v, ok)
	}
	if cmds := s.Model().Commands(); len(cmds) != 1 || cmds[0].Name != "Click" {
		t.Errorf("Commands = %+v", cmds)
	}
	jm := fake.sentJM()
	if len(jm) != 2 || jm[0] != `{"id": "QueryUserLicense","clientInternalName": "dawremote"}` {
		t.Errorf("handshake = %q", jm)
	}
}

func TestSetNumConfirmsByEcho(t *testing.T) {
	s := dialFake(t, newFake(t))
	if err := s.SetNum(context.Background(), "metronome/clickOn", 0); err != nil {
		t.Fatalf("SetNum: %v", err)
	}
	if v, _ := s.Model().Num("metronome/clickOn"); v != 0 {
		t.Errorf("clickOn = %v", v)
	}
}

func TestSetNumReportsUnconfirmed(t *testing.T) {
	fake := newFake(t)
	s := dialFake(t, fake)
	fake.echo = false
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := s.SetNum(ctx, "transport/tempo", 0.5)
	if !errors.Is(err, ErrUnconfirmed) {
		t.Errorf("SetNum = %v, want ErrUnconfirmed", err)
	}
}

func TestInvokeSendsRemoteAppBody(t *testing.T) {
	fake := newFake(t)
	s := dialFake(t, fake)
	if err := s.Invoke(context.Background(), "Track", "Add Audio Track (mono) & more", nil); err != nil {
		t.Fatal(err)
	}
	jm := fake.sentJM()
	want := `{"id": "InvokeCommand","commandCategory": "Track","commandName": "Add Audio Track (mono) & more"}`
	if jm[len(jm)-1] != want {
		t.Errorf("body = %s", jm[len(jm)-1])
	}
}

func TestPressSendsPressValueThenRelease(t *testing.T) {
	fake := newFake(t)
	s := dialFake(t, fake)
	if err := s.Press(context.Background(), "transport/start", 1); err != nil {
		t.Fatal(err)
	}
	if len(fake.batches) != 1 || len(fake.batches[0]) != 2 ||
		fake.batches[0][0].Code != proto.CodePE || fake.batches[0][1].Code != proto.CodePV {
		t.Fatalf("batches = %+v", fake.batches)
	}
	last := fake.sent[len(fake.sent)-1]
	key, state, err := proto.UnmarshalPE(last.Payload)
	if err != nil || key != "transport/start" || state != 0 {
		t.Errorf("release = %s %d %v", key, state, err)
	}
}

func TestSessionDoneWhenStudioProGoesAway(t *testing.T) {
	fake := newFake(t)
	s := dialFake(t, fake)
	_ = fake.Close()
	select {
	case <-s.Done():
	case <-time.After(time.Second):
		t.Fatal("Done not closed")
	}
	if err := s.WaitFor(context.Background(), func(*Model) bool { return false }); !errors.Is(err, ErrConnectionClosed) {
		t.Errorf("WaitFor = %v", err)
	}
}

func TestChunksRejectOversizedOrInconsistentBlobs(t *testing.T) {
	tests := []struct {
		name  string
		chunk proto.CKChunk
	}{
		{"declared total over the cap", proto.CKChunk{Offset: 0, Total: MaxBlob + 1, Size: 1, Data: []byte{1}}},
		{"chunk past its total", proto.CKChunk{Offset: 8, Total: 10, Size: 4, Data: []byte{1, 2, 3, 4}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var c chunks
			if _, err := c.add(tt.chunk); !errors.Is(err, ErrTooLarge) {
				t.Errorf("add = %v, want ErrTooLarge", err)
			}
		})
	}
}

func TestChunksCapBytesHeldAcrossChunks(t *testing.T) {
	var c chunks
	piece := make([]byte, 1<<20)
	total := uint32(MaxBlob)
	var err error
	// A peer that keeps restating offset 1 MB never completes and never resets.
	for i := 0; err == nil && i < 40; i++ {
		_, err = c.add(proto.CKChunk{Offset: 1 << 20, Total: total, Size: uint32(len(piece)), Data: piece})
	}
	if !errors.Is(err, ErrTooLarge) {
		t.Errorf("add = %v, want ErrTooLarge", err)
	}
}

func TestChunksReassembleWithinCap(t *testing.T) {
	var c chunks
	if blob, err := c.add(proto.CKChunk{Offset: 0, Total: 4, Size: 2, Data: []byte{1, 2}}); err != nil || blob != nil {
		t.Fatalf("first = %v, %v", blob, err)
	}
	blob, err := c.add(proto.CKChunk{Offset: 2, Total: 4, Size: 2, Data: []byte{3, 4}})
	if err != nil || !bytes.Equal(blob, []byte{1, 2, 3, 4}) {
		t.Errorf("blob = %v, %v", blob, err)
	}
}

func TestInvokeSendsCommandArgsAsObject(t *testing.T) {
	fake := newFake(t)
	s := dialFake(t, fake)
	args := map[string]string{"mode": "1", "cid": "{X}", "preset": "default"}
	if err := s.Invoke(context.Background(), "Track", "Add Insert to Selected Channels", args); err != nil {
		t.Fatal(err)
	}
	jm := fake.sentJM()
	want := `{"id": "InvokeCommand","commandCategory": "Track","commandName": "Add Insert to Selected Channels",` +
		`"commandArgs": {"cid": "{X}","mode": "1","preset": "default"}}`
	if jm[len(jm)-1] != want {
		t.Errorf("body = %s", jm[len(jm)-1])
	}
}

func TestDialogTrackedAndDismissed(t *testing.T) {
	fake := newFake(t)
	s := dialFake(t, fake)
	fake.mu.Lock()
	fake.frames <- zm(t, `{"id": "BeginDialog","dialogType": 1,"dialogText": "","dialogTitle": "Add Insert to Selected Channels","firstButton": "Cancel","secondButton": "OK"}`)
	fake.mu.Unlock()
	_ = s.WaitFor(context.Background(), func(m *Model) bool { _, open := m.Dialog(); return open })
	d, _ := s.Model().Dialog()
	if d.Title != "Add Insert to Selected Channels" || len(d.Buttons) != 2 {
		t.Errorf("dialog = %+v", d)
	}
	fake.onConfirm = []proto.Frame{zm(t, `{"id": "EndDialog"}`)}
	if err := s.DismissDialog(context.Background()); err != nil {
		t.Fatal(err)
	}
	if jm := fake.sentJM(); jm[len(jm)-1] != `{"id": "ConfirmDialog","dialogResult": 0}` {
		t.Errorf("sent %s", jm[len(jm)-1])
	}
}
