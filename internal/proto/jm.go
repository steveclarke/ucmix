package proto

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
)

// MarshalJM encodes a JM payload: a 4-byte little-endian length of the JSON body
// followed by the JSON body itself. The frame wrapper (Encode) adds the header,
// code, and connID. Compact JSON is emitted; the board is whitespace-insensitive.
func MarshalJM(v any) []byte {
	body, err := json.Marshal(v)
	if err != nil {
		// Command bodies here are closed structs of strings/ints; marshaling
		// cannot fail. Panicking would violate the "never panic" contract, so
		// fall back to an empty body rather than crash a caller.
		body = []byte("{}")
	}
	n := len(body)
	out := make([]byte, 0, 4+n)
	out = append(out, byte(n), byte(n>>8), byte(n>>16), byte(n>>24))
	out = append(out, body...)
	return out
}

// The command structs below expose only the variable fields. The fixed fields
// (id and the url/target boilerplate) are supplied by each type's MarshalJSON so
// callers cannot get them wrong, and so unmarshalling a captured body into the
// struct ignores those fixed fields and compares on the variable ones alone.

// SubscribeCmd is the handshake Subscribe body. All fields are fixed for our
// client; DefaultSubscribeCmd returns the exact body UC Surface / featherbear
// send. Field order matches the captured handshake.
type SubscribeCmd struct {
	ID                 string `json:"id"`
	ClientName         string `json:"clientName"`
	ClientInternalName string `json:"clientInternalName"`
	ClientType         string `json:"clientType"`
	ClientDescription  string `json:"clientDescription"`
	ClientIdentifier   string `json:"clientIdentifier"`
	ClientOptions      string `json:"clientOptions"`
	ClientEncoding     int    `json:"clientEncoding"`
}

// DefaultSubscribeCmd returns the standard Subscribe body from the protocol doc.
func DefaultSubscribeCmd() SubscribeCmd {
	return SubscribeCmd{
		ID:                 "Subscribe",
		ClientName:         "UC-Surface",
		ClientInternalName: "ucremoteapp",
		ClientType:         "StudioLive API",
		ClientDescription:  "User",
		ClientIdentifier:   "133d066a919ea0ea",
		ClientOptions:      "perm users levl redu rtan",
		ClientEncoding:     23106,
	}
}

// ResetMixerCmd scopes a mixer reset. ResetScene zeroes scene-level settings and
// ResetProject zeroes project-level settings (both 1 = full factory reset).
type ResetMixerCmd struct {
	ResetScene   int `json:"resetSceneSettings"`
	ResetProject int `json:"resetProjectSettings"`
}

// MarshalJSON emits the full ResetMixer body with fixed id/url/src fields.
func (c ResetMixerCmd) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ID           string `json:"id"`
		ResetScene   int    `json:"resetSceneSettings"`
		ResetProject int    `json:"resetProjectSettings"`
		URL          string `json:"url"`
		Src          string `json:"src"`
	}{"ResetMixer", c.ResetScene, c.ResetProject, "presets", "presets"})
}

// StorePresetCmd stores a scene/preset. PresetFile is the target path, e.g.
// "presets/proj/03.135 Main Live.proj/02.Foo.scn".
type StorePresetCmd struct {
	PresetFile string `json:"presetFile"`
}

// MarshalJSON emits the full StorePreset body with fixed id/url/presetTarget.
func (c StorePresetCmd) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ID           string `json:"id"`
		URL          string `json:"url"`
		PresetTarget string `json:"presetTarget"`
		PresetFile   string `json:"presetFile"`
	}{"StorePreset", "presets", "", c.PresetFile})
}

// RestorePresetCmd recalls a scene/preset. PresetFile is the source path.
//
// The body mirrors StorePreset but also carries presetTargetSlave: 0, which
// featherbear's verified-working recall paths (recallProject / recallProjectScene
// / recallChannelStrip) all send. The StorePreset capture omits it; there is no
// captured RestorePreset frame to confirm whether the board requires it, so this
// matches the one verified-working recall path rather than the store capture.
type RestorePresetCmd struct {
	PresetFile string `json:"presetFile"`
}

// MarshalJSON emits the full RestorePreset body with the fixed id/url/target
// fields and presetTargetSlave.
func (c RestorePresetCmd) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ID                string `json:"id"`
		URL               string `json:"url"`
		PresetTarget      string `json:"presetTarget"`
		PresetTargetSlave int    `json:"presetTargetSlave"`
		PresetFile        string `json:"presetFile"`
	}{"RestorePreset", "presets", "", 0, c.PresetFile})
}

// --- inbound JM ---

// JMMessage is an inbound JM frame: the command id the board tagged it with and
// the raw JSON body it carried. Callers switch on ID and unmarshal Body into the
// matching struct.
type JMMessage struct {
	ID   string
	Body []byte
}

// JM ids the board sends that we act on.
const (
	// JMStoredPreset is the board's confirmation that a StorePreset completed.
	// It is the only positive acknowledgment of a preset write: the board emits
	// it after the scene is on disk, so a store that never sees it did not
	// happen. Observed on a 32R via packet capture (2026-07-25).
	JMStoredPreset = "StoredPreset"
	// JMRecalledPreset is the board's confirmation that a RestorePreset
	// completed, carrying the same PresetAck body as a store. Observed on a 32R
	// (2026-07-25).
	JMRecalledPreset = "RecalledPreset"
)

// PresetAck is the body of a StoredPreset or RecalledPreset acknowledgment:
//
//	{"id": "StoredPreset", "presetFile": "presets/proj/03.X.proj/04.Y.scn",
//	 "presetName": "Y", "presetType": "scn", "url": "presets"}
//
// PresetFile echoes the request's target, so a caller waiting on a specific
// store can match on it and ignore acks for other clients' writes.
type PresetAck struct {
	PresetFile string `json:"presetFile"`
	PresetName string `json:"presetName"`
	PresetType string `json:"presetType"`
}

// ParseJM decodes an inbound JM payload into its id and raw JSON body. The
// payload is a 4-byte little-endian body length followed by the JSON.
func ParseJM(payload []byte) (JMMessage, error) {
	if len(payload) < 4 {
		return JMMessage{}, fmt.Errorf("proto: JM payload too short (%d bytes)", len(payload))
	}
	n := int(binary.LittleEndian.Uint32(payload[:4]))
	body := payload[4:]
	// Trust the declared length when it fits; a board that pads or truncates
	// still yields parseable JSON from the remainder.
	if n >= 0 && n <= len(body) {
		body = body[:n]
	}
	var head struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &head); err != nil {
		return JMMessage{}, fmt.Errorf("proto: parsing JM body: %w", err)
	}
	return JMMessage{ID: head.ID, Body: body}, nil
}

// UnmarshalPresetAck decodes a StoredPreset body.
func UnmarshalPresetAck(body []byte) (PresetAck, error) {
	var ack PresetAck
	if err := json.Unmarshal(body, &ack); err != nil {
		return PresetAck{}, fmt.Errorf("proto: parsing preset ack: %w", err)
	}
	return ack, nil
}
