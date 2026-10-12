package daw

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/steveclarke/ucmix/internal/proto"
	"github.com/steveclarke/ucmix/internal/transport"
)

// Test seams.
var (
	dialTransport  = transport.Dial
	listenMetering = func() (net.PacketConn, error) {
		return net.ListenPacket("udp4", "0.0.0.0:0")
	}
	sleepFor = func(ctx context.Context, d time.Duration) error {
		timer := time.NewTimer(d)
		defer timer.Stop()
		select {
		case <-timer.C:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
)

// ErrConnectionClosed reports that Studio Pro closed the session.
var ErrConnectionClosed = errors.New("daw: connection to Studio Pro closed")

// ErrUnconfirmed reports that Studio Pro did not echo a write in time.
var ErrUnconfirmed = errors.New("daw: Studio Pro did not confirm the change")

// gestureHold is how long a button stays pressed, matching the iPad app.
const gestureHold = 80 * time.Millisecond

// keepAliveEvery matches the cadence proven against Studio Pro.
const keepAliveEvery = time.Second

// Session is one subscribed Studio Pro Remote connection with a live model.
type Session struct {
	endpoint Endpoint
	t        transport.Transport
	metering net.PacketConn
	model    *Model

	tap func(proto.Frame)

	done      chan struct{}
	stop      chan struct{}
	closeOnce sync.Once
	closeErr  error
}

// Dial connects to Studio Pro, subscribes as the Studio Pro Remote app, loads
// the initial snapshot and keeps the model current until Close.
func Dial(ctx context.Context, endpoint Endpoint) (*Session, error) {
	return DialTap(ctx, endpoint, nil)
}

// DialTap is Dial with tap called on every inbound frame, from the first
// license reply on (for recording).
func DialTap(ctx context.Context, endpoint Endpoint, tap func(proto.Frame)) (*Session, error) {
	metering, err := listenMetering()
	if err != nil {
		return nil, fmt.Errorf("daw: opening UDP metering socket: %w", err)
	}
	address := net.JoinHostPort(endpoint.IP, strconv.Itoa(endpoint.ControlPort))
	t, err := dialTransport(ctx, address, transport.Options{
		KeepAlive:   24 * time.Hour, // the session sends its own, on its connection ID
		DialTimeout: 2 * time.Second,
	})
	if err != nil {
		_ = metering.Close()
		return nil, fmt.Errorf("daw: connecting to %s: %w", address, err)
	}
	s := &Session{
		endpoint: endpoint,
		t:        t,
		metering: metering,
		model:    NewModel(),
		tap:      tap,
		done:     make(chan struct{}),
		stop:     make(chan struct{}),
	}
	if err := s.handshake(ctx); err != nil {
		_ = s.Close()
		return nil, err
	}
	go s.readLoop()
	go s.keepAlive()
	return s, nil
}

// Endpoint is the Studio Pro this session talks to.
func (s *Session) Endpoint() Endpoint { return s.endpoint }

// Model is the live song state.
func (s *Session) Model() *Model { return s.model }

// Done is closed when the connection ends.
func (s *Session) Done() <-chan struct{} { return s.done }

func (s *Session) sendJM(ctx context.Context, body []byte) error {
	return s.t.Send(ctx, proto.Frame{
		Code:    proto.CodeJM,
		ConnID:  mainConnID(s.endpoint),
		Payload: proto.MarshalJMRaw(body),
	})
}

func (s *Session) handshake(ctx context.Context) error {
	if err := s.sendJM(ctx, queryUserLicenseBody()); err != nil {
		return fmt.Errorf("daw: sending license query: %w", err)
	}
	if err := s.waitJM(ctx, "UserLicenseReply"); err != nil {
		return fmt.Errorf("daw: waiting for Studio Pro's license reply: %w", err)
	}
	local, ok := s.metering.LocalAddr().(*net.UDPAddr)
	if !ok || local.Port <= 0 || local.Port > 65535 {
		return fmt.Errorf("daw: invalid UDP metering address %v", s.metering.LocalAddr())
	}
	if err := s.t.Send(ctx, proto.Frame{
		Code:    proto.CodeUM,
		ConnID:  meterConnID(s.endpoint),
		Payload: proto.MarshalUM(uint16(local.Port)),
	}); err != nil {
		return fmt.Errorf("daw: advertising UDP metering port: %w", err)
	}
	if err := s.sendJM(ctx, subscribeBody(newIdentifier())); err != nil {
		return fmt.Errorf("daw: sending Subscribe: %w", err)
	}
	if err := s.waitJM(ctx, "SubscriptionReply"); err != nil {
		return fmt.Errorf("daw: waiting for the song snapshot: %w", err)
	}
	return nil
}

// MaxBlob caps one chunked (CK) message before inflation.
const MaxBlob = 16 << 20

// chunks reassembles CK frames with a size cap. The shared proto assembler
// trusts the peer; this wrapper drops any blob that claims or grows past
// MaxBlob, and any chunk that does not fit its declared total.
type chunks struct {
	asm  proto.ChunkAssembler
	held int
}

func (c *chunks) add(chunk proto.CKChunk) ([]byte, error) {
	end := uint64(chunk.Offset) + uint64(chunk.Size)
	if chunk.Offset == 0 {
		c.held = 0
	}
	if chunk.Total > MaxBlob || end > uint64(chunk.Total) || c.held+len(chunk.Data) > MaxBlob {
		c.asm, c.held = proto.ChunkAssembler{}, 0
		return nil, fmt.Errorf("%w: chunked message of %d bytes", ErrTooLarge, chunk.Total)
	}
	c.held += len(chunk.Data)
	blob, complete := c.asm.Add(chunk)
	if !complete {
		return nil, nil
	}
	c.held = 0
	return blob, nil
}

// waitJM applies frames to the model until a JM with the given id arrives.
func (s *Session) waitJM(ctx context.Context, id string) error {
	var pending chunks
	for {
		select {
		case frame, ok := <-s.t.Frames():
			if !ok {
				return ErrConnectionClosed
			}
			if frame.Code == proto.CodeJM {
				if msg, err := proto.ParseJM(frame.Payload); err == nil && msg.ID == id {
					if s.tap != nil {
						s.tap(frame)
					}
					return nil
				}
			}
			s.apply(&pending, frame)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (s *Session) apply(pending *chunks, frame proto.Frame) {
	if s.tap != nil {
		s.tap(frame)
	}
	if frame.Code == proto.CodeCK {
		chunk, err := proto.ParseCK(frame.Payload)
		if err != nil {
			return
		}
		blob, err := pending.add(chunk)
		if err != nil || blob == nil {
			return
		}
		if body, err := inflate(blob); err == nil {
			_ = s.model.ApplyJSON(body)
		}
		return
	}
	_ = s.model.ApplyFrame(frame)
}

func (s *Session) readLoop() {
	defer close(s.done)
	var pending chunks
	for frame := range s.t.Frames() {
		s.apply(&pending, frame)
	}
}

func (s *Session) keepAlive() {
	ticker := time.NewTicker(keepAliveEvery)
	defer ticker.Stop()
	ka := proto.Frame{Code: proto.CodeKA, ConnID: mainConnID(s.endpoint)}
	for {
		select {
		case <-ticker.C:
			if err := s.t.Send(context.Background(), ka); err != nil {
				return
			}
		case <-s.stop:
			return
		case <-s.done:
			return
		}
	}
}

// WaitFor blocks until cond holds against the model, the context ends, or the
// connection closes. cond is checked first, so a state that already holds
// returns at once.
func (s *Session) WaitFor(ctx context.Context, cond func(*Model) bool) error {
	for {
		wake := s.model.Changed()
		if cond(s.model) {
			return nil
		}
		select {
		case <-wake:
		case <-s.done:
			return ErrConnectionClosed
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func closeTo(a, b float64) bool { return math.Abs(a-b) < 1e-4 }

// NumIs is a WaitFor condition: path holds wire value v.
func NumIs(path string, v float64) func(*Model) bool {
	return func(m *Model) bool {
		got, ok := m.Num(path)
		return ok && closeTo(got, v)
	}
}

// SetNum writes a wire value and waits until Studio Pro reports it back.
// Studio Pro sends nothing when the value does not change, so a value already
// in the model returns at once.
func (s *Session) SetNum(ctx context.Context, path string, wire float64) error {
	if err := s.t.Send(ctx, proto.Frame{
		Code:    proto.CodePV,
		ConnID:  mainConnID(s.endpoint),
		Payload: proto.MarshalPV(path, float32(wire)),
	}); err != nil {
		return fmt.Errorf("daw: writing %s: %w", path, err)
	}
	return s.confirm(ctx, NumIs(path, wire))
}

// SetStr writes a string value and waits until Studio Pro reports it back.
func (s *Session) SetStr(ctx context.Context, path, value string) error {
	if err := s.t.Send(ctx, proto.Frame{
		Code:    proto.CodePS,
		ConnID:  mainConnID(s.endpoint),
		Payload: proto.MarshalPS(path, value),
	}); err != nil {
		return fmt.Errorf("daw: writing %s: %w", path, err)
	}
	return s.confirm(ctx, func(m *Model) bool {
		got, ok := m.Str(path)
		return ok && got == value
	})
}

func (s *Session) confirm(ctx context.Context, cond func(*Model) bool) error {
	waitCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	err := s.WaitFor(waitCtx, cond)
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
		return ErrUnconfirmed
	}
	return err
}

// Press sends the button gesture the iPad app uses: press and value in one
// write, a short hold, then release as a separate write.
func (s *Session) Press(ctx context.Context, path string, wire float64) error {
	id := mainConnID(s.endpoint)
	if err := s.t.SendBatch(ctx, []proto.Frame{
		{Code: proto.CodePE, ConnID: id, Payload: proto.MarshalPE(path, 2)},
		{Code: proto.CodePV, ConnID: id, Payload: proto.MarshalPV(path, float32(wire))},
	}); err != nil {
		return fmt.Errorf("daw: pressing %s: %w", path, err)
	}
	holdErr := sleepFor(ctx, gestureHold)
	// The release goes out even if the caller gave up mid-hold: a button
	// left pressed in Studio Pro is worse than a late answer.
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	if err := s.t.Send(releaseCtx, proto.Frame{
		Code: proto.CodePE, ConnID: id, Payload: proto.MarshalPE(path, 0),
	}); err != nil {
		return fmt.Errorf("daw: releasing %s: %w", path, err)
	}
	return holdErr
}

// Invoke runs a Studio Pro command by category and name, exactly as the
// Remote app's Commands page does, with optional command arguments (the same
// name/value pairs a Studio Pro macro file carries). Studio Pro sends no
// reply; the effect shows up as model changes.
func (s *Session) Invoke(ctx context.Context, category, name string, args map[string]string) error {
	return s.sendJM(ctx, invokeCommandBody(category, name, args))
}

// DismissDialog presses the open dialog's first button (Cancel in every
// dialog seen so far) and waits for Studio Pro to close it.
func (s *Session) DismissDialog(ctx context.Context) error {
	if err := s.sendJM(ctx, []byte(`{"id": "ConfirmDialog","dialogResult": 0}`)); err != nil {
		return err
	}
	return s.confirm(ctx, func(m *Model) bool {
		_, open := m.Dialog()
		return !open
	})
}

// Close ends the session.
func (s *Session) Close() error {
	s.closeOnce.Do(func() {
		close(s.stop)
		s.closeErr = errors.Join(s.t.Close(), s.metering.Close())
	})
	return s.closeErr
}

func invokeCommandBody(category, name string, args map[string]string) []byte {
	body := fmt.Sprintf(
		`{"id": "InvokeCommand","commandCategory": %s,"commandName": %s`,
		jsonString(category), jsonString(name),
	)
	if len(args) > 0 {
		keys := make([]string, 0, len(args))
		for k := range args {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = jsonString(k) + ": " + jsonString(args[k])
		}
		body += `,"commandArgs": {` + strings.Join(parts, ",") + "}"
	}
	return []byte(body + "}")
}

// jsonString quotes s without HTML escaping, so "&" and "<" reach Studio Pro
// as typed.
func jsonString(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return string(bytes.TrimRight(buf.Bytes(), "\n"))
}

func newIdentifier() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "00000000-0000-4000-8000-000000000000"
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	raw := hex.EncodeToString(value[:])
	return fmt.Sprintf(
		"%s-%s-%s-%s-%s",
		raw[0:8], raw[8:12], raw[12:16], raw[16:20], raw[20:32],
	)
}
