package boardconfig

import (
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/steveclarke/ucmix/internal/schema"
)

// ToConfig reconstructs a declarative [Config] from a raw wire snapshot — the
// inverse of [Compile] over the modeled fields. It walks the snapshot, keeps
// keys the schema knows how to model in a Config, and converts each wire value
// back to its human form (÷ReadScale then taper.FromWire for tapered floats;
// verbatim otherwise), collapsing the sugar Compile expands: the link/stereo
// triples fold back to link:/stereo:, aux send indices to auxN keys, color alpha
// is stripped, and calibrated enums (pre mode, FX type) map back to their names.
//
// It is deliberately lossy. The invariant is round-trip cleanliness, not
// byte-identity: Compile(ToConfig(snapshot)) Diffs clean against snapshot for
// every modeled field. Concretely:
//
//   - Round-trips: channel name/icon/link/patch/48v/main/fader/mute, channel
//     monitor sends (as auxN keys) and FX sends, mix name/stereo/fader/limiter,
//     calibrated pre mode and FX type, FX-return name/sends/mains, HPF.
//   - Dropped: unmodeled keys (they carry no Config field), plus modeled-but-
//     unmodeled-in-Config keys (solo, polarity, pan, preampgain, EQ, comp, and
//     the raw assign/linkmaster/panlinkstate members of a link triple — the
//     latter are re-derived from link/stereo on Compile). Color is dropped too:
//     Compile encodes it as a hex string while the board reports raw RGBA bytes,
//     so a color line cannot Diff-clean — color stays settable via `set` but is
//     not part of the dump round-trip.
//   - Does not round-trip: uncalibrated pre-mode / FX-type enum values (dropped,
//     since there is no name for them); duplicate mix usernames (Load rejects
//     them).
func ToConfig(snapshot map[string]any) (Config, error) {
	acc := newConfigAccumulator()
	for path, val := range snapshot {
		if _, known := schema.Lookup(path); !known {
			continue // unmodeled key: not part of a declarative config
		}
		switch {
		case strings.HasPrefix(path, "line/ch"):
			acc.channelKey(path, val)
		case strings.HasPrefix(path, "aux/ch"):
			acc.mixKey(path, val)
		case strings.HasPrefix(path, "fxreturn/ch"):
			acc.fxReturnKey(path, val)
		case strings.HasPrefix(path, "fx/ch"):
			acc.fxKey(path, val)
		}
	}
	return acc.config(), nil
}

// configAccumulator gathers per-entity fields as pointer structs so many keys
// can populate one channel/mix/return, then flattens to value maps.
type configAccumulator struct {
	channels  map[int]*Channel
	mixes     map[int]*Mix
	fx        map[string]*FXBus
	fxReturns map[string]*FXReturn
}

func newConfigAccumulator() *configAccumulator {
	return &configAccumulator{
		channels:  map[int]*Channel{},
		mixes:     map[int]*Mix{},
		fx:        map[string]*FXBus{},
		fxReturns: map[string]*FXReturn{},
	}
}

func (a *configAccumulator) channel(n int) *Channel {
	if a.channels[n] == nil {
		a.channels[n] = &Channel{}
	}
	return a.channels[n]
}

func (a *configAccumulator) mix(n int) *Mix {
	if a.mixes[n] == nil {
		a.mixes[n] = &Mix{}
	}
	return a.mixes[n]
}

func (a *configAccumulator) fxReturn(letter string) *FXReturn {
	if a.fxReturns[letter] == nil {
		a.fxReturns[letter] = &FXReturn{}
	}
	return a.fxReturns[letter]
}

