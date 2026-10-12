package daw

import (
	"bytes"
	"net"
	"testing"

	"github.com/steveclarke/ucmix/internal/proto"
)

// Everything below arrives unauthenticated from the LAN or from Studio Pro;
// none of it may panic.

func FuzzParseAnnouncement(f *testing.F) {
	f.Add(announcement("DAW", "x.studioapp8", 49601))
	f.Add([]byte("UC\x00\x01"))
	f.Add([]byte("UC\x00\x01\x01\x00DA\x00\x00\x00\x00\x00\x00\x00\x00"))
	f.Add([]byte{})
	addr := &net.UDPAddr{IP: net.ParseIP("192.0.2.1"), Port: 1}
	f.Fuzz(func(t *testing.T, packet []byte) {
		_, _ = parseAnnouncement(packet, addr)
	})
}

func FuzzModelApplyFrame(f *testing.F) {
	for _, code := range []string{"PV", "PS", "PC", "ZM", "CK", "JM"} {
		f.Add(code, []byte{})
		f.Add(code, []byte("key"))
		f.Add(code, []byte("key\x00"))
		f.Add(code, []byte("key\x00\x00"))
		f.Add(code, []byte("\x05\x00\x00\x00x^garbage"))
	}
	f.Add("BO", liveSections)
	f.Add("BO", liveMarkers)
	f.Add("ZM", zm(&testing.T{}, `{"id": "Synchronize","children": {"a": {"values": {"b": [1]}}}}`).Payload)
	f.Fuzz(func(t *testing.T, code string, payload []byte) {
		if len(code) != 2 {
			return
		}
		frame := proto.Frame{Code: [2]byte{code[0], code[1]}, Payload: payload}
		_ = NewModel().ApplyFrame(frame)
		var c chunks
		if ck, err := proto.ParseCK(payload); err == nil {
			_, _ = c.add(ck)
		}
		_, _ = ReadCapture(bytes.NewReader(payload))
	})
}

func TestTruncatedWireInputIsAnErrorNotAPanic(t *testing.T) {
	cases := map[string][]byte{
		"PV short value": []byte("transport/tempo\x00\x00\x00\x01"),
		"PS no NUL":      []byte("a\x00\x00\x00abc"),
		"PC short":       []byte("a\x00"),
		"ZM short":       {1, 2, 3},
		"ZM not zlib":    {9, 0, 0, 0, 'n', 'o', 'p', 'e'},
	}
	codes := map[string][2]byte{"PV": proto.CodePV, "PS": proto.CodePS, "PC": proto.CodePC, "ZM": codeZM}
	for name, payload := range cases {
		code := codes[name[:2]]
		if err := NewModel().ApplyFrame(proto.Frame{Code: code, Payload: payload}); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
