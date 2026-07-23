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
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the whole declarative board config. Every field is optional; only
// what is declared is compiled and verified.
type Config struct {
	Version   int                 `yaml:"version"`
	Mixer     Mixer               `yaml:"mixer"`
	Channels  map[int]Channel     `yaml:"channels"`
	Mixes     map[int]Mix         `yaml:"mixes"`
	FX        map[string]FXBus    `yaml:"fx"`
	FXReturns map[string]FXReturn `yaml:"fxreturns"`
	Raw       map[string]any      `yaml:"raw"`
}

// Mixer is the optional model guard. A non-empty Model makes apply/verify abort
// on a board-model mismatch (enforced by the caller, not by this package).
type Mixer struct {
	Model string `yaml:"model"`
}

// Channel is one input strip (line/chN). Pointer fields distinguish "declared"
// from "absent": a nil pointer means the field was omitted and is not compiled.
type Channel struct {
	Name    *string          `yaml:"name"`
	Icon    *string          `yaml:"icon"`
	Color   *string          `yaml:"color"`
	Link    *bool            `yaml:"link"`
	Patch   *int             `yaml:"patch"`
	Phantom *bool            `yaml:"phantom"`
	HPF     *HPF             `yaml:"hpf"`
	Main    *bool            `yaml:"main"`
	Fader   *float64         `yaml:"fader"`
	Mute    *bool            `yaml:"mute"`
	Sends   map[string]Level `yaml:"sends"`
	FX      map[string]Level `yaml:"fx"`
}

// Mix is one monitor-mix master (aux/chN). The map key it is stored under is the
// aux channel number; odd keys are stereo-pair masters.
type Mix struct {
	Name    *string  `yaml:"name"`
	Stereo  *bool    `yaml:"stereo"`
	Fader   *float64 `yaml:"fader"`
	Pre     *string  `yaml:"pre"`
	Limiter *Limiter `yaml:"limiter"`
}

// Limiter is a mix limiter. The "on" YAML key is a reserved-looking word but a
// plain bool here.
type Limiter struct {
	On        *bool    `yaml:"on"`
	Threshold *float64 `yaml:"threshold"`
	Release   *float64 `yaml:"release"`
}

// FXBus is one internal FX processor (fx/chN), keyed A–H in the config.
type FXBus struct {
	Type *string `yaml:"type"`
}

// FXReturn is one FX return strip (fxreturn/chN), keyed A–H in the config.
type FXReturn struct {
	Name  *string          `yaml:"name"`
	Sends map[string]Level `yaml:"sends"`
	Mains *string          `yaml:"mains"` // "muted" mutes the return in main LR
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

// HPF is a high-pass filter setting: a frequency in Hz, off, or a raw:X wire
// escape hatch (used until the Hz↔position curve is calibrated).
type HPF struct {
	Off bool
	Raw *float64 // wire position, verbatim
	Hz  *float64
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
