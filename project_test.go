package ucmix

import "testing"

func TestLoadedPresetReadsSnapshot(t *testing.T) {
	ft := newFakeTransport()
	c := connectWithZB(t, ft, map[string]any{
		"presets/loaded_project_name":  "proj/03.135 Main Live.proj",
		"presets/loaded_project_title": "135 Main Live",
		"presets/loaded_scene_name":    "proj/03.135 Main Live.proj/01.Worship in the Park.scn",
		"presets/loaded_scene_title":   "Worship in the Park",
	})

	got := c.LoadedPreset()
	// The board reports the loaded project as its own slot name, without the
	// leading "presets/" a preset-file path carries.
	if got.ProjectName != "03.135 Main Live.proj" {
		t.Errorf("ProjectName = %q, want %q", got.ProjectName, "03.135 Main Live.proj")
	}
	if got.ProjectTitle != "135 Main Live" {
		t.Errorf("ProjectTitle = %q", got.ProjectTitle)
	}
	if got.SceneName != "01.Worship in the Park.scn" {
		t.Errorf("SceneName = %q, want %q", got.SceneName, "01.Worship in the Park.scn")
	}
	if got.SceneTitle != "Worship in the Park" {
		t.Errorf("SceneTitle = %q", got.SceneTitle)
	}
}

func TestLoadedPresetEmptyWhenBoardSaysNothing(t *testing.T) {
	ft := newFakeTransport()
	c := connectWithZB(t, ft, map[string]any{})

	if got := c.LoadedPreset(); got != (LoadedPreset{}) {
		t.Errorf("LoadedPreset() = %+v, want the zero value", got)
	}
}
