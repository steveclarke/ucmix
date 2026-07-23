package discovery

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/steveclarke/ucmix/internal/proto"
)

// makeBroadcast builds a mixer announcement datagram: the UCNET header, a
// two-byte length, a code, connID, 20 pad bytes, then NUL-delimited fragments
// [model, extra, serial, name].
func makeBroadcast(model, serial string) []byte {
	pkt := append([]byte{}, proto.Header[:]...)
	pkt = append(pkt, 0x00, 0x00) // length (ignored by parser)
	pkt = append(pkt, 'D', 'A')   // some code
	pkt = append(pkt, 0x68, 0x00, 0x65, 0x00)
	pkt = append(pkt, make([]byte, 20)...) // skipped pad
	for _, frag := range []string{model, "", serial, "custom"} {
		pkt = append(pkt, []byte(frag)...)
		pkt = append(pkt, 0x00)
	}
	return pkt
}

func TestParseBroadcast(t *testing.T) {
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.50"), Port: 47809}
	m, ok := parseBroadcast(makeBroadcast("StudioLive 32R", "SN123"), addr)
	if !ok {
		t.Fatal("parseBroadcast returned ok=false for a valid packet")
	}
	if m.Name != "StudioLive 32R" || m.Serial != "SN123" || m.IP != "192.168.1.50" {
		t.Errorf("parseBroadcast = %+v", m)
	}
}

func TestParseBroadcastRejects(t *testing.T) {
	addr := &net.UDPAddr{IP: net.ParseIP("10.0.0.1"), Port: 1}
	cases := map[string][]byte{
		"bad magic": {0x00, 0x01, 0x02, 0x03, 0x04},
		"too short": append(proto.Header[:], 0x00),
		"no serial": func() []byte {
			pkt := append([]byte{}, proto.Header[:]...)
			pkt = append(pkt, 0x00, 0x00, 'D', 'A', 0x68, 0x00, 0x65, 0x00)
			pkt = append(pkt, make([]byte, 20)...)
			pkt = append(pkt, []byte("only-model")...)
			pkt = append(pkt, 0x00)
			return pkt
		}(),
	}
	for name, pkt := range cases {
		t.Run(name, func(t *testing.T) {
			if _, ok := parseBroadcast(pkt, addr); ok {
				t.Errorf("parseBroadcast(%s) = ok, want rejected", name)
			}
		})
	}
}

// fakePacketConn feeds a fixed set of datagrams then blocks until its deadline,
// standing in for a real UDP socket via the listenUDP seam.
type fakePacketConn struct {
	pkts []struct {
		data []byte
		addr net.Addr
	}
	i        int
	deadline time.Time
}

func (f *fakePacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	if f.i < len(f.pkts) {
		pk := f.pkts[f.i]
		f.i++
		n := copy(p, pk.data)
		return n, pk.addr, nil
	}
	// Nothing left: honor the read deadline like a quiet socket.
	d := time.Until(f.deadline)
	if d > 0 {
		time.Sleep(d)
	}
	return 0, nil, timeoutErr{}
}

func (f *fakePacketConn) WriteTo([]byte, net.Addr) (int, error) { return 0, nil }
func (f *fakePacketConn) Close() error                          { return nil }
func (f *fakePacketConn) LocalAddr() net.Addr                   { return nil }
func (f *fakePacketConn) SetDeadline(time.Time) error           { return nil }
func (f *fakePacketConn) SetReadDeadline(t time.Time) error     { f.deadline = t; return nil }
func (f *fakePacketConn) SetWriteDeadline(time.Time) error      { return nil }

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestScanDedupesBySerial(t *testing.T) {
	addrA := &net.UDPAddr{IP: net.ParseIP("192.168.1.50"), Port: 47809}
	addrB := &net.UDPAddr{IP: net.ParseIP("192.168.1.51"), Port: 47809}
	fake := &fakePacketConn{pkts: []struct {
		data []byte
		addr net.Addr
	}{
		{makeBroadcast("StudioLive 32R", "AAA"), addrA},
		{makeBroadcast("StudioLive 32R", "AAA"), addrA}, // duplicate serial
		{makeBroadcast("StudioLive 16R", "BBB"), addrB},
	}}

	orig := listenUDP
	listenUDP = func(int) (net.PacketConn, error) { return fake, nil }
	defer func() { listenUDP = orig }()

	mixers, err := Scan(context.Background(), 200*time.Millisecond)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(mixers) != 2 {
		t.Fatalf("Scan found %d mixers, want 2: %+v", len(mixers), mixers)
	}
	if mixers[0].Name != "StudioLive 16R" || mixers[1].Name != "StudioLive 32R" {
		t.Errorf("Scan not sorted by name: %+v", mixers)
	}
}
