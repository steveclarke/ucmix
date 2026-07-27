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
)

var ErrConnectionClosed = errors.New("daw: connection closed")

// Action is one supported Studio Pro transport gesture.
type Action string

const (
	Play Action = "play"
	Stop Action = "stop"
)

type config struct {
	identifier string
	transport  transport.Options
}

// Option configures a DAW client.
type Option func(*config)

// ClientIdentifier supplies the UUID sent in the Studio Pro subscription.
func ClientIdentifier(value string) Option {
	return func(cfg *config) { cfg.identifier = value }
}

// Client is one short-lived Studio Pro UCNET session.
type Client struct {
	endpoint Endpoint
	t        transport.Transport
	metering net.PacketConn

	closeOnce sync.Once
	closeErr  error
}

// Connect completes the captured Studio Pro Remote handshake.
func Connect(ctx context.Context, endpoint Endpoint, opts ...Option) (*Client, error) {
	cfg := config{
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

	client := &Client{endpoint: endpoint, t: t, metering: metering}
	if err := client.handshake(ctx, cfg.identifier); err != nil {
		_ = client.Close()
		return nil, err
	}
	return client, nil
}

func (c *Client) handshake(ctx context.Context, identifier string) error {
	if err := c.t.Send(ctx, proto.Frame{
		Code:    proto.CodeJM,
		ConnID:  mainConnID(c.endpoint),
		Payload: proto.MarshalJM(queryUserLicense()),
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
			return ctx.Err()
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
		Payload: proto.MarshalJM(subscribe(identifier)),
	}); err != nil {
		return fmt.Errorf("daw: sending Subscribe: %w", err)
	}
	return nil
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
	for _, frame := range frames {
		if err := c.t.Send(ctx, frame); err != nil {
			return fmt.Errorf("daw: sending %s: %w", action, err)
		}
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
			if err == nil && gotKey == key && value == 1 {
				return nil
			}
		case <-ctx.Done():
			return ctx.Err()
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
