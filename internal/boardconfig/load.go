package boardconfig

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// Human-unit bounds used for early, field-named validation in [Load]. They
// mirror the calibrated taper ranges (taper.Fader, taper.LimiterThresh,
// taper.InputPatch); Compile also surfaces taper.ErrOverRange defensively, but
// Load owns the clear messages. Hz bounds are a boardconfig-level sanity check —
// the HPF taper is a passthrough stub and cannot range-check on its own.
const (
	faderMinDB = -84.0
	faderMaxDB = 10.0
	limitMinDB = -28.0
	limitMaxDB = 0.0
	patchMin   = 0
	patchMax   = 32
	hpfMinHz   = 20.0
	hpfMaxHz   = 20000.0
)

// Load parses and validates a declarative board config. Unknown fields are
// errors (KnownFields), so a typo cannot silently no-op. Structural rules —
// link only on odd channels, stereo only on odd mixes, unique mix names, and
// in-range dB/Hz/patch numbers — are checked with errors naming the offending
// field.
func Load(data []byte) (Config, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)

	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("boardconfig: parse: %w", err)
	}
	if err := validate(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func validate(cfg Config) error {
	for n, ch := range cfg.Channels {
		if err := validateChannel(n, ch); err != nil {
			return err
		}
	}
	if err := validateMixes(cfg.Mixes); err != nil {
		return err
	}
	return nil
}

func validateChannel(n int, ch Channel) error {
	if ch.Link != nil && *ch.Link && n%2 == 0 {
		return fmt.Errorf("boardconfig: channels.%d.link: link is only valid on odd channels (the pair master)", n)
	}
	if ch.Patch != nil && (*ch.Patch < patchMin || *ch.Patch > patchMax) {
		return fmt.Errorf("boardconfig: channels.%d.patch: %d out of range %d..%d", n, *ch.Patch, patchMin, patchMax)
	}
	if ch.Fader != nil {
		if err := checkDB(*ch.Fader, faderMinDB, faderMaxDB, fmt.Sprintf("channels.%d.fader", n)); err != nil {
			return err
		}
	}
	for name, lvl := range ch.Sends {
		if !lvl.Off {
			if err := checkDB(lvl.DB, faderMinDB, faderMaxDB, fmt.Sprintf("channels.%d.sends.%s", n, name)); err != nil {
				return err
			}
		}
	}
	for name, lvl := range ch.FX {
		if !lvl.Off {
			if err := checkDB(lvl.DB, faderMinDB, faderMaxDB, fmt.Sprintf("channels.%d.fx.%s", n, name)); err != nil {
				return err
			}
		}
	}
	if ch.HPF != nil && ch.HPF.Hz != nil {
		if hz := *ch.HPF.Hz; hz < hpfMinHz || hz > hpfMaxHz {
			return fmt.Errorf("boardconfig: channels.%d.hpf: %g Hz out of range %g..%g", n, hz, hpfMinHz, hpfMaxHz)
		}
	}
	return nil
}

func validateMixes(mixes map[int]Mix) error {
	seen := make(map[string]int)
	for n, mix := range mixes {
		if mix.Stereo != nil && *mix.Stereo && n%2 == 0 {
			return fmt.Errorf("boardconfig: mixes.%d.stereo: stereo is only valid on odd mixes (the pair master)", n)
		}
		if mix.Name != nil {
			if prev, ok := seen[*mix.Name]; ok {
				return fmt.Errorf("boardconfig: mixes.%d.name: duplicate mix name %q (also mixes.%d)", n, *mix.Name, prev)
			}
			seen[*mix.Name] = n
		}
		if mix.Fader != nil {
			if err := checkDB(*mix.Fader, faderMinDB, faderMaxDB, fmt.Sprintf("mixes.%d.fader", n)); err != nil {
				return err
			}
		}
		if mix.Limiter != nil && mix.Limiter.Threshold != nil {
			if err := checkDB(*mix.Limiter.Threshold, limitMinDB, limitMaxDB, fmt.Sprintf("mixes.%d.limiter.threshold", n)); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkDB(v, min, max float64, field string) error {
	if v < min || v > max {
		return fmt.Errorf("boardconfig: %s: %g dB out of range %g..%g", field, v, min, max)
	}
	return nil
}
