package proto

import "encoding/binary"

// FR ("file request") is the frame UC Surface sends to enumerate presets on the
// board. The board answers with a stream of FD ("file data") chunks whose
// reassembled body is JSON. This is the request UC Surface uses for the
// Projects/Scenes screen; a real 32R answers it immediately.
//
// FR payload layout:
//
//	[ id u16 LE ][ resource cstr ][ arg cstr ]
//
// Both fields are null-terminated ASCII. id correlates the reply's FD frames
// (echoed back in the FD header) but is not required for reassembly. The
// resource names what to list; arg is a second parameter used only by channel
// presets. For projects and scenes arg is empty, so the payload ends in the
// resource string followed by two null bytes:
//
//	list projects:          resource "Listpresets/proj",                 arg ""
//	list scenes in project: resource "Listpresets/proj/<name>.proj",     arg ""
//	list channel presets:   resource "Listpresets/channel",             arg "<category>"
//
// Observed on a 32R (v0.4.0) via a UC Surface packet capture.

// MarshalFR builds an FR request payload for resource and arg. Pass an empty arg
// for project and scene listings.
func MarshalFR(id uint16, resource, arg string) []byte {
	out := make([]byte, 0, 2+len(resource)+1+len(arg)+1)
	var idBuf [2]byte
	binary.LittleEndian.PutUint16(idBuf[:], id)
	out = append(out, idBuf[:]...)
	out = append(out, resource...)
	out = append(out, 0)
	out = append(out, arg...)
	out = append(out, 0)
	return out
}
