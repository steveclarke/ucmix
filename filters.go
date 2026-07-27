package ucmix

import (
	"context"
	"fmt"
	"strings"
)

// Scope filters — the tiles that decide what a Store, Recall or Reset touches.
//
// UC Surface shows them as blue tiles next to the Scenes and Projects screens.
// On the wire they are not a preset-layer format at all: each tile is an
// ordinary on/off parameter in the board's snapshot, so reading them is a
// snapshot read and setting one is a single PV write. 1 = the setting is
// included in a store/recall/reset, 0 = excluded.
//
// Confirmed against a 32R on firmware 3.4.0. The board ships with the Scene
// Filter's 48v tile at 0, which is why a scene recall leaves phantom power
// alone.

// FilterGroup names one set of scope-filter tiles.
type FilterGroup string

const (
	// SceneFilter is the Scene Filter tile set (global/fltr*), the top-level
	// filter a scene store/recall obeys.
	SceneFilter FilterGroup = "scene"
	// AdvancedSceneFilter is the Advanced Scene Filter tile set
	// (advancedscenefilters/), which subdivides what a scene carries.
	AdvancedSceneFilter FilterGroup = "advanced"
	// ProjectFilter is the Project Filter tile set (projectfilters/) — the
	// project layer's setup: patching, routing, flex mode, GEQ, solo.
	ProjectFilter FilterGroup = "project"
)

// filterGroup describes one tile set: the path prefix its keys share and the
// tiles in board order.
type filterGroupDef struct {
	prefix string
	tiles  []string
}

// filterGroupDefs holds the tile roster per group. Tile names are the board's
// own key names with the prefix stripped — no invented labels, so a tile always
// names the key it writes.
var filterGroupDefs = map[FilterGroup]filterGroupDef{
	SceneFilter: {
		prefix: "global/fltr",
		tiles: []string{
			"name", "mute", "fx", "eqdynins", "eqdynouts", "aux", "assign",
			"preamps", "fader", "geq", "dcagrp", "48v", "mutegroups", "user",
			"patch",
		},
	},
	AdvancedSceneFilter: {
		prefix: "advancedscenefilters/fltr_",
		tiles: []string{
			"channel_info", "preamp", "channelstrip", "input_fatch",
			"output_fatch", "channel_delay", "mutes", "main_mix_level",
			"main_mix_assigns", "subgroup_assigns", "aux_matrix_mixes",
			"fx_mixes", "fx_type", "dca_groups", "mute_groups",
		},
	},
	ProjectFilter: {
		prefix: "projectfilters/fltr_",
		tiles: []string{
			"input_source", "flexmixmode", "flexmixprepostmode",
			"fxmixpreposmode", "talkbackassigns", "solosettings",
			"generalsettings", "avbstreamrouting", "inputpatching",
			"outputpatching", "avbpatching", "sdpatching", "usbpatching", "geq",
			"user_functions",
		},
	},
}

// FilterGroups returns every scope-filter group, scene filter first.
func FilterGroups() []FilterGroup {
	return []FilterGroup{SceneFilter, AdvancedSceneFilter, ProjectFilter}
}

// FilterTiles returns a group's tile names in board order.
func FilterTiles(group FilterGroup) ([]string, error) {
	def, ok := filterGroupDefs[group]
	if !ok {
		return nil, unknownFilterGroup(group)
	}
	return append([]string(nil), def.tiles...), nil
}

// FilterPath returns the wire key one tile writes. The tile name is matched
// case-insensitively and a dash stands in for an underscore, so "dca-groups"
// and "dca_groups" name the same tile.
func FilterPath(group FilterGroup, tile string) (string, error) {
	def, ok := filterGroupDefs[group]
	if !ok {
		return "", unknownFilterGroup(group)
	}
	want := normalizeTile(tile)
	for _, t := range def.tiles {
		if t == want {
			return def.prefix + t, nil
		}
	}
	return "", fmt.Errorf("ucmix: %s filter has no %q tile", group, tile)
}

// normalizeTile folds a tile name to its canonical form: lower case, dashes as
// underscores.
func normalizeTile(tile string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(tile)), "-", "_")
}

// unknownFilterGroup reports a group name that is not one of FilterGroups.
func unknownFilterGroup(group FilterGroup) error {
	names := make([]string, 0, len(filterGroupDefs))
	for _, g := range FilterGroups() {
		names = append(names, string(g))
	}
	return fmt.Errorf("ucmix: no filter group %q (have %s)", group, strings.Join(names, ", "))
}

// FilterTile is one tile's state. Included reports whether the setting travels
// with a store/recall/reset. Present is false when the board's snapshot carried
// no such key — an absent tile and an excluded one are different facts, and a
// firmware that does not expose a tile must not read as "excluded".
type FilterTile struct {
	Group    FilterGroup
	Tile     string
	Path     string
	Included bool
	Present  bool
}

// Filters returns the state of every tile in the named groups, in board order.
// With no groups it covers all of them. The values come from the client's
// snapshot, so this is a local read of already-subscribed state.
func (c *Client) Filters(groups ...FilterGroup) ([]FilterTile, error) {
	if len(groups) == 0 {
		groups = FilterGroups()
	}
	snap := c.Snapshot()
	var out []FilterTile
	for _, g := range groups {
		def, ok := filterGroupDefs[g]
		if !ok {
			return nil, unknownFilterGroup(g)
		}
		for _, tile := range def.tiles {
			path := def.prefix + tile
			raw, present := snap[path]
			out = append(out, FilterTile{
				Group:    g,
				Tile:     tile,
				Path:     path,
				Included: present && truthy(raw),
				Present:  present,
			})
		}
	}
	return out, nil
}

// SetFilter includes or excludes one tile from a store/recall/reset. It writes
// the tile's key as a plain 1/0 toggle; an unknown group or tile is an error
// rather than a raw write to a made-up path.
func (c *Client) SetFilter(ctx context.Context, group FilterGroup, tile string, included bool) error {
	path, err := FilterPath(group, tile)
	if err != nil {
		return err
	}
	return c.Set(ctx, path, included)
}
