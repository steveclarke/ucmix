package boardconfig

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/steveclarke/ucmix/internal/schema"
)

// Desired is one compiled write: a wire path, the value to compare/apply, and
// the human value it came from (for diff output).
//
// WireValue is expressed in the board's *read/snapshot* scale, so it compares
// directly against [Diff]'s snapshot. For volume families that means the human
// dB is taper-converted to a 0..1 position and then multiplied by ReadScale
// (×100) — e.g. -6 dB → 0.746 → 74.6, matching a snapshot read. An apply layer
// that writes raw PV must undo the ×100 (the "÷100 write" quirk); it must not
// send WireValue verbatim for those keys.
type Desired struct {
	Path       string
	WireValue  any
	HumanValue any
}

// Compile lowers a validated config to an ordered desired set. Ordering mirrors
// the proven manual rebuild sequence (design §5.2): identity → links → patch →
// preamp/48V/HPF → assigns → levels/sends → mix masters → limiters → FX type →
// FX returns → raw. Sugar (link/stereo triples, name→aux-index sends, fx
// assigns, color alpha) expands here.
func Compile(cfg Config) ([]Desired, error) {
	c := &compiler{mixes: cfg.Mixes}

	// Phase 1: identity (names, icons, colors).
	for _, n := range sortedChannelKeys(cfg.Channels) {
		ch := cfg.Channels[n]
		if ch.Name != nil {
			c.str(fmt.Sprintf("line/ch%d/username", n), *ch.Name)
		}
		if ch.Icon != nil {
			c.str(fmt.Sprintf("line/ch%d/iconid", n), *ch.Icon)
		}
		if ch.Color != nil {
			if err := c.color(fmt.Sprintf("line/ch%d/color", n), *ch.Color, n); err != nil {
				return nil, err
			}
		}
	}
	for _, n := range sortedMixKeys(cfg.Mixes) {
		if mix := cfg.Mixes[n]; mix.Name != nil {
			c.str(fmt.Sprintf("aux/ch%d/username", n), *mix.Name)
		}
	}
	for _, k := range sortedStringKeys(cfg.FXReturns) {
		if fr := cfg.FXReturns[k]; fr.Name != nil {
			ch, err := letterToChannel(k)
			if err != nil {
				return nil, fmt.Errorf("boardconfig: fxreturns.%s: %w", k, err)
			}
			c.str(fmt.Sprintf("fxreturn/ch%d/username", ch), *fr.Name)
		}
	}

	// Phase 2: links (stereo pairs).
	for _, n := range sortedChannelKeys(cfg.Channels) {
		if ch := cfg.Channels[n]; ch.Link != nil && *ch.Link {
			c.bool(fmt.Sprintf("line/ch%d/link", n), true)
			c.bool(fmt.Sprintf("line/ch%d/linkmaster", n), true)
			c.bool(fmt.Sprintf("line/ch%d/panlinkstate", n), true)
		}
	}
	for _, n := range sortedMixKeys(cfg.Mixes) {
		if mix := cfg.Mixes[n]; mix.Stereo != nil && *mix.Stereo {
			c.bool(fmt.Sprintf("aux/ch%d/link", n), true)
			c.bool(fmt.Sprintf("aux/ch%d/linkmaster", n), true)
			c.bool(fmt.Sprintf("aux/ch%d/panlinkstate", n), true)
		}
	}

	// Phase 3: input patch.
	for _, n := range sortedChannelKeys(cfg.Channels) {
		if ch := cfg.Channels[n]; ch.Patch != nil {
			if err := c.taper(fmt.Sprintf("line/ch%d/adc_src", n), float64(*ch.Patch)); err != nil {
				return nil, err
			}
		}
	}

	// Phase 4: preamp / 48V / HPF.
	for _, n := range sortedChannelKeys(cfg.Channels) {
		ch := cfg.Channels[n]
		if ch.Phantom != nil {
			c.bool(fmt.Sprintf("line/ch%d/48v", n), *ch.Phantom)
		}
		if ch.HPF != nil {
			if err := c.hpf(fmt.Sprintf("line/ch%d/filter/hpf", n), *ch.HPF); err != nil {
				return nil, err
			}
		}
	}

	// Phase 5: assigns (main + fx-send routing enables).
	for _, n := range sortedChannelKeys(cfg.Channels) {
		ch := cfg.Channels[n]
		if ch.Main != nil {
			c.bool(fmt.Sprintf("line/ch%d/lr", n), *ch.Main)
		}
		for _, letter := range sortedStringKeys(ch.FX) {
			num, err := letterToNum(letter)
			if err != nil {
				return nil, fmt.Errorf("boardconfig: channels.%d.fx.%s: %w", n, letter, err)
			}
			c.bool(fmt.Sprintf("line/ch%d/assign_fx%d", n, num), true)
		}
	}

	// Phase 6: levels and sends.
	for _, n := range sortedChannelKeys(cfg.Channels) {
		ch := cfg.Channels[n]
		if ch.Fader != nil {
			if err := c.taper(fmt.Sprintf("line/ch%d/volume", n), *ch.Fader); err != nil {
				return nil, err
			}
		}
		if ch.Mute != nil {
			c.bool(fmt.Sprintf("line/ch%d/mute", n), *ch.Mute)
		}
		for _, name := range sortedSendKeys(ch.Sends) {
			aux, err := c.resolveSend(name)
			if err != nil {
				return nil, fmt.Errorf("boardconfig: channels.%d.sends.%s: %w", n, name, err)
			}
			if err := c.level(fmt.Sprintf("line/ch%d/aux%d", n, aux), ch.Sends[name]); err != nil {
				return nil, err
			}
		}
		for _, letter := range sortedStringKeys(ch.FX) {
			if err := c.level(fmt.Sprintf("line/ch%d/FX%s", n, letter), ch.FX[letter]); err != nil {
				return nil, err
			}
		}
	}

	// Phase 7: mix masters.
	for _, n := range sortedMixKeys(cfg.Mixes) {
		mix := cfg.Mixes[n]
		if mix.Fader != nil {
			if err := c.taper(fmt.Sprintf("aux/ch%d/volume", n), *mix.Fader); err != nil {
				return nil, err
			}
		}
		if mix.Pre != nil {
			wire, err := preToWire(*mix.Pre)
			if err != nil {
				return nil, fmt.Errorf("boardconfig: mixes.%d.pre: %w", n, err)
			}
			c.push(fmt.Sprintf("aux/ch%d/auxpremode", n), wire, *mix.Pre)
		}
	}

	// Phase 8: limiters.
	for _, n := range sortedMixKeys(cfg.Mixes) {
		mix := cfg.Mixes[n]
		if mix.Limiter == nil {
			continue
		}
		if mix.Limiter.On != nil {
			c.bool(fmt.Sprintf("aux/ch%d/limit/limiteron", n), *mix.Limiter.On)
		}
		if mix.Limiter.Threshold != nil {
			if err := c.taper(fmt.Sprintf("aux/ch%d/limit/threshold", n), *mix.Limiter.Threshold); err != nil {
				return nil, err
			}
		}
		if mix.Limiter.Release != nil {
			if err := c.taper(fmt.Sprintf("aux/ch%d/limit/release", n), *mix.Limiter.Release); err != nil {
				return nil, err
			}
		}
	}

	// Phase 9: FX bus type.
	for _, k := range sortedStringKeys(cfg.FX) {
		fx := cfg.FX[k]
		if fx.Type == nil {
			continue
		}
		ch, err := letterToChannel(k)
		if err != nil {
			return nil, fmt.Errorf("boardconfig: fx.%s: %w", k, err)
		}
		wire, err := fxTypeToWire(*fx.Type)
		if err != nil {
			return nil, fmt.Errorf("boardconfig: fx.%s.type: %w", k, err)
		}
		c.push(fmt.Sprintf("fx/ch%d/type", ch), wire, *fx.Type)
	}

	// Phase 10: FX returns (sends + mains mute).
	for _, k := range sortedStringKeys(cfg.FXReturns) {
		fr := cfg.FXReturns[k]
		ch, err := letterToChannel(k)
		if err != nil {
			return nil, fmt.Errorf("boardconfig: fxreturns.%s: %w", k, err)
		}
		for _, name := range sortedSendKeys(fr.Sends) {
			aux, err := c.resolveSend(name)
			if err != nil {
				return nil, fmt.Errorf("boardconfig: fxreturns.%s.sends.%s: %w", k, name, err)
			}
			if err := c.level(fmt.Sprintf("fxreturn/ch%d/aux%d", ch, aux), fr.Sends[name]); err != nil {
				return nil, err
			}
		}
		if fr.Mains != nil {
			muted, err := mainsToMute(*fr.Mains)
			if err != nil {
				return nil, fmt.Errorf("boardconfig: fxreturns.%s.mains: %w", k, err)
			}
			c.bool(fmt.Sprintf("fxreturn/ch%d/mute", ch), muted)
		}
	}

	// Phase 11: raw escape hatch — wire path→value verbatim, no taper.
	for _, path := range sortedStringKeys(cfg.Raw) {
		v := cfg.Raw[path]
		c.push(path, v, v)
	}

	return c.out, nil
}