func (a *configAccumulator) channelKey(path string, val any) {
	n, rest, ok := chIndex(path, "line/ch")
	if !ok {
		return
	}
	ch := a.channel(n)
	switch rest {
	case "username":
		ch.Name = strPtr(val)
	case "iconid":
		ch.Icon = strPtr(val)
	case "48v":
		ch.Phantom = boolPtr(val)
	case "lr":
		ch.Main = boolPtr(val)
	case "mute":
		ch.Mute = boolPtr(val)
	case "volume":
		if f, ok := humanFloat(path, val); ok {
			ch.Fader = &f
		}
	case "adc_src":
		if f, ok := humanFloat(path, val); ok {
			p := int(math.Round(f))
			ch.Patch = &p
		}
	case "filter/hpf":
		ch.HPF = hpfFrom(val)
	case "link":
		if truthy(val) && n%2 == 1 { // link lives on the odd pair master
			t := true
			ch.Link = &t
		}
	default:
		// Sends (auxN) and FX sends (FX letter). Everything else —
		// linkmaster/panlinkstate/assign_fx*/solo/polarity/pan/preampgain/eq/comp
		// — is dropped: re-derived from the sugar or not modeled in Config.
		if m := auxSendRe.FindStringSubmatch(rest); m != nil {
			if ch.Sends == nil {
				ch.Sends = map[string]Level{}
			}
			ch.Sends["aux"+m[1]] = levelFrom(path, val)
		} else if m := fxSendRe.FindStringSubmatch(rest); m != nil {
			if ch.FX == nil {
				ch.FX = map[string]Level{}
			}
			ch.FX[m[1]] = levelFrom(path, val)
		}
	}
}

func (a *configAccumulator) mixKey(path string, val any) {
	n, rest, ok := chIndex(path, "aux/ch")
	if !ok {
		return
	}
	mix := a.mix(n)
	switch rest {
	case "username":
		mix.Name = strPtr(val)
	case "volume":
		if f, ok := humanFloat(path, val); ok {
			mix.Fader = &f
		}
	case "link":
		if truthy(val) && n%2 == 1 { // stereo lives on the odd pair master
			t := true
			mix.Stereo = &t
		}
	case "auxpremode":
		if p, ok := preFrom(val); ok {
			mix.Pre = &p
		}
	case "limit/limiteron":
		limiter(mix).On = boolPtr(val)
	case "limit/threshold":
		if f, ok := humanFloat(path, val); ok {
			limiter(mix).Threshold = &f
		}
	case "limit/release":
		if f, ok := humanFloat(path, val); ok {
			limiter(mix).Release = &f
		}
	}
}

func (a *configAccumulator) fxReturnKey(path string, val any) {
	n, rest, ok := chIndex(path, "fxreturn/ch")
	if !ok {
		return
	}
	letter := channelToLetter(n)
	if letter == "" {
		return
	}
	fr := a.fxReturn(letter)
	switch rest {
	case "username":
		fr.Name = strPtr(val)
	case "mute":
		s := "on"
		if truthy(val) {
			s = "muted"
		}
		fr.Mains = &s
	default:
		if m := auxSendRe.FindStringSubmatch(rest); m != nil {
			if fr.Sends == nil {
				fr.Sends = map[string]Level{}
			}
			fr.Sends["aux"+m[1]] = levelFrom(path, val)
		}
	}
}

func (a *configAccumulator) fxKey(path string, val any) {
	n, rest, ok := chIndex(path, "fx/ch")
	if !ok || rest != "type" {
		return
	}
	name, ok := fxTypeFrom(val)
	if !ok {
		return // uncalibrated type value: no name to emit
	}
	letter := channelToLetter(n)
	if letter == "" {
		return
	}
	a.fx[letter] = &FXBus{Type: &name}
}

// config flattens the pointer accumulators into a value Config.
func (a *configAccumulator) config() Config {
	cfg := Config{Version: 1}
	if len(a.channels) > 0 {
		cfg.Channels = make(map[int]Channel, len(a.channels))
		for k, v := range a.channels {
			cfg.Channels[k] = *v
		}
	}
	if len(a.mixes) > 0 {
		cfg.Mixes = make(map[int]Mix, len(a.mixes))
		for k, v := range a.mixes {
			cfg.Mixes[k] = *v
		}
	}
	if len(a.fx) > 0 {
		cfg.FX = make(map[string]FXBus, len(a.fx))
		for k, v := range a.fx {
			cfg.FX[k] = *v
		}
	}
	if len(a.fxReturns) > 0 {
		cfg.FXReturns = make(map[string]FXReturn, len(a.fxReturns))
		for k, v := range a.fxReturns {
			cfg.FXReturns[k] = *v
		}
	}
	return cfg
}

// limiter lazily creates the mix limiter block.
func limiter(m *Mix) *Limiter {
	if m.Limiter == nil {
		m.Limiter = &Limiter{}
	}
	return m.Limiter
}

