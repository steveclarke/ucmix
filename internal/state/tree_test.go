package state

import (
	"fmt"
	"reflect"
	"sort"
	"sync"
	"testing"
)

func TestSnapshotThenDeltaMerge(t *testing.T) {
	tests := []struct {
		name     string
		snapshot map[string]any
		applies  []struct {
			path string
			v    any
		}
		want map[string]any
	}{
		{
			name:     "empty tree",
			snapshot: nil,
			want:     map[string]any{},
		},
		{
			name: "snapshot only",
			snapshot: map[string]any{
				"line/ch1/volume": float32(0.75),
				"line/ch1/name":   "Drums",
			},
			want: map[string]any{
				"line/ch1/volume": float32(0.75),
				"line/ch1/name":   "Drums",
			},
		},
		{
			name: "delta overrides existing path",
			snapshot: map[string]any{
				"line/ch1/volume": float32(0.75),
			},
			applies: []struct {
				path string
				v    any
			}{
				{"line/ch1/volume", float32(0.5)},
			},
			want: map[string]any{
				"line/ch1/volume": float32(0.5),
			},
		},
		{
			name: "delta adds new path",
			snapshot: map[string]any{
				"line/ch1/volume": float32(0.75),
			},
			applies: []struct {
				path string
				v    any
			}{
				{"line/ch1/mute", true},
			},
			want: map[string]any{
				"line/ch1/volume": float32(0.75),
				"line/ch1/mute":   true,
			},
		},
		{
			name: "last write wins",
			applies: []struct {
				path string
				v    any
			}{
				{"line/ch1/name", "A"},
				{"line/ch1/name", "B"},
				{"line/ch1/name", "C"},
			},
			want: map[string]any{
				"line/ch1/name": "C",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := NewTree()
			tr.LoadSnapshot(tt.snapshot)
			for _, a := range tt.applies {
				tr.Apply(a.path, a.v)
			}
			got := tr.Snapshot()
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Snapshot() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestLoadSnapshotReplacesAll(t *testing.T) {
	tr := NewTree()
	tr.LoadSnapshot(map[string]any{
		"line/ch1/name": "Drums",
		"line/ch2/name": "Bass",
	})
	// Second snapshot must clear the first entirely.
	tr.LoadSnapshot(map[string]any{
		"line/ch3/name": "Vox",
	})

	if _, ok := tr.Get("line/ch1/name"); ok {
		t.Error("line/ch1/name still present after replacing snapshot")
	}
	if v, ok := tr.Get("line/ch3/name"); !ok || v != "Vox" {
		t.Errorf("line/ch3/name = %v, %v; want Vox, true", v, ok)
	}
}

func TestGet(t *testing.T) {
	tr := NewTree()
	tr.LoadSnapshot(map[string]any{"line/ch1/volume": float32(0.75)})
	tr.Apply("line/ch1/mute", true)

	tests := []struct {
		name    string
		path    string
		wantVal any
		wantOK  bool
	}{
		{"from snapshot", "line/ch1/volume", float32(0.75), true},
		{"from delta", "line/ch1/mute", true, true},
		{"missing", "line/ch99/volume", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := tr.Get(tt.path)
			if ok != tt.wantOK || (ok && !reflect.DeepEqual(got, tt.wantVal)) {
				t.Errorf("Get(%q) = %v, %v; want %v, %v", tt.path, got, ok, tt.wantVal, tt.wantOK)
			}
		})
	}
}

func TestSnapshotIsolation(t *testing.T) {
	tr := NewTree()
	tr.LoadSnapshot(map[string]any{
		"scalar":    float32(0.5),
		"nestedMap": map[string]any{"a": 1, "deep": map[string]any{"x": "orig"}},
		"nestedSlc": []any{1, 2, map[string]any{"k": "orig"}},
		"strKey":    "hello",
	})

	snap := tr.Snapshot()

	// Mutate every level of the returned copy.
	snap["scalar"] = float32(0.9)
	snap["newkey"] = "added"
	snap["nestedMap"].(map[string]any)["a"] = 999
	snap["nestedMap"].(map[string]any)["deep"].(map[string]any)["x"] = "mutated"
	snap["nestedSlc"].([]any)[0] = 111
	snap["nestedSlc"].([]any)[2].(map[string]any)["k"] = "mutated"

	// The tree must be untouched.
	if v, _ := tr.Get("scalar"); v != float32(0.5) {
		t.Errorf("scalar mutated in tree: %v", v)
	}
	if _, ok := tr.Get("newkey"); ok {
		t.Error("newkey leaked into tree")
	}
	nm, _ := tr.Get("nestedMap")
	nmm := nm.(map[string]any)
	if nmm["a"] != 1 {
		t.Errorf("nestedMap[a] mutated in tree: %v", nmm["a"])
	}
	if nmm["deep"].(map[string]any)["x"] != "orig" {
		t.Errorf("nestedMap.deep.x mutated in tree: %v", nmm["deep"].(map[string]any)["x"])
	}
	ns, _ := tr.Get("nestedSlc")
	nss := ns.([]any)
	if nss[0] != 1 {
		t.Errorf("nestedSlc[0] mutated in tree: %v", nss[0])
	}
	if nss[2].(map[string]any)["k"] != "orig" {
		t.Errorf("nestedSlc[2].k mutated in tree: %v", nss[2].(map[string]any)["k"])
	}
}

func TestLoadSnapshotCopiesInput(t *testing.T) {
	// Mutating the caller's map after LoadSnapshot must not affect the tree.
	src := map[string]any{
		"line/ch1/name": "Drums",
		"nested":        map[string]any{"a": 1},
	}
	tr := NewTree()
	tr.LoadSnapshot(src)

	src["line/ch1/name"] = "Changed"
	src["nested"].(map[string]any)["a"] = 999

	if v, _ := tr.Get("line/ch1/name"); v != "Drums" {
		t.Errorf("caller mutation leaked: got %v", v)
	}
	n, _ := tr.Get("nested")
	if n.(map[string]any)["a"] != 1 {
		t.Errorf("caller nested mutation leaked: got %v", n.(map[string]any)["a"])
	}
}

func TestApplyCopiesInput(t *testing.T) {
	tr := NewTree()
	m := map[string]any{"a": 1}
	tr.Apply("key", m)
	m["a"] = 999

	got, _ := tr.Get("key")
	if got.(map[string]any)["a"] != 1 {
		t.Errorf("Apply did not copy nested value: got %v", got.(map[string]any)["a"])
	}
}

func TestPaths(t *testing.T) {
	tr := NewTree()
	tr.LoadSnapshot(map[string]any{
		"line/ch1/volume": float32(0.5),
		"line/ch1/name":   "Drums",
		"line/ch2/name":   "Bass",
		"aux/ch1/name":    "Mon",
	})

	tests := []struct {
		name   string
		prefix string
		want   []string
	}{
		{
			name:   "empty prefix returns all sorted",
			prefix: "",
			want: []string{
				"aux/ch1/name",
				"line/ch1/name",
				"line/ch1/volume",
				"line/ch2/name",
			},
		},
		{
			name:   "prefix line",
			prefix: "line/",
			want: []string{
				"line/ch1/name",
				"line/ch1/volume",
				"line/ch2/name",
			},
		},
		{
			name:   "prefix line/ch1",
			prefix: "line/ch1",
			want: []string{
				"line/ch1/name",
				"line/ch1/volume",
			},
		},
		{
			name:   "exact key as prefix",
			prefix: "aux/ch1/name",
			want:   []string{"aux/ch1/name"},
		},
		{
			name:   "no match",
			prefix: "nope/",
			want:   []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tr.Paths(tt.prefix)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Paths(%q) = %v, want %v", tt.prefix, got, tt.want)
			}
			// Confirm sorted.
			if !sort.StringsAreSorted(got) {
				t.Errorf("Paths(%q) not sorted: %v", tt.prefix, got)
			}
		})
	}
}

func TestConcurrentAccess(t *testing.T) {
	tr := NewTree()
	tr.LoadSnapshot(map[string]any{"seed": 0})

	const workers = 8
	const iters = 500
	var wg sync.WaitGroup

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				path := fmt.Sprintf("line/ch%d/volume", w)
				switch i % 4 {
				case 0:
					tr.Apply(path, float32(i))
				case 1:
					tr.Get(path)
				case 2:
					tr.Snapshot()
				case 3:
					tr.Paths("line/")
				}
			}
		}(w)
	}
	wg.Wait()
}
