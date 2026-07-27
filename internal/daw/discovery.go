// Package daw contains the unpublished Studio Pro UCNET proof of concept.
package daw

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/steveclarke/ucmix/internal/proto"
	"golang.org/x/sys/unix"
)

const (
	// DiscoveryPort is the shared UCNET announcement port.
	DiscoveryPort = 47809

	announcementStringsOffset = 16
)

var (
	ErrNoStudioPro       = errors.New("daw: no Studio Pro endpoint found")
	ErrMultipleStudioPro = errors.New("daw: multiple Studio Pro endpoints found")
)

// Endpoint is one Studio Pro instance decoded from a DA announcement.
type Endpoint struct {
	Product     string `json:"product"`
	Version     string `json:"version"`
	DeviceClass string `json:"deviceClass"`
	Identity    string `json:"identity"`
	Name        string `json:"name"`
	IP          string `json:"ip"`
	ControlPort int    `json:"controlPort"`
	ServerID    byte   `json:"-"`
}

var listenAnnouncements = func() (net.PacketConn, error) {
	lc := net.ListenConfig{
		Control: func(_, _ string, raw syscall.RawConn) error {
			var socketErr error
			if err := raw.Control(func(fd uintptr) {
				if err := unix.SetsockoptInt(
					int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1,
				); err != nil {
					socketErr = err
					return
				}
				socketErr = unix.SetsockoptInt(
					int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1,
				)
			}); err != nil {
				return err
			}
			return socketErr
		},
	}
	return lc.ListenPacket(
		context.Background(), "udp4", fmt.Sprintf("0.0.0.0:%d", DiscoveryPort),
	)
}

// Scan passively listens for DAW announcements for timeout.
func Scan(ctx context.Context, timeout time.Duration) ([]Endpoint, error) {
	conn, err := listenAnnouncements()
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()

	deadline := time.Now().Add(timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}

	found := map[string]Endpoint{}
	buffer := make([]byte, 2048)
	for ctx.Err() == nil && time.Now().Before(deadline) {
		readDeadline := time.Now().Add(250 * time.Millisecond)
		if deadline.Before(readDeadline) {
			readDeadline = deadline
		}
		_ = conn.SetReadDeadline(readDeadline)

		n, addr, err := conn.ReadFrom(buffer)
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				continue
			}
			if errors.Is(err, net.ErrClosed) || errors.Is(err, os.ErrClosed) {
				break
			}
			return nil, err
		}
		if endpoint, ok := parseAnnouncement(buffer[:n], addr); ok {
			found[endpoint.Identity] = endpoint
		}
	}

	out := make([]Endpoint, 0, len(found))
	for _, endpoint := range found {
		out = append(out, endpoint)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Identity < out[j].Identity
	})
	return out, nil
}

func parseAnnouncement(packet []byte, addr net.Addr) (Endpoint, bool) {
	if len(packet) <= announcementStringsOffset ||
		!bytes.Equal(packet[:4], proto.Header[:]) ||
		!bytes.Equal(packet[6:8], []byte{'D', 'A'}) {
		return Endpoint{}, false
	}

	port := int(binary.LittleEndian.Uint16(packet[4:6]))
	if port == 0 {
		return Endpoint{}, false
	}
	parts := splitNUL(packet[announcementStringsOffset:])
	if len(parts) < 4 || parts[1] != "DAW" || parts[2] == "" {
		return Endpoint{}, false
	}
	product, version, ok := strings.Cut(parts[0], "/")
	if !ok || product == "" || version == "" {
		return Endpoint{}, false
	}

	host, ok := addressHost(addr)
	if !ok {
		return Endpoint{}, false
	}
	return Endpoint{
		Product:     product,
		Version:     version,
		DeviceClass: parts[1],
		Identity:    parts[2],
		Name:        parts[3],
		IP:          host,
		ControlPort: port,
		ServerID:    packet[8],
	}, true
}

func splitNUL(data []byte) []string {
	raw := bytes.Split(data, []byte{0})
	for len(raw) > 0 && len(raw[len(raw)-1]) == 0 {
		raw = raw[:len(raw)-1]
	}
	out := make([]string, len(raw))
	for i, part := range raw {
		out[i] = string(part)
	}
	return out
}

func addressHost(addr net.Addr) (string, bool) {
	if udp, ok := addr.(*net.UDPAddr); ok {
		return udp.IP.String(), true
	}
	host, _, err := net.SplitHostPort(addr.String())
	return host, err == nil
}

// SelectStudioPro requires exactly one supported Studio Pro 8 endpoint.
func SelectStudioPro(endpoints []Endpoint) (Endpoint, error) {
	var matches []Endpoint
	for _, endpoint := range endpoints {
		if endpoint.DeviceClass == "DAW" &&
			strings.HasSuffix(endpoint.Identity, ".studioapp8") {
			matches = append(matches, endpoint)
		}
	}
	switch len(matches) {
	case 0:
		return Endpoint{}, ErrNoStudioPro
	case 1:
		return matches[0], nil
	default:
		return Endpoint{}, fmt.Errorf(
			"%w: %d matches", ErrMultipleStudioPro, len(matches),
		)
	}
}
