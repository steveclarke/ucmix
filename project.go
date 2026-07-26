package ucmix

import "strings"

// The project layer — the setup a scene sits on: input source and patching,
// AVB/SD/USB routing, flex mode, GEQ, solo. Each project is a folder of scenes
// on the board (dir:true in an FR listing) holding one "<title>.cnfg" file, the
// project-layer state itself. ListProjects enumerates them; the Project Filter
// tiles (see filters.go) decide what the layer carries.
//
// Storing and recalling a project as a unit is not implemented: the JM request
// UC Surface sends for it has not been captured, and the preset layer has
// already cost several rounds of wrong guesses. See issue #6.

// LoadedPreset is the project and scene the board currently has loaded, as it
// reports them in presets/loaded_*. Names are slot names ("03.135 Main
// Live.proj", "01.Opening.scn"); titles are the display names.
type LoadedPreset struct {
	ProjectName  string
	ProjectTitle string
	SceneName    string
	SceneTitle   string
}

// LoadedPreset reads the loaded project and scene out of the client's snapshot.
// Fields the board did not report come back empty.
//
// The board states these as paths relative to its preset root ("proj/03.135 Main
// Live.proj/01.Opening.scn"); this returns the trailing slot name, which is what
// ListProjects and ListScenes key on.
func (c *Client) LoadedPreset() LoadedPreset {
	snap := c.Snapshot()
	return LoadedPreset{
		ProjectName:  slotName(snapString(snap, "presets/loaded_project_name")),
		ProjectTitle: snapString(snap, "presets/loaded_project_title"),
		SceneName:    slotName(snapString(snap, "presets/loaded_scene_name")),
		SceneTitle:   snapString(snap, "presets/loaded_scene_title"),
	}
}

// snapString reads a string value out of a snapshot, or "" if the key is absent
// or not a string.
func snapString(snap map[string]any, path string) string {
	s, _ := snap[path].(string)
	return s
}

// slotName trims a preset path down to its trailing slot name.
func slotName(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}
