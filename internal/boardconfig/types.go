// Package boardconfig is the declarative "board as code" layer: it parses a
// sparse YAML config in human units (dB, Hz, physical input numbers, color hex,
// named FX types), compiles it to an ordered set of desired wire writes, and
// diffs that desired set against a live board snapshot.
//
// Three operations make up the public surface:
//
//   - [Load] parses and validates the YAML. Unknown fields are errors (a typo
//     must never silently no-op), and structural rules (even-channel link,
//     duplicate mix names, out-of-range numbers) are checked with clear,
//     field-named errors.
//   - [Compile] lowers a [Config] to an ordered []Desired using the schema table
//     and tapers. Sugar (link triples, name→aux-index sends, fx assigns, stereo
//     mixes, color alpha) expands here.
//   - [Diff] compares a desired set against a snapshot map, humanizing both sides
//     and applying a per-taper tolerance in human units (wire floats quantize, so
//     exact equality is wrong by construction).
//
// Only declared fields participate — the config is a statement of intent, not a
// full state dump. See knowledge/ucnet-studiolive/ucmix-design.md §5.
package boardconfig

import (
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the whole declarative board config. Every field is optional; only
// what is declared is compiled and verified. The omitempty tags keep a
// round-tripped dump (ToConfig -> YAML) free of null/empty noise.
type Config struct {
	Version   int                 `yaml:"version,omitempty"`
	Mixer     Mixer               `yaml:"mixer,omitempty"`
	Channels  map[int]Channel     `yaml:"channels,omitempty"`
	Mixes     map[int]Mix         `yaml:"mixes,omitempty"`
	FX        map[string]FXBus    `yaml:"fx,omitempty"`
	FXReturns map[string]FXReturn `yaml:"fxreturns,omitempty"`
	Raw       map[string]any      `yaml:"raw,omitempty"`
}

// Mixer is the optional model guard. A non-empty Model makes apply/verify abort
// on a board-model mismatch (enforced by the caller, not by this package).
type Mixer struct {
	Model string `yaml:"model,omitempty"`
}

// Channel is one input strip (line/chN). Pointer fields distinguish "declared"
// from "absent": a nil pointer means the field was omitted and is not compiled.
type Channel struct {
	Name    *string          `yaml:"name,omitempty"`
	Icon    *string          `yaml:"icon,omitempty"`
	Color   *string          `yaml:"color,omitempty"`
	Link    *bool            `yaml:"link,omitempty"`
	Patch   *int             `yaml:"patch,omitempty"`
	Phantom *bool            `yaml:"phantom,omitempty"`
	HPF     *HPF             `yaml:"hpf,omitempty"`
	Main    *bool            `yaml:"main,omitempty"`
	Fader   *float64         `yaml:"fader,omitempty"`
	Mute    *bool            `yaml:"mute,omitempty"`
	Sends   map[string]Level `yaml:"sends,omitempty"`
	FX      map[string]Level `yaml:"fx,omitempty"`
}

// Mix is one monitor-mix master (aux/chN). The map key it is stored under is the
// aux channel number; odd keys are stereo-pair masters.
type Mix struct {
	Name    *string  `yaml:"name,omitempty"`
	Stereo  *bool    `yaml:"stereo,omitempty"`
	Fader   *float64 `yaml:"fader,omitempty"`
	Pre     *string  `yaml:"pre,omitempty"`
	Limiter *Limiter `yaml:"limiter,omitempty"`
}

// Limiter is a mix limiter. The "on" YAML key is a reserved-looking word but a
// plain bool here.
type Limiter struct {
	On        *bool    `yaml:"on,omitempty"`
	Threshold *float64 `yaml:"threshold,omitempty"`
	Release   *float64 `yaml:"release,omitempty"`
}

// FXBus is one internal FX processor (fx/chN), keyed A–H in the config.
type FXBus struct {
	Type *string `yaml:"type,omitempty"`
}

// FXReturn is one FX return strip (fxreturn/chN), keyed A–H in the config.
type FXReturn struct {
	Name  *string          `yaml:"name,omitempty"`
	Sends map[string]Level `yaml:"sends,omitempty"`
	Mains *string          `yaml:"mains,omitempty"` // "muted" mutes the return in main LR
}

// Level is a send or fader level in dB, or the literal off. YAML 1.2 parses a
// bare off as the string "off", which this unmarshaler recognizes.
type Level struct {
	Off bool
	DB  float64
}

// UnmarshalYAML decodes a scalar as either "off" or a dB number.
func (l *Level) UnmarshalYAML(node *yaml.Node) error {
	if node.Value == "off" {
		l.Off = true
		return nil
	}
	return node.Decode(&l.DB)
}

// MarshalYAML emits a Level as the scalar Load expects: the string "off" or the
// bare dB number. The value receiver is required — Sends/FX map values are not
// addressable, so a pointer-receiver marshaler would silently not fire.
func (l Level) MarshalYAML() (any, error) {
	if l.Off {
		return "off", nil
	}
	return l.DB, nil
}

// HPF is a high-pass filter setting: a frequency in Hz, off, or a raw:X wire
// escape hatch (used until the Hz↔position curve is calibrated).
type HPF struct {
	Off bool
	Raw *float64 // wire position, verbatim
	Hz  *float64
}

// MarshalYAML emits an HPF as the scalar Load expects: "off", "raw:<float>", or
// the bare Hz number. Value receiver so it fires for *HPF fields on marshal.
func (h HPF) MarshalYAML() (any, error) {
	switch {
	case h.Off:
		return "off", nil
	case h.Raw != nil:
		return fmt.Sprintf("raw:%g", *h.Raw), nil
	case h.Hz != nil:
		return *h.Hz, nil
	default:
		return nil, nil
	}
}

// UnmarshalYAML decodes a scalar as "off", "raw:<float>", or a Hz number.
func (h *HPF) UnmarshalYAML(node *yaml.Node) error {
	s := node.Value
	switch {
	case s == "off":
		h.Off = true
		return nil
	case strings.HasPrefix(s, "raw:"):
		f, err := strconv.ParseFloat(strings.TrimPrefix(s, "raw:"), 64)
		if err != nil {
			return err
		}
		h.Raw = &f
		return nil
	default:
		var hz float64
		if err := node.Decode(&hz); err != nil {
			return err
		}
		h.Hz = &hz
		return nil
	}
}
