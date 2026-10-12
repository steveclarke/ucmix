package daw

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/steveclarke/ucmix/internal/proto"
	"github.com/steveclarke/ucmix/internal/transport"
)

var (
	dialTransport  = transport.Dial
	listenMetering = func() (net.PacketConn, error) {
		return net.ListenPacket("udp4", "0.0.0.0:0")
	}
	actionHold = func(ctx context.Context, duration time.Duration) error {
		timer := time.NewTimer(duration)
		defer timer.Stop()
		select {
		case <-timer.C:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
)

var ErrConnectionClosed = errors.New("daw: connection closed")

const transportGestureHold = 80 * time.Millisecond

// Action is one supported Studio Pro transport gesture.
type Action string

const (
	Play Action = "play"
	Stop Action = "stop"
)

type config struct {
	identifier       string
	handshakeTimeout time.Duration
	actionTimeout    time.Duration
	transport        transport.Options
}

// Option configures a DAW client.
type Option func(*config)

// ClientIdentifier supplies the UUID sent in the Studio Pro subscription.
func ClientIdentifier(value string) Option {
	return func(cfg *config) { cfg.identifier = value }
}

// HandshakeTimeout bounds the wait for Studio Pro's license reply.
func HandshakeTimeout(value time.Duration) Option {
	return func(cfg *config) { cfg.handshakeTimeout = value }
}

// ActionTimeout bounds each transport send-and-confirm exchange.
func ActionTimeout(value time.Duration) Option {
	return func(cfg *config) { cfg.actionTimeout = value }
}

// Client is one short-lived Studio Pro UCNET session.
type Client struct {
	endpoint Endpoint
	t        transport.Transport
	metering net.PacketConn

	actionTimeout time.Duration

	stateMu        sync.RWMutex
	transportState map[string]float32

	closeOnce sync.Once
	closeErr  error
}

// Connect completes the captured Studio Pro Remote handshake.
func Connect(ctx context.Context, endpoint Endpoint, opts ...Option) (*Client, error) {
	cfg := config{
		handshakeTimeout: 5 * time.Second,
		actionTimeout:    5 * time.Second,
		transport: transport.Options{
			KeepAlive:   30 * time.Second,
			DialTimeout: 3 * time.Second,
		},
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.identifier == "" {
		cfg.identifier = newIdentifier()
	}

	metering, err := listenMetering()
	if err != nil {
		return nil, fmt.Errorf("daw: opening UDP metering socket: %w", err)
	}
	address := net.JoinHostPort(endpoint.IP, strconv.Itoa(endpoint.ControlPort))
	t, err := dialTransport(ctx, address, cfg.transport)
	if err != nil {
		_ = metering.Close()
		return nil, fmt.Errorf("daw: connecting to %s: %w", address, err)
	}

	client := &Client{
		endpoint:       endpoint,
		t:              t,
		metering:       metering,
		actionTimeout:  cfg.actionTimeout,
		transportState: make(map[string]float32),
	}
	handshakeCtx, cancelHandshake := context.WithTimeout(
		ctx, cfg.handshakeTimeout,
	)
	defer cancelHandshake()
	if err := client.handshake(handshakeCtx, cfg.identifier); err != nil {
		_ = client.Close()
		return nil, err
	}
	return client, nil
}

func (c *Client) handshake(ctx context.Context, identifier string) error {
	if err := c.t.Send(ctx, proto.Frame{
		Code:    proto.CodeJM,
		ConnID:  mainConnID(c.endpoint),
		Payload: proto.MarshalJMRaw(queryUserLicenseBody()),
	}); err != nil {
		return fmt.Errorf("daw: sending license query: %w", err)
	}

	licensed := false
	for !licensed {
		select {
		case frame, ok := <-c.t.Frames():
			if !ok {
				return ErrConnectionClosed
			}
			if frame.Code != proto.CodeJM {
				continue
			}
			message, err := proto.ParseJM(frame.Payload)
			if err == nil && message.ID == "UserLicenseReply" {
				licensed = true
			}
		case <-ctx.Done():
			return fmt.Errorf("daw: waiting for license reply: %w", ctx.Err())
		}
	}

	local, ok := c.metering.LocalAddr().(*net.UDPAddr)
	if !ok || local.Port <= 0 || local.Port > 65535 {
		return fmt.Errorf(
			"daw: invalid UDP metering address %v", c.metering.LocalAddr(),
		)
	}
	if err := c.t.Send(ctx, proto.Frame{
		Code:    proto.CodeUM,
		ConnID:  meterConnID(c.endpoint),
		Payload: proto.MarshalUM(uint16(local.Port)),
	}); err != nil {
		return fmt.Errorf("daw: advertising UDP metering port: %w", err)
	}
	if err := c.t.Send(ctx, proto.Frame{
		Code:    proto.CodeJM,
		ConnID:  mainConnID(c.endpoint),
		Payload: proto.MarshalJMRaw(subscribeBody(identifier)),
	}); err != nil {
		return fmt.Errorf("daw: sending Subscribe: %w", err)
	}

	var chunks proto.ChunkAssembler
	for {
		select {
		case frame, ok := <-c.t.Frames():
			if !ok {
				return ErrConnectionClosed
			}
			switch frame.Code {
			case proto.CodeZB:
				_ = c.loadSnapshot(frame.Payload)
				return nil
			case proto.CodeCK:
				chunk, err := proto.ParseCK(frame.Payload)
				if err != nil {
					continue
				}
				if blob, complete := chunks.Add(chunk); complete {
					_ = c.loadSnapshot(blob)
					return nil
				}
			}
		case <-ctx.Done():
			return fmt.Errorf("daw: waiting for initial snapshot: %w", ctx.Err())
		}
	}
}

func (c *Client) loadSnapshot(blob []byte) error {
	snapshot, err := proto.ParseZB(blob)
	if err != nil {
		return fmt.Errorf("daw: decoding initial snapshot: %w", err)
	}
	state := make(map[string]float32, 2)
	for _, key := range []string{"transport/start", "transport/stop"} {
		if value, ok := asFloat32(snapshot[key]); ok {
			state[key] = value
		}
	}
	c.stateMu.Lock()
	c.transportState = state
	c.stateMu.Unlock()
	return nil
}

func asFloat32(value any) (float32, bool) {
	switch value := value.(type) {
	case float32:
		return value, true
	case float64:
		return float32(value), true
	case int:
		return float32(value), true
	case int64:
		return float32(value), true
	case uint64:
		return float32(value), true
	case bool:
		if value {
			return 1, true
		}
		return 0, true
	default:
		return 0, false
	}
}

func (c *Client) recordTransport(key string, value float32) {
	c.stateMu.Lock()
	if c.transportState == nil {
		c.transportState = make(map[string]float32)
	}
	c.transportState[key] = value
	c.stateMu.Unlock()
}

func (c *Client) transportConfirmed(key string) bool {
	c.stateMu.RLock()
	value, ok := c.transportState[key]
	c.stateMu.RUnlock()
	return ok && value == 1
}

func (c *Client) recordActionConfirmed(action Action) {
	switch action {
	case Play:
		c.recordTransport("transport/start", 1)
		c.recordTransport("transport/stop", 0)
	case Stop:
		c.recordTransport("transport/start", 0)
		c.recordTransport("transport/stop", 1)
	}
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

// Trigger sends the captured PE/PV/PE gesture and waits for Studio Pro to
// report the matching active transport state.
func (c *Client) Trigger(ctx context.Context, action Action) error {
	timeout := c.actionTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	actionCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var key string
	switch action {
	case Play:
		key = "transport/start"
	case Stop:
		key = "transport/stop"
	default:
		return fmt.Errorf("daw: unsupported transport action %q", action)
	}

	connID := mainConnID(c.endpoint)
	frames := []proto.Frame{
		{
			Code:    proto.CodePE,
			ConnID:  connID,
			Payload: proto.MarshalPE(key, 2),
		},
		{
			Code:    proto.CodePV,
			ConnID:  connID,
			Payload: proto.MarshalPV(key, 1),
		},
		{
			Code:    proto.CodePE,
			ConnID:  connID,
			Payload: proto.MarshalPE(key, 0),
		},
	}
	if err := c.t.SendBatch(actionCtx, frames[:2]); err != nil {
		return fmt.Errorf("daw: sending %s press: %w", action, err)
	}
	if err := actionHold(actionCtx, transportGestureHold); err != nil {
		return fmt.Errorf("daw: holding %s gesture: %w", action, err)
	}
	if err := c.t.Send(actionCtx, frames[2]); err != nil {
		return fmt.Errorf("daw: sending %s release: %w", action, err)
	}
	if action == Stop {
		c.recordActionConfirmed(Stop)
		return nil
	}
	if c.transportConfirmed(key) {
		c.recordActionConfirmed(action)
		return nil
	}

	for {
		select {
		case frame, ok := <-c.t.Frames():
			if !ok {
				return ErrConnectionClosed
			}
			if frame.Code != proto.CodePV {
				continue
			}
			gotKey, value, err := proto.UnmarshalPV(frame.Payload)
			if err != nil {
				continue
			}
			c.recordTransport(gotKey, value)
			if gotKey == key && value == 1 {
				c.recordActionConfirmed(action)
				return nil
			}
		case <-actionCtx.Done():
			return fmt.Errorf(
				"daw: waiting for %s confirmation: %w",
				action, actionCtx.Err(),
			)
		}
	}
}

// Frames exposes live Studio Pro events for the disposable protocol lab.
// Production callers should use higher-level operations instead.
func (c *Client) Frames() <-chan proto.Frame {
	return c.t.Frames()
}

// SendParameter sends one PV write without waiting for confirmation.
func (c *Client) SendParameter(ctx context.Context, key string, value float32) error {
	return c.t.Send(ctx, proto.Frame{
		Code:    proto.CodePV,
		ConnID:  mainConnID(c.endpoint),
		Payload: proto.MarshalPV(key, value),
	})
}

// Gesture sends the press/write/release sequence used by DAW buttons.
func (c *Client) Gesture(ctx context.Context, key string, value float32) error {
	connID := mainConnID(c.endpoint)
	if err := c.t.SendBatch(ctx, []proto.Frame{
		{
			Code:    proto.CodePE,
			ConnID:  connID,
			Payload: proto.MarshalPE(key, 2),
		},
		{
			Code:    proto.CodePV,
			ConnID:  connID,
			Payload: proto.MarshalPV(key, value),
		},
	}); err != nil {
		return err
	}
	if err := actionHold(ctx, transportGestureHold); err != nil {
		return err
	}
	return c.t.Send(ctx, proto.Frame{
		Code:    proto.CodePE,
		ConnID:  connID,
		Payload: proto.MarshalPE(key, 0),
	})
}

// SetParameter writes one Studio Pro float parameter and waits for the echoed
// value that confirms the DAW accepted it.
func (c *Client) SetParameter(ctx context.Context, key string, value float32) error {
	timeout := c.actionTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	actionCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := c.SendParameter(actionCtx, key, value); err != nil {
		return fmt.Errorf("daw: setting %s: %w", key, err)
	}

	for {
		select {
		case frame, ok := <-c.t.Frames():
			if !ok {
				return ErrConnectionClosed
			}
			if frame.Code != proto.CodePV {
				continue
			}
			gotKey, gotValue, err := proto.UnmarshalPV(frame.Payload)
			if err == nil && gotKey == key && gotValue == value {
				return nil
			}
		case <-actionCtx.Done():
			return fmt.Errorf(
				"daw: waiting for %s=%g confirmation: %w",
				key, value, actionCtx.Err(),
			)
		}
	}
}

// Close closes both the UCNET TCP session and its UDP metering socket.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		c.closeErr = errors.Join(c.t.Close(), c.metering.Close())
	})
	return c.closeErr
}
