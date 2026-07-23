// Package discovery finds StudioLive mixers on the local network. Series III
// mixers periodically broadcast a UDP announcement on port 47809; discovery is
// passive — it binds the port, listens for a window, and parses each datagram.
// Nothing is sent. Ported from the featherbear presonus-studiolive-api
// reference (src/lib/Discovery.ts).
package discovery

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"syscall"
	"time"

	"github.com/steveclarke/ucmix/internal/proto"
)

// Port is the UDP port StudioLive mixers broadcast their presence on.
const Port = 47809

// fragmentOffset is where the null-delimited identity fragments begin within a
// broadcast: 12 bytes of UCNET header/code/connID, then 20 bytes the reference
// skips before the model/serial strings.
const fragmentOffset = 12 + 20

// Mixer is one discovered board. Name is the model identifier the board
// broadcasts (e.g. for device-image lookup), not the operator's custom board
// name, which is not present in the announcement.
type Mixer struct {
	Name   string `json:"name"`
	Serial string `json:"serial"`
	IP     string `json:"ip"`
	Port   int    `json:"port"`
}

// listenUDP is the socket-open seam; tests swap it to feed synthetic datagrams.
// The socket sets SO_REUSEADDR and SO_REUSEPORT so discovery coexists with UC
// Surface (or another ucmix), which also bind the broadcast port — without them
// the bind fails with "address already in use" whenever UC Surface is running.
var listenUDP = func(port int) (net.PacketConn, error) {
	lc := net.ListenConfig{
		Control: func(_, _ string, c syscall.RawConn) error {
			var opErr error
			if err := c.Control(func(fd uintptr) {
				if err := syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1); err != nil {
					opErr = err
					return
				}
				opErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEPORT, 1)
			}); err != nil {
				return err
			}
			return opErr
		},
	}
	return lc.ListenPacket(context.Background(), "udp4", fmt.Sprintf("0.0.0.0:%d", port))
}

// Scan listens for mixer broadcasts for timeout (or until ctx is canceled) and
// returns the mixers found, deduplicated by serial and sorted by name. An empty
// result is not an error — a segmented network or firewall simply yields none.
func Scan(ctx context.Context, timeout time.Duration) ([]Mixer, error) {
	conn, err := listenUDP(Port)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()

	deadline := time.Now().Add(timeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}

	found := map[string]Mixer{}
	buf := make([]byte, 2048)
	for ctx.Err() == nil {
		now := time.Now()
		if !now.Before(deadline) {
			break
		}
		// Read in short slices so ctx cancellation and the overall deadline are
		// both honored without blocking a full window on one call.
		step := 250 * time.Millisecond
		if d := time.Until(deadline); d < step {
			step = d
		}
		_ = conn.SetReadDeadline(now.Add(step))

		n, addr, err := conn.ReadFrom(buf)
		if err != nil {
			if isTimeout(err) {
				continue
			}
			if errors.Is(err, net.ErrClosed) || errors.Is(err, os.ErrClosed) {
				break
			}
			return nil, err
		}
		m, ok := parseBroadcast(buf[:n], addr)
		if !ok {
			continue
		}
		found[m.Serial] = m
	}

	out := make([]Mixer, 0, len(found))
	for _, m := range found {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Serial < out[j].Serial
	})
	return out, nil
}

// parseBroadcast decodes one datagram into a Mixer. It returns ok=false for any
// packet that is not a well-formed mixer announcement (wrong magic, too short,
// or missing a serial). The IP comes from the datagram's source address.
func parseBroadcast(packet []byte, addr net.Addr) (Mixer, bool) {
	if len(packet) < 4 || !bytes.Equal(packet[:4], proto.Header[:]) {
		return Mixer{}, false
	}
	if len(packet) <= fragmentOffset {
		return Mixer{}, false
	}
	frags := splitNul(packet[fragmentOffset:])
	// [model, _, serial, name?] — index 2 is the serial; without it, ignore.
	if len(frags) < 3 || frags[2] == "" {
		return Mixer{}, false
	}
	ip, port := hostPort(addr)
	return Mixer{
		Name:   frags[0],
		Serial: frags[2],
		IP:     ip,
		Port:   port,
	}, true
}

// splitNul splits b on NUL bytes into UTF-8 strings, dropping a trailing empty
// fragment from a terminating NUL.
func splitNul(b []byte) []string {
	parts := bytes.Split(b, []byte{0x00})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, string(p))
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

// hostPort extracts the IP and port from a UDP source address.
func hostPort(addr net.Addr) (string, int) {
	if u, ok := addr.(*net.UDPAddr); ok {
		return u.IP.String(), u.Port
	}
	if host, _, err := net.SplitHostPort(addr.String()); err == nil {
		return host, 0
	}
	return addr.String(), 0
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
