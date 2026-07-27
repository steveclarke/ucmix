package proto

import (
	"bytes"
	"testing"
)

func TestMarshalPEMatchesCapturedTransportEvents(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		state uint16
		want  []byte
	}{
		{"play press", "transport/start", 2, []byte("transport/start\x00\x02\x00")},
		{"play release", "transport/start", 0, []byte("transport/start\x00\x00\x00")},
		{"stop press", "transport/stop", 2, []byte("transport/stop\x00\x02\x00")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MarshalPE(tt.key, tt.state)
			if !bytes.Equal(got, tt.want) {
				t.Fatalf("MarshalPE = % x, want % x", got, tt.want)
			}
			key, state, err := UnmarshalPE(got)
			if err != nil || key != tt.key || state != tt.state {
				t.Fatalf("UnmarshalPE = %q, %d, %v", key, state, err)
			}
		})
	}
}

func TestUnmarshalPERejectsMalformedPayload(t *testing.T) {
	for _, payload := range [][]byte{
		nil,
		[]byte("transport/start"),
		[]byte("transport/start\x00\x02"),
		[]byte("transport/start\x00\x02\x00\xff"),
	} {
		if _, _, err := UnmarshalPE(payload); err == nil {
			t.Fatalf("UnmarshalPE(% x) succeeded", payload)
		}
	}
}