var (
	auxSendRe = regexp.MustCompile(`^aux(\d+)$`)
	fxSendRe  = regexp.MustCompile(`^FX([A-H])$`)
)

// chIndex splits "prefix<N>/<rest>" into the channel number and the remaining
// key. Returns ok=false when the path does not match the shape.
func chIndex(path, prefix string) (n int, rest string, ok bool) {
	if !strings.HasPrefix(path, prefix) {
		return 0, "", false
	}
	tail := path[len(prefix):]
	slash := strings.IndexByte(tail, '/')
	if slash < 0 {
		return 0, "", false
	}
	num, err := strconv.Atoi(tail[:slash])
	if err != nil {
		return 0, "", false
	}
	return num, tail[slash+1:], true
}

// humanFloat converts a wire value at path back to its human unit using the
// key's schema (÷ReadScale then taper). Keys without a taper return raw/scale.
func humanFloat(path string, val any) (float64, bool) {
	f, ok := asFloat(val)
	if !ok {
		return 0, false
	}
	spec, known := schema.Lookup(path)
	if !known {
		return f, true
	}
	scale := spec.ReadScale
	if scale == 0 {
		scale = 1
	}
	if spec.Taper != nil {
		return round1(spec.Taper.FromWire(f / scale)), true
	}
	return f / scale, true
}

// round1 rounds a humanized value to one decimal place. Taper inversion leaves
// float noise (-6 dB reads back as -6.0000016); rounding keeps the dumped config
// readable and stays far inside every Diff tolerance (dB 0.5, Hz 5, ms 5).
func round1(f float64) float64 { return math.Round(f*10) / 10 }

// levelFrom inverts a send/FX level. A wire 0.0 taper-inverts to the bottom dB
// (the taper's floor), which Compile re-tapers back to 0.0 — so an off level
// round-trips as its floor dB rather than the literal off.
func levelFrom(path string, val any) Level {
	if f, ok := humanFloat(path, val); ok {
		return Level{DB: f}
	}
	return Level{Off: true}
}

// hpfFrom inverts a high-pass value. Exactly 0 is off; any other position is
// emitted as a raw: escape hatch (the Hz curve is undecoded, so a raw position
// is the only form that round-trips through the passthrough taper cleanly).
func hpfFrom(val any) *HPF {
	f, ok := asFloat(val)
	if !ok {
		return nil
	}
	if f == 0 {
		return &HPF{Off: true}
	}
	r := f
	return &HPF{Raw: &r}
}

// preFrom inverts the aux pre/post enum. Only calibrated positions map back to a
// name; anything else is dropped.
func preFrom(val any) (string, bool) {
	f, ok := asFloat(val)
	if !ok {
		return "", false
	}
	switch {
	case math.Abs(f-0.0) < 1e-6:
		return "pre1", true
	case math.Abs(f-0.5) < 1e-6:
		return "pre2", true
	case math.Abs(f-1.0) < 1e-6:
		return "post", true
	default:
		return "", false
	}
}

// fxTypeFrom inverts the FX type enum. Only calibrated positions map back to a
// name; anything else is dropped.
func fxTypeFrom(val any) (string, bool) {
	f, ok := asFloat(val)
	if !ok {
		return "", false
	}
	if math.Abs(f-0.375) < 1e-6 {
		return "vintage-plate", true
	}
	return "", false
}

// truthy reports whether a wire value is on (non-zero / true).
func truthy(val any) bool {
	f, ok := asFloat(val)
	return ok && f != 0
}

// strPtr returns a pointer to the string form of a snapshot value, or nil when
// it is not a string.
func strPtr(val any) *string {
	if s, ok := val.(string); ok {
		return &s
	}
	return nil
}

// boolPtr returns a pointer to the bool form of a wire value (non-zero = true).
func boolPtr(val any) *bool {
	if _, ok := asFloat(val); !ok {
		if b, ok := val.(bool); ok {
			return &b
		}
		return nil
	}
	b := truthy(val)
	return &b
}

// channelToLetter maps an FX/FX-return channel number 1..8 to its letter A..H.
func channelToLetter(n int) string {
	if n < 1 || n > 8 {
		return ""
	}
	return string(rune('A' + n - 1))
}