// compiler accumulates desired writes and resolves send names against the mixes.
type compiler struct {
	out   []Desired
	mixes map[int]Mix
}

func (c *compiler) push(path string, wire, human any) {
	c.out = append(c.out, Desired{Path: path, WireValue: wire, HumanValue: human})
}

func (c *compiler) bool(path string, b bool) { c.push(path, b, b) }
func (c *compiler) str(path, s string)       { c.push(path, s, s) }

// taper converts a human number through the key's taper and read-scale.
func (c *compiler) taper(path string, human float64) error {
	spec, ok := schema.Lookup(path)
	if !ok || spec.Taper == nil {
		return fmt.Errorf("boardconfig: %s: no taper for a human-unit field", path)
	}
	pos, err := spec.Taper.ToWire(human)
	if err != nil {
		return fmt.Errorf("boardconfig: %s: %w", path, err)
	}
	scale := spec.ReadScale
	if scale == 0 {
		scale = 1
	}
	c.push(path, pos*scale, human)
	return nil
}

// level emits a send/fx level in dB, or 0.0 for off.
func (c *compiler) level(path string, lvl Level) error {
	if lvl.Off {
		c.push(path, 0.0, "off")
		return nil
	}
	return c.taper(path, lvl.DB)
}

// hpf emits a high-pass setting: off → 0.0, raw:X → X verbatim, Hz → taper.
func (c *compiler) hpf(path string, h HPF) error {
	switch {
	case h.Off:
		c.push(path, 0.0, "off")
		return nil
	case h.Raw != nil:
		c.push(path, *h.Raw, fmt.Sprintf("raw:%g", *h.Raw))
		return nil
	default:
		return c.taper(path, *h.Hz)
	}
}

