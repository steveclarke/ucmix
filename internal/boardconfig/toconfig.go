package boardconfig

import "errors"

// ErrNotImplemented is returned by [ToConfig] until the inverse-of-Compile
// dumper lands.
var ErrNotImplemented = errors.New("boardconfig: ToConfig not implemented")

// ToConfig is the inverse of [Compile] for modeled fields — it would read a live
// board snapshot and reconstruct the declarative [Config], backing a future
// `ucmix dump --as-config`. Round-trip fidelity (Compile → apply-to-map →
// ToConfig verifies clean) is the intended invariant.
//
// TODO(dump): implement the snapshot → Config inversion. It must group wire keys
// back into channels/mixes/fx/fxreturns, collapse the link/stereo triples and
// color alpha back to their sugar forms, resolve aux send indices back to mix
// names, and humanize tapered floats. Deferred so Load/Compile/Diff (the
// verify/apply core) ship first.
func ToConfig(snapshot map[string]any) (Config, error) {
	return Config{}, ErrNotImplemented
}
