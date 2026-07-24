package ucmix

import (
	"time"

	"github.com/steveclarke/ucmix/internal/proto"
	"github.com/steveclarke/ucmix/internal/transport"
)

// Option configures a Client at Connect time. Options are functional: each
// mutates a private config that Connect reads once.
type Option func(*config)

// defaultCommitDelay is the post-write hold a Client applies before the caller
// closes, so the board's sequential per-connection reader consumes the write
// frames before the connection drops. Closing immediately races delivery and
// the tail of a write burst is silently lost.
const defaultCommitDelay = 500 * time.Millisecond

// config is the resolved connect-time configuration. Zero values take the
// transport/proto defaults.
type config struct {
	clientName     string            // overrides the Subscribe clientName; "" keeps the default
	transportOpts  transport.Options // pacing / keep-alive / dial timeout
	connectTimeout time.Duration     // bound on the handshake wait; 0 = no extra bound
	commitDelay    time.Duration     // post-write hold; 0 = no barrier (caller commits itself)
}

// resolve applies opts to a fresh config seeded with the defaults.
func resolve(opts []Option) config {
	c := config{commitDelay: defaultCommitDelay}
	for _, o := range opts {
		o(&c)
	}
	return c
}

// subscribeCmd returns the Subscribe body for this config: the default UC
// Surface body with clientName overridden when WithClientName was set.
func (c config) subscribeCmd() proto.SubscribeCmd {
	cmd := proto.DefaultSubscribeCmd()
	if c.clientName != "" {
		cmd.ClientName = c.clientName
	}
	return cmd
}

// WithClientName overrides the clientName reported in the Subscribe handshake.
// The default is "UC-Surface" (what UC Surface and featherbear send).
func WithClientName(name string) Option {
	return func(c *config) { c.clientName = name }
}

// WithTransportOptions sets the transport pacing, keep-alive, and dial-timeout
// options. Zero-valued fields inside opts still take the transport defaults.
func WithTransportOptions(opts transport.Options) Option {
	return func(c *config) { c.transportOpts = opts }
}

// WithConnectTimeout bounds how long Connect waits for the ZB snapshot before
// giving up. Zero (the default) means the only bound is the caller's context.
func WithConnectTimeout(d time.Duration) Option {
	return func(c *config) { c.connectTimeout = d }
}

// WithCommitDelay sets the post-write hold applied by Set and SetMany so the
// board commits a write burst before the connection closes. The default is
// defaultCommitDelay. Zero disables the barrier — for callers that stream their
// own paced writes and commit themselves (e.g. a fade), where a per-write hold
// would stall the stream.
func WithCommitDelay(d time.Duration) Option {
	return func(c *config) { c.commitDelay = d }
}
