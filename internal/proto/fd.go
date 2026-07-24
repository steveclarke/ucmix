package proto

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
)

// FD ("file data") frames carry the board's reply to an FR request. The body —
// JSON naming the presets — is streamed across one or more FD frames and must be
// reassembled before parsing. The chunking mirrors the CK snapshot chunks, so
// the reassembly reuses ChunkAssembler; only the header layout differs.
//
// FD payload layout (14-byte header):
//
//	[ id u16 LE ][ offset u32 LE ][ total u32 LE ][ more u16 LE ][ size u16 LE ][ data ]
//
// id echoes the FR request id. offset is this chunk's byte position in the full
// body; total is the full body size; size is this chunk's data length. more is 1
// while further chunks follow and 0 on the last — redundant with
// offset+size==total, which is what drives completion. Observed on a 32R
// (v0.4.0) via a UC Surface packet capture; a ~7 KB body arrives as a 4096-byte
// chunk followed by the remainder.

// fdHeaderLen is the fixed FD header size preceding the chunk data.
const fdHeaderLen = 14

// FDChunk is one parsed FD frame: the reply id plus the CK-shaped chunk fed to
// ChunkAssembler.
type FDChunk struct {
	ID    uint16
	Chunk CKChunk
}

// errShortFD is returned when an FD payload is too small to hold its 14-byte
// header.
var errShortFD = errors.New("proto: FD payload shorter than 14-byte header")

// ParseFD parses an FD frame payload. It validates that the declared chunk size
// matches the bytes present.
func ParseFD(payload []byte) (FDChunk, error) {
	if len(payload) < fdHeaderLen {
		return FDChunk{}, errShortFD
	}
	id := binary.LittleEndian.Uint16(payload[0:2])
	offset := binary.LittleEndian.Uint32(payload[2:6])
	total := binary.LittleEndian.Uint32(payload[6:10])
	size := uint32(binary.LittleEndian.Uint16(payload[12:14]))
	data := payload[fdHeaderLen:]
	if int(size) != len(data) {
		return FDChunk{}, fmt.Errorf("proto: FD chunk size %d != data length %d", size, len(data))
	}
	return FDChunk{
		ID:    id,
		Chunk: CKChunk{Offset: offset, Total: total, Size: size, Data: data},
	}, nil
}

// BuildFDPayload builds an FD frame payload carrying chunk at offset within a
// body of total bytes. It is the inverse of ParseFD, used by the fake board and
// the encoder's round-trip test. The more flag is set when this is not the last
// chunk (offset+len(chunk) < total).
func BuildFDPayload(id uint16, offset, total uint32, chunk []byte) []byte {
	out := make([]byte, fdHeaderLen+len(chunk))
	binary.LittleEndian.PutUint16(out[0:2], id)
	binary.LittleEndian.PutUint32(out[2:6], offset)
	binary.LittleEndian.PutUint32(out[6:10], total)
	var more uint16
	if offset+uint32(len(chunk)) < total {
		more = 1
	}
	binary.LittleEndian.PutUint16(out[10:12], more)
	binary.LittleEndian.PutUint16(out[12:14], uint16(len(chunk)))
	copy(out[fdHeaderLen:], chunk)
	return out
}

// PresetFile is one entry in a reassembled preset-list reply. Dir is true for a
// project (a folder holding scenes); scene entries are files with Dir false. An
// unused slot carries the board's empty-slot marker in Title.
type PresetFile struct {
	Name  string `json:"name"`
	Title string `json:"title"`
	Dir   bool   `json:"dir"`
}

// EmptyPresetTitle is the title a 32R gives an unused project or scene slot. The
// board reports a fixed roster of slots (100 projects, 20 scenes per project);
// occupied entries carry a real title, empty ones this marker.
const EmptyPresetTitle = "* Empty Location *"

// ParsePresetList parses a reassembled FD body — {"files":[…]} — into its
// entries.
func ParsePresetList(body []byte) ([]PresetFile, error) {
	var reply struct {
		Files []PresetFile `json:"files"`
	}
	if err := json.Unmarshal(body, &reply); err != nil {
		return nil, fmt.Errorf("proto: parsing preset list: %w", err)
	}
	return reply.Files, nil
}
