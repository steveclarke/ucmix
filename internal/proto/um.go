package proto

import (
	"encoding/binary"
	"fmt"
)

// MarshalUM encodes the UDP metering port advertised during a handshake.
func MarshalUM(port uint16) []byte {
	out := make([]byte, 2)
	binary.LittleEndian.PutUint16(out, port)
	return out
}

// UnmarshalUM decodes a UDP metering port.
func UnmarshalUM(payload []byte) (uint16, error) {
	if len(payload) != 2 {
		return 0, fmt.Errorf("proto: UM payload is %d bytes, need 2", len(payload))
	}
	return binary.LittleEndian.Uint16(payload), nil
}
