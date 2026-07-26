package ucmix

import (
	"context"
	"testing"
	"time"

	"github.com/steveclarke/ucmix/internal/fakeboard"
	"github.com/steveclarke/ucmix/internal/schema"
)

// Every tile the public table names must be a modeled, writable on/off key.
// Filters reads and SetFilter writes through the schema, so a tile missing from
// it would read raw and write unencoded.
func TestFilterTilesAreModeledToggles(t *testing.T) {
	for _, g := range FilterGroups() {
		tiles, err := FilterTiles(g)
		if err != nil {
			t.Fatalf("FilterTiles(%q): %v", g, err)
		}
		if len(tiles) == 0 {
			t.Fatalf("FilterTiles(%q) is empty", g)
		}
		for _, tile := range tiles {
			path, err := FilterPath(g, tile)
			if err != nil {
				t.Fatalf("FilterPath(%q, %q): %v", g, tile, err)
			}
			spec, ok := schema.Lookup(path)
			if !ok {
				t.Errorf("%s/%s: %q is not in the schema", g, tile, path)
				continue
			}
			if spec.Kind != schema.KindBool || !spec.Writable {
				t.Errorf("%s/%s: %q is kind %v writable=%v; want a writable bool",
					g, tile, path, spec.Kind, spec.Writable)
			}
		}
	}
}

func TestFilterPath(t *testing.T) {
	tests := []struct {
		name    string
		group   FilterGroup
		tile    string
		want    string
		wantErr bool
	}{
		{"scene tile", SceneFilter, "48v", "global/fltr48v", false},
		{"scene tile uppercase", SceneFilter, "Mute", "global/fltrmute", false},
		{"advanced tile", AdvancedSceneFilter, "dca_groups", "advancedscenefilters/fltr_dca_groups", false},
		{"dash for underscore", AdvancedSceneFilter, "dca-groups", "advancedscenefilters/fltr_dca_groups", false},
		{"project tile", ProjectFilter, "inputpatching", "projectfilters/fltr_inputpatching", false},
		{"unknown tile", SceneFilter, "nope", "", true},
		{"tile from another group", SceneFilter, "dca_groups", "", true},
		{"unknown group", FilterGroup("bogus"), "48v", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FilterPath(tt.group, tt.tile)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("FilterPath(%q, %q) = %q, nil; want an error", tt.group, tt.tile, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("FilterPath(%q, %q): %v", tt.group, tt.tile, err)
			}
			if got != tt.want {
				t.Errorf("FilterPath(%q, %q) = %q, want %q", tt.group, tt.tile, got, tt.want)
			}
		})
	}
}

func TestFiltersReportsSnapshotState(t *testing.T) {
	b := fakeboard.New(map[string]any{
		"global/fltrname": float32(1.0),
		"global/fltr48v":  float32(0.0),
	})
	c := connectReal(t, startFakeboard(t, b))

	tiles, err := c.Filters(SceneFilter)
	if err != nil {
		t.Fatalf("Filters: %v", err)
	}
	byTile := map[string]FilterTile{}
	for _, tile := range tiles {
		byTile[tile.Tile] = tile
	}

	if got := byTile["name"]; !got.Included || !got.Present {
		t.Errorf("name = %+v; want included and present", got)
	}
	if got := byTile["48v"]; got.Included || !got.Present {
		t.Errorf("48v = %+v; want excluded and present", got)
	}
	// A key the board never sent is reported absent, not as excluded — an
	// unanswered tile and a switched-off tile are different facts.
	if got := byTile["fx"]; got.Present {
		t.Errorf("fx = %+v; want present=false", got)
	}
}

func TestFiltersCoversEveryGroupByDefault(t *testing.T) {
	b := fakeboard.New(map[string]any{"global/fltrname": float32(1.0)})
	c := connectReal(t, startFakeboard(t, b))

	tiles, err := c.Filters()
	if err != nil {
		t.Fatalf("Filters: %v", err)
	}
	seen := map[FilterGroup]int{}
	for _, tile := range tiles {
		seen[tile.Group]++
	}
	for _, g := range FilterGroups() {
		want, _ := FilterTiles(g)
		if seen[g] != len(want) {
			t.Errorf("group %q: %d tiles, want %d", g, seen[g], len(want))
		}
	}
}

func TestSetFilterWritesOneOrZero(t *testing.T) {
	b := fakeboard.New(map[string]any{"global/fltr48v": float32(0.0)})
	c := connectReal(t, startFakeboard(t, b), WithCommitDelay(0))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.SetFilter(ctx, SceneFilter, "48v", true); err != nil {
		t.Fatalf("SetFilter: %v", err)
	}
	waitForBoardValue(t, b, "global/fltr48v", float32(1.0))

	if err := c.SetFilter(ctx, SceneFilter, "48v", false); err != nil {
		t.Fatalf("SetFilter off: %v", err)
	}
	waitForBoardValue(t, b, "global/fltr48v", float32(0.0))
}

func TestSetFilterRejectsUnknownTile(t *testing.T) {
	b := fakeboard.New(map[string]any{"global/fltr48v": float32(0.0)})
	c := connectReal(t, startFakeboard(t, b), WithCommitDelay(0))

	if err := c.SetFilter(context.Background(), SceneFilter, "phantom", true); err == nil {
		t.Fatal("SetFilter with an unknown tile returned nil; want an error")
	}
}

// waitForBoardValue polls the fake board until path holds want, so a test does
// not race the board's apply of a just-sent delta.
func waitForBoardValue(t *testing.T, b *fakeboard.Board, path string, want any) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var got any
	for time.Now().Before(deadline) {
		got = b.Snapshot()[path]
		if got == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("board %s = %v (%T), want %v", path, got, got, want)
}
