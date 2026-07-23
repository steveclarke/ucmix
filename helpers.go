package ucmix

import (
	"context"
	"encoding/hex"
	"fmt"
)

// ChannelType names a family of channels and maps to its wire path prefix.
type ChannelType string

const (
	Line     ChannelType = "line"     // input channels: line/chN
	Aux      ChannelType = "aux"      // monitor-mix masters: aux/chN
	FXBus    ChannelType = "fx"       // FX buses: fx/chN
	FXReturn ChannelType = "fxreturn" // FX returns: fxreturn/chN
	Main     ChannelType = "main"     // main bus: main/chN
	Sub      ChannelType = "sub"      // subgroups: sub/chN
)

// Path builds a "prefix/chN/leaf" wire path for this channel type, e.g.
// Line.Path(12, "volume") == "line/ch12/volume". A leaf may itself contain
// slashes ("filter/hpf", "limit/threshold").
func (ct ChannelType) Path(n int, leaf string) string {
	return fmt.Sprintf("%s/ch%d/%s", ct, n, leaf)
}

// Mute mutes or unmutes channel n of type ct.
func (c *Client) Mute(ctx context.Context, ct ChannelType, n int, on bool) error {
	return c.Set(ctx, ct.Path(n, "mute"), on)
}

// SetFaderDB sets the fader level of channel n to db decibels.
func (c *Client) SetFaderDB(ctx context.Context, ct ChannelType, n int, db float64) error {
	return c.Set(ctx, ct.Path(n, "volume"), db)
}

// SetName sets the channel's user-visible name.
func (c *Client) SetName(ctx context.Context, ct ChannelType, n int, name string) error {
	return c.Set(ctx, ct.Path(n, "username"), name)
}

// SetIcon sets the channel's icon id (e.g. "drums/drumset").
func (c *Client) SetIcon(ctx context.Context, ct ChannelType, n int, icon string) error {
	return c.Set(ctx, ct.Path(n, "iconid"), icon)
}

// SetColor sets the channel color from an RGB hex string (e.g. "4ed2ff"). A
// fully-opaque alpha byte (0xff) is appended, matching the board's PC format.
func (c *Client) SetColor(ctx context.Context, ct ChannelType, n int, rgbHex string) error {
	rgb, err := hex.DecodeString(rgbHex)
	if err != nil {
		return fmt.Errorf("ucmix: color %q is not hex: %w", rgbHex, err)
	}
	return c.Set(ctx, ct.Path(n, "color"), append(rgb, 0xff))
}

// Set48V toggles phantom power on channel n.
func (c *Client) Set48V(ctx context.Context, ct ChannelType, n int, on bool) error {
	return c.Set(ctx, ct.Path(n, "48v"), on)
}

// SetHPFHz sets the high-pass filter frequency. The HPF taper is an
// uncalibrated pass-through in v1, so hz is sent as the raw 0..1 wire value.
func (c *Client) SetHPFHz(ctx context.Context, ct ChannelType, n int, hz float64) error {
	return c.Set(ctx, ct.Path(n, "filter/hpf"), hz)
}

// SetInputPatch patches channel n to physical input number input (0 = unpatched,
// 1..32). Encodes as input/32 on the wire (adc_src).
func (c *Client) SetInputPatch(ctx context.Context, ct ChannelType, n, input int) error {
	return c.Set(ctx, ct.Path(n, "adc_src"), float64(input))
}

// SetPan sets the channel pan as a raw 0..1 wire position (no taper is
// calibrated for pan in v1).
func (c *Client) SetPan(ctx context.Context, ct ChannelType, n int, pos float64) error {
	return c.Set(ctx, ct.Path(n, "pan"), pos)
}

// SetLink toggles the stereo link flag on channel n. The full link expansion
// (linkmaster, panlinkstate) is a boardconfig concern; this sets only link.
func (c *Client) SetLink(ctx context.Context, ct ChannelType, n int, on bool) error {
	return c.Set(ctx, ct.Path(n, "link"), on)
}

// SetSendDB sets channel n's monitor send to aux bus auxN to db decibels.
func (c *Client) SetSendDB(ctx context.Context, ct ChannelType, n, auxN int, db float64) error {
	return c.Set(ctx, ct.Path(n, fmt.Sprintf("aux%d", auxN)), db)
}

// SetLimiter configures the limiter on channel n of type ct: on/off, threshold
// in dB, and release in ms. It emits three writes (limiteron, threshold,
// release); the first error stops the sequence.
func (c *Client) SetLimiter(ctx context.Context, ct ChannelType, n int, on bool, thresholdDB, releaseMs float64) error {
	if err := c.Set(ctx, ct.Path(n, "limit/limiteron"), on); err != nil {
		return err
	}
	if err := c.Set(ctx, ct.Path(n, "limit/threshold"), thresholdDB); err != nil {
		return err
	}
	return c.Set(ctx, ct.Path(n, "limit/release"), releaseMs)
}

// SetReverbType sets the FX-bus reverb type as a raw enum float (e.g. 0.375 =
// Vintage Plate). No taper is calibrated for the type enum in v1.
func (c *Client) SetReverbType(ctx context.Context, n int, typeVal float64) error {
	return c.Set(ctx, FXBus.Path(n, "type"), typeVal)
}

// AssignMain toggles channel n's assignment to the main LR bus (the lr flag).
func (c *Client) AssignMain(ctx context.Context, ct ChannelType, n int, on bool) error {
	return c.Set(ctx, ct.Path(n, "lr"), on)
}
