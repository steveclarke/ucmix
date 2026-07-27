package daw

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/steveclarke/ucmix/internal/proto"
)

func announcement(class, identity string, port uint16) []byte {
	pkt := append([]byte{}, proto.Header[:]...)
	var p [2]byte
	binary.LittleEndian.PutUint16(p[:], port)
	pkt = append(pkt, p[:]...)
	pkt = append(pkt, 'D', 'A', 0x6b, 0, 0, 0, 0, 0, 0, 0)
	for _, value := range []string{
		"Studio Pro/8.1.1.113103 macOS Arm64",
		class,
		identity,
		"Test Mac mini",
	} {
		pkt = append(pkt, value...)
		pkt = append(pkt, 0)
	}
	return pkt
}

func TestParseAnnouncement(t *testing.T) {
	addr := &net.UDPAddr{IP: net.ParseIP("192.0.2.20"), Port: 62000}
	got, ok := parseAnnouncement(announcement("DAW", "sanitized.studioapp8", 49601), addr)
	if !ok {
		t.Fatal("parseAnnouncement returned ok=false")
	}
	want := Endpoint{
		Product:     "Studio Pro",
		Version:     "8.1.1.113103 macOS Arm64",
		DeviceClass: "DAW",
		Identity:    "sanitized.studioapp8",
		Name:        "Test Mac mini",
		IP:          "192.0.2.20",
		ControlPort: 49601,
		ServerID:    0x6b,
	}
	if got != want {
		t.Fatalf("parseAnnouncement = %+v, want %+v", got, want)
	}
}

func TestParseAnnouncementRejectsOtherTraffic(t *testing.T) {
	addr := &net.UDPAddr{IP: net.ParseIP("192.0.2.20"), Port: 62000}
	wrongCode := announcement("DAW", "x.studioapp8", 49601)
	wrongCode[6] = 'P'
	cases := map[string][]byte{
		"short":       proto.Header[:],
		"mixer class": announcement("Mixer", "board.serial", 53000),
		"wrong code":  wrongCode,
		"zero port":   announcement("DAW", "x.studioapp8", 0),
	}
	for name, pkt := range cases {
		t.Run(name, func(t *testing.T) {
			if _, ok := parseAnnouncement(pkt, addr); ok {
				t.Fatalf("parseAnnouncement accepted %s", name)
			}
		})
	}
}

func TestSelectStudioPro(t *testing.T) {
	match := Endpoint{DeviceClass: "DAW", Identity: "one.studioapp8"}
	got, err := SelectStudioPro([]Endpoint{
		{DeviceClass: "DAW", Identity: "other.product"},
		match,
	})
	if err != nil || got != match {
		t.Fatalf("SelectStudioPro = %+v, %v", got, err)
	}
}

func TestSelectStudioProRejectsZeroOrMultipleMatches(t *testing.T) {
	if _, err := SelectStudioPro(nil); !errors.Is(err, ErrNoStudioPro) {
		t.Fatalf("SelectStudioPro(nil) = %v, want ErrNoStudioPro", err)
	}
	matches := []Endpoint{
		{DeviceClass: "DAW", Identity: "one.studioapp8"},
		{DeviceClass: "DAW", Identity: "two.studioapp8"},
	}
	if _, err := SelectStudioPro(matches); !errors.Is(err, ErrMultipleStudioPro) {
		t.Fatalf("SelectStudioPro(two matches) = %v, want ErrMultipleStudioPro", err)
	}
}

type announcementPacket struct {
	data []byte
	addr net.Addr
}

type fakeAnnouncementConn struct {
	packets  []announcementPacket
	next     int
	deadline time.Time
}

func (f *fakeAnnouncementConn) ReadFrom(buffer []byte) (int, net.Addr, error) {
	if f.next < len(f.packets) {
		packet := f.packets[f.next]
		f.next++
		return copy(buffer, packet.data), packet.addr, nil
	}
	if wait := time.Until(f.deadline); wait > 0 {
		time.Sleep(wait)
	}
	return 0, nil, fakeTimeout{}
}

func (f *fakeAnnouncementConn) WriteTo([]byte, net.Addr) (int, error) { return 0, nil }
func (f *fakeAnnouncementConn) Close() error                          { return nil }
func (f *fakeAnnouncementConn) LocalAddr() net.Addr                   { return nil }
func (f *fakeAnnouncementConn) SetDeadline(time.Time) error           { return nil }
func (f *fakeAnnouncementConn) SetReadDeadline(value time.Time) error {
	f.deadline = value
	return nil
}
func (f *fakeAnnouncementConn) SetWriteDeadline(time.Time) error { return nil }

type fakeTimeout struct{}

func (fakeTimeout) Error() string   { return "i/o timeout" }
func (fakeTimeout) Timeout() bool   { return true }
func (fakeTimeout) Temporary() bool { return true }

func TestScanDeduplicatesByIdentity(t *testing.T) {
	addr := &net.UDPAddr{IP: net.ParseIP("192.0.2.20"), Port: 62000}
	fake := &fakeAnnouncementConn{packets: []announcementPacket{
		{announcement("DAW", "sanitized.studioapp8", 49601), addr},
		{announcement("DAW", "sanitized.studioapp8", 49601), addr},
		{announcement("Mixer", "board.serial", 53000), addr},
	}}
	original := listenAnnouncements
	listenAnnouncements = func() (net.PacketConn, error) { return fake, nil }
	t.Cleanup(func() { listenAnnouncements = original })

	got, err := Scan(context.Background(), 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ControlPort != 49601 {
		t.Fatalf("Scan = %+v", got)
	}
}
