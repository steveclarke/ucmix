package daw

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
)

// Event is one entry of a song event list: a marker or an arranger section.
// Times are in seconds from the song start.
type Event struct {
	Name  string  `json:"name"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Color uint32  `json:"color,omitempty"`
	Tag   string  `json:"-"`
}

// EventList is a decoded BO "EvDL" frame.
type EventList struct {
	Kind   string // Mark, Arng, Chrd, Lyrc
	Events []Event
}

var errBadEventList = errors.New("daw: malformed event list")

// maxEvents bounds one list; a song has tens of markers, not thousands.
const maxEvents = 4096

// ParseEventList decodes a BO payload of the form
//
//	"EvDL" [len u32] [version u16] [kind 4] [u32 0] [count u32]
//	count × ( [tag 4] [u32 flags] [color u32] [start f64] [end f64] [name NUL] )
//
// as seen live for markers (Mark: MSta, Mark, MEnd) and arranger sections
// (Arng). All integers and floats are little-endian.
func ParseEventList(p []byte) (EventList, error) {
	if len(p) < 22 || !bytes.Equal(p[:4], []byte("EvDL")) {
		return EventList{}, errBadEventList
	}
	list := EventList{Kind: string(p[10:14])}
	count := binary.LittleEndian.Uint32(p[18:22])
	if count > maxEvents {
		return EventList{}, errBadEventList
	}
	i := 22
	for n := uint32(0); n < count; n++ {
		if len(p) < i+29 {
			return EventList{}, errBadEventList
		}
		e := Event{
			Tag:   string(p[i : i+4]),
			Color: binary.LittleEndian.Uint32(p[i+8 : i+12]),
			Start: math.Float64frombits(binary.LittleEndian.Uint64(p[i+12 : i+20])),
			End:   math.Float64frombits(binary.LittleEndian.Uint64(p[i+20 : i+28])),
		}
		i += 28
		end := bytes.IndexByte(p[i:], 0)
		if end < 0 {
			return EventList{}, errBadEventList
		}
		e.Name = string(p[i : i+end])
		i += end + 1
		list.Events = append(list.Events, e)
	}
	return list, nil
}
