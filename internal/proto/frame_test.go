package proto

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestEncodeGolden(t *testing.T) {
	tests := []struct {
		name  string
		frame Frame
		want  []byte
	}{
		{
			name:  "KA empty payload default connID",
			frame: Frame{Code: CodeKA},
			want: []byte{
				0x55, 0x43, 0x00, 0x01, // header
				0x06, 0x00, // length = 6
				'K', 'A',
				0x68, 0x00, 0x65, 0x00, // default connID
			},
		},
		{
			name:  "PV with explicit connID and payload",
			frame: Frame{Code: CodePV, ConnID: [4]byte{0x6b, 0x00, 0x65, 0x00}, Payload: []byte{0xaa, 0xbb}},
			want: []byte{
				0x55, 0x43, 0x00, 0x01,
				0x08, 0x00, // length = 6 + 2
				'P', 'V',
				0x6b, 0x00, 0x65, 0x00,
				0xaa, 0xbb,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Encode(tc.frame)
			if !bytes.Equal(got, tc.want) {
				t.Fatalf("Encode() =\n% x\nwant\n% x", got, tc.want)
			}
		})
	}
}

func TestReadFrameRoundTrip(t *testing.T) {
	frames := []Frame{
		{Code: CodeKA, ConnID: DefaultConnID},
		{Code: CodePV, ConnID: [4]byte{0x6b, 0x00, 0x65, 0x00}, Payload: []byte{0x01, 0x02, 0x03}},
		{Code: CodeZB, ConnID: DefaultConnID, Payload: bytes.Repeat([]byte{0x7a}, 300)},
	}
	// Concatenate several frames back-to-back to prove ReadFrame consumes
	// exactly one frame per call.
	var buf bytes.Buffer
	for _, f := range frames {
		buf.Write(Encode(f))
	}
	r := bufio.NewReader(&buf)
	for i, want := range frames {
		got, err := ReadFrame(r)
		if err != nil {
			t.Fatalf("frame %d: ReadFrame error: %v", i, err)
		}
		if got.Code != want.Code {
			t.Errorf("frame %d: Code = %q, want %q", i, got.Code, want.Code)
		}
		if got.ConnID != want.ConnID {
			t.Errorf("frame %d: ConnID = % x, want % x", i, got.ConnID, want.ConnID)
		}
		if !bytes.Equal(got.Payload, want.Payload) {
			t.Errorf("frame %d: Payload = % x, want % x", i, got.Payload, want.Payload)
		}
	}
	if _, err := ReadFrame(r); !errors.Is(err, io.EOF) {
		t.Errorf("expected EOF after last frame, got %v", err)
	}
}

func TestReadFramePreservesForeignConnID(t *testing.T) {
	// UC Surface uses a different connIdentity; decode must store it verbatim,
	// never assert DefaultConnID.
	foreign := [4]byte{0x6b, 0x00, 0x65, 0x00}
	wire := Encode(Frame{Code: CodePS, ConnID: foreign, Payload: []byte{0x00}})
	f, err := ReadFrame(bufio.NewReader(bytes.NewReader(wire)))
	if err != nil {
		t.Fatal(err)
	}
	if f.ConnID != foreign {
		t.Fatalf("ConnID = % x, want % x", f.ConnID, foreign)
	}
}

func TestReadFrameMalformed(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
	}{
		{"empty", nil},
		{"short header", []byte{0x55, 0x43}},
		{"bad magic", []byte{0x00, 0x00, 0x00, 0x00, 0x06, 0x00, 'K', 'A', 0x68, 0x00, 0x65, 0x00}},
		{"missing length", []byte{0x55, 0x43, 0x00, 0x01, 0x06}},
		{"length under 6", []byte{0x55, 0x43, 0x00, 0x01, 0x05, 0x00, 'K', 'A', 0x68}},
		{"truncated body", []byte{0x55, 0x43, 0x00, 0x01, 0x08, 0x00, 'P', 'V', 0x68, 0x00}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ReadFrame(bufio.NewReader(bytes.NewReader(tc.in)))
			if err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestReadFrameBadMagicIsTyped(t *testing.T) {
	in := []byte{0x00, 0x00, 0x00, 0x00, 0x06, 0x00, 'K', 'A', 0x68, 0x00, 0x65, 0x00}
	_, err := ReadFrame(bufio.NewReader(bytes.NewReader(in)))
	if !errors.Is(err, ErrBadMagic) {
		t.Fatalf("want ErrBadMagic, got %v", err)
	}
}
