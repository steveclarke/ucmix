package proto

import (
	"bufio"
	"bytes"
	"encoding/json"
	"testing"
)

// capturedResetJSON is the exact JSON body from a real UC Surface ResetMixer
// frame (pretty-printed, 104 bytes). Whitespace differs from our compact output;
// only the semantics round-trip.
const capturedResetJSON = `{"id": "ResetMixer","resetSceneSettings": 1,"resetProjectSettings": 0,"url": "presets","src": "presets"}`

// buildCapturedResetFrame assembles the full wire bytes of the captured frame:
// header + length + "JM" + connID(6b006500) + 4-byte LE JSON length + body.
func buildCapturedResetFrame() []byte {
	body := []byte(capturedResetJSON)
	n := len(body)
	var out []byte
	out = append(out, 0x55, 0x43, 0x00, 0x01)    // header
	out = append(out, 0x72, 0x00)                // length = 0x0072 = 114
	out = append(out, 'J', 'M')                  // code
	out = append(out, 0x6b, 0x00, 0x65, 0x00)    // connID (UC Surface)
	out = append(out, byte(n), byte(n>>8), 0, 0) // 4-byte LE JSON length
	out = append(out, body...)
	return out
}

func TestCapturedResetFrameShape(t *testing.T) {
	wire := buildCapturedResetFrame()
	if len(capturedResetJSON) != 104 {
		t.Fatalf("captured JSON body is %d bytes, expected 104", len(capturedResetJSON))
	}
	if got := len(wire); got != 4+2+114 {
		t.Fatalf("captured frame is %d bytes, expected %d", got, 4+2+114)
	}

	f, err := ReadFrame(bufio.NewReader(bytes.NewReader(wire)))
	if err != nil {
		t.Fatal(err)
	}
	if f.Code != CodeJM {
		t.Errorf("Code = %q, want JM", f.Code)
	}
	if f.ConnID != [4]byte{0x6b, 0x00, 0x65, 0x00} {
		t.Errorf("ConnID = % x, want 6b 00 65 00", f.ConnID)
	}
	// Payload = 4-byte LE length prefix + JSON body.
	if len(f.Payload) < 4 {
		t.Fatal("payload too short")
	}
	jsonLen := int(f.Payload[0]) | int(f.Payload[1])<<8 | int(f.Payload[2])<<16 | int(f.Payload[3])<<24
	if jsonLen != 104 {
		t.Errorf("JSON length prefix = %d, want 104", jsonLen)
	}
	if got := len(f.Payload) - 4; got != 104 {
		t.Errorf("payload-after-prefix = %d bytes, want 104", got)
	}
}

// TestResetMixerSemanticRoundTrip unmarshals both the captured body and our own
// MarshalJM output into ResetMixerCmd and compares structs. Whitespace differs,
// so this is a semantic (not byte) comparison against the capture.
func TestResetMixerSemanticRoundTrip(t *testing.T) {
	want := ResetMixerCmd{ResetScene: 1, ResetProject: 0}

	var fromCapture ResetMixerCmd
	if err := json.Unmarshal([]byte(capturedResetJSON), &fromCapture); err != nil {
		t.Fatal(err)
	}
	if fromCapture != want {
		t.Errorf("from capture = %+v, want %+v", fromCapture, want)
	}

	payload := MarshalJM(want)
	var fromOurs ResetMixerCmd
	if err := json.Unmarshal(payload[4:], &fromOurs); err != nil {
		t.Fatal(err)
	}
	if fromOurs != want {
		t.Errorf("from our marshal = %+v, want %+v", fromOurs, want)
	}
}

// TestResetMixerByteGolden is a regression tripwire on our own compact
// MarshalJM+Encode output (default connID). Not compared against the capture.
func TestResetMixerByteGolden(t *testing.T) {
	got := Encode(Frame{Code: CodeJM, Payload: MarshalJM(ResetMixerCmd{ResetScene: 1, ResetProject: 0})})
	want := []byte{
		0x55, 0x43, 0x00, 0x01, // header
		0x6d, 0x00, // length = 109
		0x4a, 0x4d, // JM
		0x68, 0x00, 0x65, 0x00, // default connID
		0x63, 0x00, 0x00, 0x00, // JSON length LE = 99
	}
	want = append(want, []byte(`{"id":"ResetMixer","resetSceneSettings":1,"resetProjectSettings":0,"url":"presets","src":"presets"}`)...)
	if !bytes.Equal(got, want) {
		t.Fatalf("MarshalJM+Encode =\n% x\nwant\n% x", got, want)
	}
}

func TestJMCommandBodies(t *testing.T) {
	tests := []struct {
		name string
		cmd  any
		want string
	}{
		{
			"StorePreset",
			StorePresetCmd{PresetFile: "presets/proj/03.135 Main Live.proj/02.Foo.scn"},
			`{"id":"StorePreset","url":"presets","presetTarget":"","presetFile":"presets/proj/03.135 Main Live.proj/02.Foo.scn"}`,
		},
		{
			"RestorePreset",
			RestorePresetCmd{PresetFile: "presets/proj/01.Showfile.proj/02.Scene.scn"},
			`{"id":"RestorePreset","url":"presets","presetTarget":"","presetTargetSlave":0,"presetFile":"presets/proj/01.Showfile.proj/02.Scene.scn"}`,
		},
		{
			"Subscribe",
			DefaultSubscribeCmd(),
			`{"id":"Subscribe","clientName":"UC-Surface","clientInternalName":"ucremoteapp","clientType":"StudioLive API","clientDescription":"User","clientIdentifier":"133d066a919ea0ea","clientOptions":"perm users levl redu rtan","clientEncoding":23106}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			payload := MarshalJM(tc.cmd)
			// Verify the 4-byte LE length prefix matches the body length.
			n := int(payload[0]) | int(payload[1])<<8 | int(payload[2])<<16 | int(payload[3])<<24
			body := payload[4:]
			if n != len(body) {
				t.Errorf("length prefix %d != body length %d", n, len(body))
			}
			if string(body) != tc.want {
				t.Errorf("body =\n%s\nwant\n%s", body, tc.want)
			}
		})
	}
}