// color parses a hex RGB(A) string, appending the default opaque alpha when the
// input is 6 hex digits. The wire value is the 8-digit RGBA string.
func (c *compiler) color(path, hex string, n int) error {
	h := strings.TrimPrefix(hex, "#")
	if !hexColor.MatchString(h) {
		return fmt.Errorf("boardconfig: channels.%d.color: %q is not 6 or 8 hex digits", n, hex)
	}
	if len(h) == 6 {
		h += "ff"
	}
	h = strings.ToLower(h)
	c.push(path, h, strings.ToLower(strings.TrimPrefix(hex, "#")))
	return nil
}

// resolveSend maps a send key to its aux channel number. A key of the form
// "auxN" is that raw aux channel; any other key is a mix name resolved against
// the mixes section — its map key is the aux channel number.
func (c *compiler) resolveSend(key string) (int, error) {
	if m := auxKey.FindStringSubmatch(key); m != nil {
		return strconv.Atoi(m[1])
	}
	for n, mix := range c.mixes {
		if mix.Name != nil && *mix.Name == key {
			return n, nil
		}
	}
	return 0, fmt.Errorf("unknown mix name (not in mixes: and not an auxN form)")
}

var (
	hexColor = regexp.MustCompile(`^[0-9A-Fa-f]{6}([0-9A-Fa-f]{2})?$`)
	auxKey   = regexp.MustCompile(`^aux(\d+)$`)
)

// letterToNum maps a single FX letter A–H to 1–8.
func letterToNum(letter string) (int, error) {
	if len(letter) != 1 || letter[0] < 'A' || letter[0] > 'H' {
		return 0, fmt.Errorf("expected an FX letter A–H, got %q", letter)
	}
	return int(letter[0]-'A') + 1, nil
}

// letterToChannel maps an FX/FX-return letter A–H to its channel number 1–8.
func letterToChannel(letter string) (int, error) { return letterToNum(letter) }

// preToWire maps a monitor-send pre/post mode to its wire enum float. Only pre2
// is confirmed by the board capture (0.5); pre1 and post are assumed endpoints.
func preToWire(pre string) (float64, error) {
	switch pre {
	case "pre1":
		return 0.0, nil
	case "pre2":
		return 0.5, nil
	case "post":
		return 1.0, nil
	default:
		return 0, fmt.Errorf("unknown pre mode %q (want pre1|pre2|post)", pre)
	}
}

// fxTypeToWire maps a named FX type to its wire enum float. Only vintage-plate
// is confirmed by the board capture (0.375); extend as types are calibrated.
func fxTypeToWire(name string) (float64, error) {
	switch name {
	case "vintage-plate":
		return 0.375, nil
	default:
		return 0, fmt.Errorf("unknown FX type %q (only vintage-plate is calibrated)", name)
	}
}

// mainsToMute maps the fxreturn mains setting to a mute bool.
func mainsToMute(v string) (bool, error) {
	switch v {
	case "muted":
		return true, nil
	case "on":
		return false, nil
	default:
		return false, fmt.Errorf("unknown mains value %q (want muted|on)", v)
	}
}

func sortedChannelKeys(m map[int]Channel) []int {
	ks := make([]int, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Ints(ks)
	return ks
}

func sortedMixKeys(m map[int]Mix) []int {
	ks := make([]int, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Ints(ks)
	return ks
}

func sortedSendKeys(m map[string]Level) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func sortedStringKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
