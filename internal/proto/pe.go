package proto

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// MarshalPE encodes a momentary event as key, NUL, and a uint16 state.
func MarshalPE(key string, state uint16) []byte {
	out := make([]byte, 0, len(key)+3)
	out = append(out, key...)
	out = append(out, 0)
	var value [2]byte
	binary.LittleEndian.PutUint16(value[:], state)
	return append(out, value[:]...)
}

// UnmarshalPE decodes a momentary event payload.
func UnmarshalPE(payload []byte) (string, uint16, error) {
	i := bytes.IndexByte(payload, 0)
	if i <= 0 || len(payload) != i+3 {
		return "", 0, fmt.Errorf("proto: malformed PE payload")
	}
	return string(payload[:i]), binary.LittleEndian.Uint16(payload[i+1:]), nil
}
