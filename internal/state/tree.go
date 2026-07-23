// Package state holds the mixer's live path→value tree.
//
// A Tree is a flat, concurrency-safe map from a UCNET path (stored
// "/"-delimited exactly as the wire sends it, never nested) to its current
// value. It merges the ZB snapshot (a full replace-all) with the live PV/PC/PS
// deltas that follow. It knows nothing about what any key means — humanizing,
// tapers, and read-scale quirks live in the schema layer above it.
//
// The tree is pure: no I/O and no goroutines. Its only concurrency machinery is
// an RWMutex guarding the map, so many readers or one writer may touch it at
// once.
package state

import (
	"sort"
	"strings"
	"sync"
)

// Tree is a concurrency-safe flat path→value map.
type Tree struct {
	mu sync.RWMutex
	m  map[string]any
}

// NewTree returns an empty Tree ready for use.
func NewTree() *Tree {
	return &Tree{m: make(map[string]any)}
}

// LoadSnapshot replaces the entire contents of the tree with a deep copy of m
// (ZB is a full snapshot, so it clears everything already present). A nil or
// empty map yields an empty tree. The caller may mutate m afterwards without
// affecting the tree.
func (t *Tree) LoadSnapshot(m map[string]any) {
	next := make(map[string]any, len(m))
	for k, v := range m {
		next[k] = deepCopy(v)
	}
	t.mu.Lock()
	t.m = next
	t.mu.Unlock()
}

// Apply overlays a single PV/PC/PS delta, setting one path to a deep copy of v
// (overwriting any existing value). The caller may mutate v afterwards without
// affecting the tree.
func (t *Tree) Apply(path string, v any) {
	cp := deepCopy(v)
	t.mu.Lock()
	t.m[path] = cp
	t.mu.Unlock()
}

// Get returns the value at path and whether it was present. The returned value
// shares structure with the tree; callers that intend to mutate nested
// maps/slices should use Snapshot instead.
func (t *Tree) Get(path string) (any, bool) {
	t.mu.RLock()
	v, ok := t.m[path]
	t.mu.RUnlock()
	return v, ok
}

// Snapshot returns a deep copy of the whole tree. Mutating the returned map, or
// any nested map/slice within it, cannot affect the tree.
func (t *Tree) Snapshot() map[string]any {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(map[string]any, len(t.m))
	for k, v := range t.m {
		out[k] = deepCopy(v)
	}
	return out
}

// Paths returns a sorted slice of every key that equals prefix or begins with
// it, matched as a raw string prefix (not segment-aware): prefix "line/ch1"
// matches both "line/ch1/name" and "line/ch10/name". Pass "" to get all keys.
// The result is never nil.
func (t *Tree) Paths(prefix string) []string {
	t.mu.RLock()
	out := make([]string, 0, len(t.m))
	for k := range t.m {
		if prefix == "" || strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	t.mu.RUnlock()
	sort.Strings(out)
	return out
}

// deepCopy recursively copies map[string]any and []any values so the result
// shares no mutable structure with the input. Scalars (and any other type) are
// returned as-is, copied by value, which is correct for the immutable scalars
// UCNET carries: float32, bool, string, and numeric kinds.
func deepCopy(v any) any {
	switch t := v.(type) {
	case map[string]any:
		cp := make(map[string]any, len(t))
		for k, val := range t {
			cp[k] = deepCopy(val)
		}
		return cp
	case []any:
		cp := make([]any, len(t))
		for i, val := range t {
			cp[i] = deepCopy(val)
		}
		return cp
	default:
		return v
	}
}
