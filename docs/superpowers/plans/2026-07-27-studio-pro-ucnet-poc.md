# Studio Pro UCNET Transport PoC Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build an unpublished `ucdawpoc` command that discovers the running Studio Pro DAW, starts playback for one second, and leaves the DAW stopped.

**Architecture:** Keep DAW discovery, handshake, and transport actions in a new `internal/daw` package so the published mixer client remains unchanged. Reuse the existing UCNET frame, JM, PV, CK, and TCP transport code, adding only the two captured message codecs that are missing (`PE` and `UM`). A thin Cobra command performs discovery and calls a safety wrapper that sends Stop before Play and guarantees a best-effort Stop afterward.

**Tech Stack:** Go 1.26.4, standard library networking and JSON, existing `internal/proto`, existing `internal/transport`, Cobra 1.10.2, Go `testing`.

## Global Constraints

- This is an unpublished protocol spike under `cmd/ucdawpoc`; do not add DAW commands to the published `ucmix` command.
- Do not change mixer state or the existing mixer connection handshake.
- Support only `transport/start` and `transport/stop`; do not add Record, arm, save, quit, locate, track, fader, plug-in, marker, or session commands.
- Discover the endpoint from its live `DA` announcement; do not hard-code `<LAN IP>`, UDP source port `62807`, or TCP control port `49601`.
- Accept only device class `DAW` with an identity ending in `.studioapp8`.
- Reject zero or multiple matching Studio Pro endpoints.
- Send Stop before the live Play test.
- After Play is attempted, make a best-effort Stop within one second even when Play confirmation fails or the process is interrupted.
- Treat the server's matching `PV` transport state update as command confirmation; do not invent an acknowledgment.
- Keep packet fixtures minimal and sanitized. Do not commit a packet capture, user license reply, real client UUID, computer key, network address, or unrelated Studio Pro state.
- Routine tests must use in-memory fakes or local sockets and must not contact the Mac Mini, iPad, gateway, mixer, or public network.

---

## File Structure

- `internal/daw/discovery.go`: Parse Studio Pro `DA` announcements, scan UDP port 47809, and select exactly one supported endpoint.
- `internal/daw/discovery_test.go`: Sanitized announcement fixtures and scan/selection tests.
- `internal/proto/frame.go`: Register the captured `PE` and `UM` UCNET message codes.
- `internal/proto/pe.go`: Encode and decode a momentary Studio Pro transport event.
- `internal/proto/pe_test.go`: Exact sanitized Play/Stop event fixtures and malformed-payload tests.
- `internal/proto/um.go`: Encode the UDP metering listener port advertised during the DAW handshake.
- `internal/proto/um_test.go`: Exact port byte-order and malformed-payload tests.
- `internal/daw/messages.go`: Captured Studio Pro license-query and subscription JM bodies plus DAW connection identities.
- `internal/daw/messages_test.go`: Exact sanitized JM and frame fixtures.
- `internal/daw/client.go`: Open the TCP and UDP session, complete the captured handshake, send transport actions, and wait for matching `PV` state.
- `internal/daw/client_test.go`: In-memory transport and local packet-connection tests for handshake, confirmation, errors, and cleanup.
- `internal/daw/playback.go`: Safe one-second playback wrapper with Stop-before-Play and deferred Stop behavior.
- `internal/daw/playback_test.go`: Action-order and failure-path tests.
- `cmd/ucdawpoc/main.go`: Unpublished Cobra command with `discover`, `transport play`, and `transport stop`.
- `cmd/ucdawpoc/main_test.go`: Command routing, endpoint errors, and output tests using injected package seams.

---

### Task 1: Parse and Select Studio Pro Announcements

**Files:**

- Create: `internal/daw/discovery.go`
- Create: `internal/daw/discovery_test.go`

**Interfaces:**

- Consumes: `proto.Header`, UDP announcements on port `47809`, and `context.Context`.
- Produces: `type Endpoint`, `func Scan(context.Context, time.Duration) ([]Endpoint, error)`, and `func SelectStudioPro([]Endpoint) (Endpoint, error)`.

- [ ] **Step 1: Write failing parser and selector tests**

Use a sanitized copy of the captured announcement. Its wire layout is:

```text
[0:4]   UC 00 01
[4:6]   TCP control port, uint16 little-endian
[6:8]   DA
[8]     server connection identity byte
[9:16]  reserved
[16:]   product/version, class, identity, display name; NUL-delimited
```

Create `internal/daw/discovery_test.go` with:

```go
package daw

import (
	"encoding/binary"
	"net"
	"testing"

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
		Product: "Studio Pro", Version: "8.1.1.113103 macOS Arm64",
		DeviceClass: "DAW", Identity: "sanitized.studioapp8",
		Name: "Test Mac mini", IP: "192.0.2.20", ControlPort: 49601,
		ServerID: 0x6b,
	}
	if got != want {
		t.Fatalf("parseAnnouncement = %+v, want %+v", got, want)
	}
}

func TestParseAnnouncementRejectsOtherTraffic(t *testing.T) {
	addr := &net.UDPAddr{IP: net.ParseIP("192.0.2.20"), Port: 62000}
	cases := map[string][]byte{
		"short":       proto.Header[:],
		"mixer class": announcement("Mixer", "board.serial", 53000),
		"wrong code":  func() []byte { p := announcement("DAW", "x.studioapp8", 49601); p[6] = 'P'; return p }(),
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
	if _, err := SelectStudioPro(nil); err == nil {
		t.Fatal("SelectStudioPro(nil) succeeded")
	}
	matches := []Endpoint{
		{DeviceClass: "DAW", Identity: "one.studioapp8"},
		{DeviceClass: "DAW", Identity: "two.studioapp8"},
	}
	if _, err := SelectStudioPro(matches); err == nil {
		t.Fatal("SelectStudioPro(two matches) succeeded")
	}
}
```

- [ ] **Step 2: Run the focused tests and verify the expected compile failure**

Run:

```bash
go test ./internal/daw -run 'Test(ParseAnnouncement|SelectStudioPro)' -count=1
```

Expected: FAIL because `Endpoint`, `parseAnnouncement`, and `SelectStudioPro` do not exist.

- [ ] **Step 3: Implement the endpoint parser and selector**

Create `internal/daw/discovery.go` with these public types and parsing rules:

```go
package daw

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/steveclarke/ucmix/internal/proto"
)

const DiscoveryPort = 47809
const announcementStringsOffset = 16

var (
	ErrNoStudioPro       = errors.New("daw: no Studio Pro endpoint found")
	ErrMultipleStudioPro = errors.New("daw: multiple Studio Pro endpoints found")
)

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
	host, _, err := net.SplitHostPort(addr.String())
	if udp, isUDP := addr.(*net.UDPAddr); isUDP {
		host = udp.IP.String()
	} else if err != nil {
		return Endpoint{}, false
	}
	return Endpoint{
		Product: product, Version: version, DeviceClass: parts[1],
		Identity: parts[2], Name: parts[3], IP: host,
		ControlPort: port, ServerID: packet[8],
	}, true
}

func splitNUL(data []byte) []string {
	raw := bytes.Split(data, []byte{0})
	out := make([]string, 0, len(raw))
	for _, part := range raw {
		if len(part) > 0 {
			out = append(out, string(part))
		}
	}
	return out
}

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
		return Endpoint{}, fmt.Errorf("%w: %d matches", ErrMultipleStudioPro, len(matches))
	}
}
```

- [ ] **Step 4: Run the focused tests and verify they pass**

Run:

```bash
go test ./internal/daw -run 'Test(ParseAnnouncement|SelectStudioPro)' -count=1
```

Expected: PASS.

- [ ] **Step 5: Add the passive UDP scan test and implementation**

Add a `net.PacketConn` fake following `internal/discovery/discovery_test.go`, then assert that `Scan`:

```go
type packet struct {
	data []byte
	addr net.Addr
}

type fakeAnnouncementConn struct {
	packets []packet
	next int
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
func (f *fakeAnnouncementConn) WriteTo([]byte, net.Addr) (int, error) {
	return 0, nil
}
func (f *fakeAnnouncementConn) Close() error { return nil }
func (f *fakeAnnouncementConn) LocalAddr() net.Addr { return nil }
func (f *fakeAnnouncementConn) SetDeadline(time.Time) error { return nil }
func (f *fakeAnnouncementConn) SetReadDeadline(value time.Time) error {
	f.deadline = value
	return nil
}
func (f *fakeAnnouncementConn) SetWriteDeadline(time.Time) error { return nil }

type fakeTimeout struct{}
func (fakeTimeout) Error() string { return "i/o timeout" }
func (fakeTimeout) Timeout() bool { return true }
func (fakeTimeout) Temporary() bool { return true }

func TestScanDeduplicatesByIdentity(t *testing.T) {
	addr := &net.UDPAddr{IP: net.ParseIP("192.0.2.20"), Port: 62000}
	fake := &fakeAnnouncementConn{packets: []packet{
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
```

Implement:

```go
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
	return lc.ListenPacket(context.Background(), "udp4",
		fmt.Sprintf("0.0.0.0:%d", DiscoveryPort))
}

func Scan(ctx context.Context, timeout time.Duration) ([]Endpoint, error) {
	conn, err := listenAnnouncements()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
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
```

This makes the 20 ms scan finish without touching the network. Do not call the
mixer reachability probe because each DA announcement supplies its own TCP
control port.

- [ ] **Step 6: Run all DAW discovery tests**

Run:

```bash
go test ./internal/daw -run 'Test(ParseAnnouncement|SelectStudioPro|Scan)' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit the discovery unit**

```bash
git add internal/daw/discovery.go internal/daw/discovery_test.go
git commit -m "feat(daw): discover Studio Pro endpoints"
```

---

### Task 2: Add Captured PE and UM Protocol Codecs

**Files:**

- Modify: `internal/proto/frame.go`
- Create: `internal/proto/pe.go`
- Create: `internal/proto/pe_test.go`
- Create: `internal/proto/um.go`
- Create: `internal/proto/um_test.go`

**Interfaces:**

- Consumes: `proto.Frame` and existing UCNET framing.
- Produces: `CodePE`, `CodeUM`, `MarshalPE(string, uint16)`, `UnmarshalPE([]byte)`, `MarshalUM(uint16)`, and `UnmarshalUM([]byte)`.

- [ ] **Step 1: Write exact PE fixture tests**

Create `internal/proto/pe_test.go`:

```go
package proto

import (
	"bytes"
	"testing"
)

func TestMarshalPEMatchesCapturedTransportEvents(t *testing.T) {
	tests := []struct {
		name string
		key  string
		state uint16
		want []byte
	}{
		{"play press", "transport/start", 2,
			[]byte("transport/start\x00\x02\x00")},
		{"play release", "transport/start", 0,
			[]byte("transport/start\x00\x00\x00")},
		{"stop press", "transport/stop", 2,
			[]byte("transport/stop\x00\x02\x00")},
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
```

- [ ] **Step 2: Run the PE tests and verify they fail**

Run:

```bash
go test ./internal/proto -run 'Test(MarshalPE|UnmarshalPE)' -count=1
```

Expected: FAIL because the PE functions do not exist.

- [ ] **Step 3: Implement PE**

Add `CodePE = [2]byte{'P', 'E'}` to the message-code block in `frame.go`.

Create `internal/proto/pe.go`:

```go
package proto

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

func MarshalPE(key string, state uint16) []byte {
	out := make([]byte, 0, len(key)+3)
	out = append(out, key...)
	out = append(out, 0)
	var value [2]byte
	binary.LittleEndian.PutUint16(value[:], state)
	return append(out, value[:]...)
}

func UnmarshalPE(payload []byte) (string, uint16, error) {
	i := bytes.IndexByte(payload, 0)
	if i <= 0 || len(payload) != i+3 {
		return "", 0, fmt.Errorf("proto: malformed PE payload")
	}
	return string(payload[:i]), binary.LittleEndian.Uint16(payload[i+1:]), nil
}
```

- [ ] **Step 4: Write and run UM tests**

Create `internal/proto/um_test.go`:

```go
package proto

import (
	"bytes"
	"testing"
)

func TestMarshalUMUsesLittleEndianUDPPort(t *testing.T) {
	got := MarshalUM(58395)
	want := []byte{0x1b, 0xe4}
	if !bytes.Equal(got, want) {
		t.Fatalf("MarshalUM = % x, want % x", got, want)
	}
	port, err := UnmarshalUM(got)
	if err != nil || port != 58395 {
		t.Fatalf("UnmarshalUM = %d, %v", port, err)
	}
}

func TestUnmarshalUMRequiresTwoBytes(t *testing.T) {
	for _, payload := range [][]byte{nil, []byte{1}, []byte{1, 2, 3}} {
		if _, err := UnmarshalUM(payload); err == nil {
			t.Fatalf("UnmarshalUM(% x) succeeded", payload)
		}
	}
}
```

Run:

```bash
go test ./internal/proto -run 'Test(MarshalUM|UnmarshalUM)' -count=1
```

Expected: FAIL because the UM functions do not exist.

- [ ] **Step 5: Implement UM and run all focused codec tests**

Add `CodeUM = [2]byte{'U', 'M'}` to `frame.go`.

Create `internal/proto/um.go`:

```go
package proto

import (
	"encoding/binary"
	"fmt"
)

func MarshalUM(port uint16) []byte {
	out := make([]byte, 2)
	binary.LittleEndian.PutUint16(out, port)
	return out
}

func UnmarshalUM(payload []byte) (uint16, error) {
	if len(payload) != 2 {
		return 0, fmt.Errorf("proto: UM payload is %d bytes, need 2", len(payload))
	}
	return binary.LittleEndian.Uint16(payload), nil
}
```

Run:

```bash
go test ./internal/proto -run 'Test(MarshalPE|UnmarshalPE|MarshalUM|UnmarshalUM)' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit the codecs**

```bash
git add internal/proto/frame.go internal/proto/pe.go internal/proto/pe_test.go internal/proto/um.go internal/proto/um_test.go
git commit -m "feat(proto): encode DAW transport events"
```

---

### Task 3: Encode the Studio Pro Handshake

**Files:**

- Create: `internal/daw/messages.go`
- Create: `internal/daw/messages_test.go`

**Interfaces:**

- Consumes: `Endpoint.ServerID`, `proto.Frame`, and `proto.MarshalJM`.
- Produces: `mainConnID(Endpoint)`, `meterConnID(Endpoint)`, `queryUserLicense()`, and `subscribe(string)`.

- [ ] **Step 1: Write exact sanitized handshake tests**

Create `internal/daw/messages_test.go`:

```go
package daw

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/steveclarke/ucmix/internal/proto"
)

func TestConnectionIdentitiesUseAnnouncedServerID(t *testing.T) {
	endpoint := Endpoint{ServerID: 0x6b}
	if got := mainConnID(endpoint); got != [4]byte{0x68, 0, 0x6b, 0} {
		t.Fatalf("mainConnID = % x", got)
	}
	if got := meterConnID(endpoint); got != [4]byte{0, 0, 0x6b, 0} {
		t.Fatalf("meterConnID = % x", got)
	}
}

func TestQueryUserLicenseMessage(t *testing.T) {
	frame := proto.Frame{
		Code: proto.CodeJM, ConnID: [4]byte{0x68, 0, 0x6b, 0},
		Payload: proto.MarshalJM(queryUserLicense()),
	}
	msg, err := proto.ParseJM(frame.Payload)
	if err != nil || msg.ID != "QueryUserLicense" {
		t.Fatalf("ParseJM = %+v, %v", msg, err)
	}
	var body map[string]any
	if err := json.Unmarshal(msg.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body["clientInternalName"] != "dawremote" {
		t.Fatalf("clientInternalName = %v", body["clientInternalName"])
	}
}

func TestSubscribeMatchesCapturedDAWFields(t *testing.T) {
	payload := proto.MarshalJM(subscribe("00000000-0000-4000-8000-000000000000"))
	msg, err := proto.ParseJM(payload)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte(`{"id":"Subscribe","clientName":"Studio Pro Remote","clientInternalName":"dawremote","clientType":"iPad","clientDescription":"iPad","clientIdentifier":"00000000-0000-4000-8000-000000000000","clientOptions":"surround","clientEncoding":23117}`)
	if !bytes.Equal(msg.Body, want) {
		t.Fatalf("Subscribe body = %s, want %s", msg.Body, want)
	}
}
```

- [ ] **Step 2: Run the message tests and verify they fail**

Run:

```bash
go test ./internal/daw -run 'Test(ConnectionIdentities|QueryUserLicense|Subscribe)' -count=1
```

Expected: FAIL because the handshake helpers do not exist.

- [ ] **Step 3: Implement the closed handshake message types**

Create `internal/daw/messages.go`:

```go
package daw

type queryLicenseMessage struct {
	ID                 string `json:"id"`
	ClientInternalName string `json:"clientInternalName"`
}

type subscribeMessage struct {
	ID                 string `json:"id"`
	ClientName         string `json:"clientName"`
	ClientInternalName string `json:"clientInternalName"`
	ClientType         string `json:"clientType"`
	ClientDescription  string `json:"clientDescription"`
	ClientIdentifier   string `json:"clientIdentifier"`
	ClientOptions      string `json:"clientOptions"`
	ClientEncoding     int    `json:"clientEncoding"`
}

func mainConnID(endpoint Endpoint) [4]byte {
	return [4]byte{0x68, 0, endpoint.ServerID, 0}
}

func meterConnID(endpoint Endpoint) [4]byte {
	return [4]byte{0, 0, endpoint.ServerID, 0}
}

func queryUserLicense() queryLicenseMessage {
	return queryLicenseMessage{
		ID: "QueryUserLicense", ClientInternalName: "dawremote",
	}
}

func subscribe(identifier string) subscribeMessage {
	return subscribeMessage{
		ID: "Subscribe", ClientName: "Studio Pro Remote",
		ClientInternalName: "dawremote", ClientType: "iPad",
		ClientDescription: "iPad", ClientIdentifier: identifier,
		ClientOptions: "surround", ClientEncoding: 23117,
	}
}
```

- [ ] **Step 4: Run the message tests**

Run:

```bash
go test ./internal/daw -run 'Test(ConnectionIdentities|QueryUserLicense|Subscribe)' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the handshake messages**

```bash
git add internal/daw/messages.go internal/daw/messages_test.go
git commit -m "feat(daw): encode Studio Pro handshake"
```

---

### Task 4: Connect and Confirm Play/Stop State

**Files:**

- Create: `internal/daw/client.go`
- Create: `internal/daw/client_test.go`

**Interfaces:**

- Consumes: `Endpoint`, `transport.Dial`, `proto.MarshalJM`, `proto.MarshalUM`, `proto.MarshalPE`, `proto.MarshalPV`, and inbound `JM`/`PV` frames.
- Produces: `type Action`, `const Play`, `const Stop`, `func Connect(context.Context, Endpoint, ...Option) (*Client, error)`, `func (*Client) Trigger(context.Context, Action) error`, and `func (*Client) Close() error`.

- [ ] **Step 1: Write the failing handshake test with in-memory seams**

In `internal/daw/client_test.go`, define a fake `transport.Transport` that records frames and supplies inbound frames, and a fake `net.PacketConn` whose `LocalAddr` is UDP port 58395. Swap package seams:

```go
var dialTransport = transport.Dial
var listenMetering = func() (net.PacketConn, error) {
	return net.ListenPacket("udp4", "0.0.0.0:0")
}
```

Test this exact handshake order:

```go
func TestConnectPerformsCapturedHandshake(t *testing.T) {
	endpoint := Endpoint{
		IP: "192.0.2.20", ControlPort: 49601, ServerID: 0x6b,
	}
	tcp := newFakeTransport()
	udp := newFakePacketConn(58395)
	useClientFakes(t, tcp, udp)
	tcp.onSend = func(frame proto.Frame) {
		if frame.Code != proto.CodeJM {
			return
		}
		msg, _ := proto.ParseJM(frame.Payload)
		if msg.ID == "QueryUserLicense" {
			go tcp.deliver(proto.Frame{
				Code: proto.CodeJM,
				Payload: proto.MarshalJM(map[string]string{
					"id": "UserLicenseReply",
				}),
			})
		}
	}

	client, err := Connect(
		context.Background(), endpoint,
		ClientIdentifier("00000000-0000-4000-8000-000000000000"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}

	sent := tcp.sentFrames()
	if len(sent) != 3 {
		t.Fatalf("sent %d frames, want 3", len(sent))
	}
	if sent[0].Code != proto.CodeJM ||
		sent[0].ConnID != [4]byte{0x68, 0, 0x6b, 0} {
		t.Fatalf("license frame = %+v", sent[0])
	}
	msg, err := proto.ParseJM(sent[0].Payload)
	if err != nil || msg.ID != "QueryUserLicense" {
		t.Fatalf("license message = %+v, %v", msg, err)
	}
	if sent[1].Code != proto.CodeUM ||
		sent[1].ConnID != [4]byte{0, 0, 0x6b, 0} ||
		!bytes.Equal(sent[1].Payload, []byte{0x1b, 0xe4}) {
		t.Fatalf("UM frame = %+v", sent[1])
	}
	msg, err = proto.ParseJM(sent[2].Payload)
	if err != nil || msg.ID != "Subscribe" ||
		sent[2].ConnID != [4]byte{0x68, 0, 0x6b, 0} {
		t.Fatalf("subscribe frame = %+v, message = %+v, err = %v", sent[2], msg, err)
	}
	if !tcp.closed() || !udp.closed() {
		t.Fatal("Connect client did not close both sockets")
	}
}
```

The helpers used by this test are concrete in-memory implementations:

```go
type fakeTransport struct {
	mu sync.Mutex
	sent []proto.Frame
	in chan proto.Frame
	done chan struct{}
	onSend func(proto.Frame)
}

func newFakeTransport() *fakeTransport {
	return &fakeTransport{
		in: make(chan proto.Frame, 32), done: make(chan struct{}),
	}
}

func (f *fakeTransport) Send(_ context.Context, frame proto.Frame) error {
	f.mu.Lock()
	f.sent = append(f.sent, frame)
	hook := f.onSend
	f.mu.Unlock()
	if hook != nil {
		hook(frame)
	}
	return nil
}

func (f *fakeTransport) Frames() <-chan proto.Frame { return f.in }
func (f *fakeTransport) Close() error {
	select {
	case <-f.done:
	default:
		close(f.done)
		close(f.in)
	}
	return nil
}
func (f *fakeTransport) deliver(frame proto.Frame) { f.in <- frame }
func (f *fakeTransport) sentFrames() []proto.Frame {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]proto.Frame(nil), f.sent...)
}
func (f *fakeTransport) closed() bool {
	select {
	case <-f.done:
		return true
	default:
		return false
	}
}

type fakePacketConn struct {
	local *net.UDPAddr
	done chan struct{}
}

func newFakePacketConn(port int) *fakePacketConn {
	return &fakePacketConn{
		local: &net.UDPAddr{IP: net.IPv4zero, Port: port},
		done: make(chan struct{}),
	}
}
func (f *fakePacketConn) ReadFrom([]byte) (int, net.Addr, error) {
	return 0, nil, net.ErrClosed
}
func (f *fakePacketConn) WriteTo(p []byte, _ net.Addr) (int, error) {
	return len(p), nil
}
func (f *fakePacketConn) Close() error {
	select {
	case <-f.done:
	default:
		close(f.done)
	}
	return nil
}
func (f *fakePacketConn) LocalAddr() net.Addr { return f.local }
func (f *fakePacketConn) SetDeadline(time.Time) error { return nil }
func (f *fakePacketConn) SetReadDeadline(time.Time) error { return nil }
func (f *fakePacketConn) SetWriteDeadline(time.Time) error { return nil }
func (f *fakePacketConn) closed() bool {
	select {
	case <-f.done:
		return true
	default:
		return false
	}
}

func useClientFakes(t *testing.T, tcp *fakeTransport, udp *fakePacketConn) {
	t.Helper()
	oldDial, oldListen := dialTransport, listenMetering
	dialTransport = func(
		context.Context, string, transport.Options,
	) (transport.Transport, error) {
		return tcp, nil
	}
	listenMetering = func() (net.PacketConn, error) { return udp, nil }
	t.Cleanup(func() {
		dialTransport, listenMetering = oldDial, oldListen
	})
}
```

The injected reply contains no captured license, user, or computer values:

```go
	reply := proto.Frame{
		Code: proto.CodeJM,
		Payload: proto.MarshalJM(map[string]string{"id": "UserLicenseReply"}),
	}
```

- [ ] **Step 2: Run the handshake test and verify it fails**

Run:

```bash
go test ./internal/daw -run TestConnectPerformsCapturedHandshake -count=1
```

Expected: FAIL because the client does not exist.

- [ ] **Step 3: Implement options, connection setup, and cleanup**

Create the following foundation in `internal/daw/client.go`:

```go
package daw

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/steveclarke/ucmix/internal/proto"
	"github.com/steveclarke/ucmix/internal/transport"
)

var (
	dialTransport = transport.Dial
	listenMetering = func() (net.PacketConn, error) {
		return net.ListenPacket("udp4", "0.0.0.0:0")
	}
)

var ErrConnectionClosed = errors.New("daw: connection closed")

type Action string

const (
	Play Action = "play"
	Stop Action = "stop"
)

type config struct {
	identifier string
	transport  transport.Options
}

type Option func(*config)

func ClientIdentifier(value string) Option {
	return func(cfg *config) { cfg.identifier = value }
}

type Client struct {
	endpoint Endpoint
	t        transport.Transport
	metering net.PacketConn
	closeOnce sync.Once
	closeErr  error
}

func Connect(ctx context.Context, endpoint Endpoint, opts ...Option) (*Client, error) {
	cfg := config{transport: transport.Options{KeepAlive: time.Hour}}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.identifier == "" {
		cfg.identifier = newIdentifier()
	}
	metering, err := listenMetering()
	if err != nil {
		return nil, fmt.Errorf("daw: opening UDP metering socket: %w", err)
	}
	addr := net.JoinHostPort(endpoint.IP, strconv.Itoa(endpoint.ControlPort))
	t, err := dialTransport(ctx, addr, cfg.transport)
	if err != nil {
		_ = metering.Close()
		return nil, fmt.Errorf("daw: connecting to %s: %w", addr, err)
	}
	client := &Client{endpoint: endpoint, t: t, metering: metering}
	if err := client.handshake(ctx, cfg.identifier); err != nil {
		_ = client.Close()
		return nil, err
	}
	return client, nil
}

func newIdentifier() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "00000000-0000-4000-8000-000000000000"
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	raw := hex.EncodeToString(value[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		raw[0:8], raw[8:12], raw[12:16], raw[16:20], raw[20:32])
}

func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		c.closeErr = errors.Join(c.t.Close(), c.metering.Close())
	})
	return c.closeErr
}
```

Implement the handshake exactly as:

```go
func (c *Client) handshake(ctx context.Context, identifier string) error {
	if err := c.t.Send(ctx, proto.Frame{
		Code: proto.CodeJM,
		ConnID: mainConnID(c.endpoint),
		Payload: proto.MarshalJM(queryUserLicense()),
	}); err != nil {
		return fmt.Errorf("daw: sending license query: %w", err)
	}

	licensed := false
	for !licensed {
		select {
		case frame, ok := <-c.t.Frames():
			if !ok {
				return ErrConnectionClosed
			}
			if frame.Code != proto.CodeJM {
				continue
			}
			message, err := proto.ParseJM(frame.Payload)
			if err == nil && message.ID == "UserLicenseReply" {
				licensed = true
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	local, ok := c.metering.LocalAddr().(*net.UDPAddr)
	if !ok || local.Port <= 0 || local.Port > 65535 {
		return fmt.Errorf(
			"daw: invalid UDP metering address %v",
			c.metering.LocalAddr(),
		)
	}
	if err := c.t.Send(ctx, proto.Frame{
		Code: proto.CodeUM,
		ConnID: meterConnID(c.endpoint),
		Payload: proto.MarshalUM(uint16(local.Port)),
	}); err != nil {
		return fmt.Errorf("daw: advertising UDP metering port: %w", err)
	}
	if err := c.t.Send(ctx, proto.Frame{
		Code: proto.CodeJM,
		ConnID: mainConnID(c.endpoint),
		Payload: proto.MarshalJM(subscribe(identifier)),
	}); err != nil {
		return fmt.Errorf("daw: sending Subscribe: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run the handshake test**

Run:

```bash
go test ./internal/daw -run TestConnectPerformsCapturedHandshake -count=1
```

Expected: PASS.

- [ ] **Step 5: Write failing transport-action tests**

Add:

```go
func actionClient(t *testing.T) (*Client, *fakeTransport) {
	t.Helper()
	tcp := newFakeTransport()
	udp := newFakePacketConn(58395)
	client := &Client{
		endpoint: Endpoint{ServerID: 0x6b},
		t: tcp, metering: udp,
	}
	t.Cleanup(func() { _ = client.Close() })
	return client, tcp
}

func TestTriggerPlaySendsCapturedSequenceAndWaitsForState(t *testing.T) {
	client, tcp := actionClient(t)
	tcp.onSend = func(frame proto.Frame) {
		if frame.Code != proto.CodePE {
			return
		}
		key, state, _ := proto.UnmarshalPE(frame.Payload)
		if key == "transport/start" && state == 0 {
			go tcp.deliver(proto.Frame{
				Code: proto.CodePV,
				Payload: proto.MarshalPV("transport/start", 1),
			})
		}
	}
	if err := client.Trigger(context.Background(), Play); err != nil {
		t.Fatal(err)
	}
	assertActionFrames(t, tcp.sentFrames(), "transport/start")
}

func TestTriggerStopUsesDistinctPath(t *testing.T) {
	client, tcp := actionClient(t)
	tcp.onSend = func(frame proto.Frame) {
		if frame.Code != proto.CodePE {
			return
		}
		key, state, _ := proto.UnmarshalPE(frame.Payload)
		if key == "transport/stop" && state == 0 {
			go tcp.deliver(proto.Frame{
				Code: proto.CodePV,
				Payload: proto.MarshalPV("transport/stop", 1),
			})
		}
	}
	if err := client.Trigger(context.Background(), Stop); err != nil {
		t.Fatal(err)
	}
	assertActionFrames(t, tcp.sentFrames(), "transport/stop")
}

func assertActionFrames(t *testing.T, frames []proto.Frame, key string) {
	t.Helper()
	if len(frames) != 3 {
		t.Fatalf("sent %d frames, want 3", len(frames))
	}
	wantCodes := [][2]byte{proto.CodePE, proto.CodePV, proto.CodePE}
	for i, want := range wantCodes {
		if frames[i].Code != want ||
			frames[i].ConnID != [4]byte{0x68, 0, 0x6b, 0} {
			t.Fatalf("frame %d = %+v", i, frames[i])
		}
	}
	if gotKey, state, err := proto.UnmarshalPE(frames[0].Payload);
		err != nil || gotKey != key || state != 2 {
		t.Fatalf("press = %q, %d, %v", gotKey, state, err)
	}
	if gotKey, value, err := proto.UnmarshalPV(frames[1].Payload);
		err != nil || gotKey != key || value != 1 {
		t.Fatalf("value = %q, %f, %v", gotKey, value, err)
	}
	if gotKey, state, err := proto.UnmarshalPE(frames[2].Payload);
		err != nil || gotKey != key || state != 0 {
		t.Fatalf("release = %q, %d, %v", gotKey, state, err)
	}
}

func TestTriggerRejectsUnknownAction(t *testing.T) {
	client, tcp := actionClient(t)
	if err := client.Trigger(context.Background(), Action("record")); err == nil {
		t.Fatal("Trigger(record) succeeded")
	}
	if got := len(tcp.sentFrames()); got != 0 {
		t.Fatalf("Trigger(record) sent %d frames", got)
	}
}

func TestTriggerRequiresMatchingConfirmation(t *testing.T) {
	client, tcp := actionClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	tcp.onSend = func(frame proto.Frame) {
		if frame.Code != proto.CodePE {
			return
		}
		key, state, _ := proto.UnmarshalPE(frame.Payload)
		if key == "transport/start" && state == 0 {
			go func() {
				tcp.deliver(proto.Frame{
					Code: proto.CodePV,
					Payload: proto.MarshalPV("transport/start", 0),
				})
				tcp.deliver(proto.Frame{
					Code: proto.CodePV,
					Payload: proto.MarshalPV("unrelated", 1),
				})
				cancel()
			}()
		}
	}
	if err := client.Trigger(ctx, Play); !errors.Is(err, context.Canceled) {
		t.Fatalf("Trigger = %v, want context.Canceled", err)
	}
}
```

- [ ] **Step 6: Run the action tests and verify they fail**

Run:

```bash
go test ./internal/daw -run 'TestTrigger' -count=1
```

Expected: FAIL because `Trigger` does not exist.

- [ ] **Step 7: Implement the captured action sequence**

Add:

```go
func (c *Client) Trigger(ctx context.Context, action Action) error {
	var key string
	switch action {
	case Play:
		key = "transport/start"
	case Stop:
		key = "transport/stop"
	default:
		return fmt.Errorf("daw: unsupported transport action %q", action)
	}
	connID := mainConnID(c.endpoint)
	frames := []proto.Frame{
		{Code: proto.CodePE, ConnID: connID, Payload: proto.MarshalPE(key, 2)},
		{Code: proto.CodePV, ConnID: connID, Payload: proto.MarshalPV(key, 1)},
		{Code: proto.CodePE, ConnID: connID, Payload: proto.MarshalPE(key, 0)},
	}
	for _, frame := range frames {
		if err := c.t.Send(ctx, frame); err != nil {
			return fmt.Errorf("daw: sending %s: %w", action, err)
		}
	}
	for {
		select {
		case frame, ok := <-c.t.Frames():
			if !ok {
				return ErrConnectionClosed
			}
			if frame.Code != proto.CodePV {
				continue
			}
			gotKey, value, err := proto.UnmarshalPV(frame.Payload)
			if err == nil && gotKey == key && value == 1 {
				return nil
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
```

The confirmation loop deliberately drains unrelated initial `CK` snapshot chunks and unrelated state frames until the matching transport state arrives.

- [ ] **Step 8: Run all DAW client tests and the race detector**

Run:

```bash
go test -race ./internal/daw -run 'Test(Connect|Trigger)' -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit the DAW client**

```bash
git add internal/daw/client.go internal/daw/client_test.go
git commit -m "feat(daw): connect and control Studio Pro transport"
```

---

### Task 5: Guarantee the Live Test Leaves Studio Pro Stopped

**Files:**

- Create: `internal/daw/playback.go`
- Create: `internal/daw/playback_test.go`

**Interfaces:**

- Consumes: any `Controller` implementing `Trigger(context.Context, Action) error`.
- Produces: `func PulsePlayback(context.Context, Controller, time.Duration) error`.

- [ ] **Step 1: Write failure-path-first safety tests**

Create `internal/daw/playback_test.go` with a fake controller that records actions and can fail on a selected call:

```go
package daw

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

type fakeController struct {
	actions []Action
	failAt  int
}

func (f *fakeController) Trigger(_ context.Context, action Action) error {
	f.actions = append(f.actions, action)
	if f.failAt == len(f.actions) {
		return errors.New("injected failure")
	}
	return nil
}

func TestPulsePlaybackStopsBeforeAndAfterPlay(t *testing.T) {
	fake := &fakeController{}
	if err := PulsePlayback(context.Background(), fake, 0); err != nil {
		t.Fatal(err)
	}
	want := []Action{Stop, Play, Stop}
	if !reflect.DeepEqual(fake.actions, want) {
		t.Fatalf("actions = %v, want %v", fake.actions, want)
	}
}

func TestPulsePlaybackStopsWhenPlayConfirmationFails(t *testing.T) {
	fake := &fakeController{failAt: 2}
	if err := PulsePlayback(context.Background(), fake, 0); err == nil {
		t.Fatal("PulsePlayback succeeded")
	}
	want := []Action{Stop, Play, Stop}
	if !reflect.DeepEqual(fake.actions, want) {
		t.Fatalf("actions = %v, want %v", fake.actions, want)
	}
}

func TestPulsePlaybackDoesNotPlayWhenInitialStopFails(t *testing.T) {
	fake := &fakeController{failAt: 1}
	if err := PulsePlayback(context.Background(), fake, time.Second); err == nil {
		t.Fatal("PulsePlayback succeeded")
	}
	if want := []Action{Stop}; !reflect.DeepEqual(fake.actions, want) {
		t.Fatalf("actions = %v, want %v", fake.actions, want)
	}
}
```

- [ ] **Step 2: Run the safety tests and verify they fail**

Run:

```bash
go test ./internal/daw -run TestPulsePlayback -count=1
```

Expected: FAIL because `Controller` and `PulsePlayback` do not exist.

- [ ] **Step 3: Implement the guarded playback pulse**

Create `internal/daw/playback.go`:

```go
package daw

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type Controller interface {
	Trigger(context.Context, Action) error
}

const emergencyStopTimeout = 2 * time.Second

func PulsePlayback(ctx context.Context, controller Controller, duration time.Duration) (err error) {
	if duration < 0 || duration > time.Second {
		return fmt.Errorf("daw: playback duration must be between 0 and 1s")
	}
	if err := controller.Trigger(ctx, Stop); err != nil {
		return fmt.Errorf("daw: initial stop: %w", err)
	}

	stopped := false
	defer func() {
		if stopped {
			return
		}
		stopCtx, cancel := context.WithTimeout(context.Background(), emergencyStopTimeout)
		defer cancel()
		stopErr := controller.Trigger(stopCtx, Stop)
		if stopErr != nil {
			err = errors.Join(err, fmt.Errorf("daw: emergency stop: %w", stopErr))
		}
	}()

	stopDeadline := time.Now().Add(time.Second)
	playCtx, cancelPlay := context.WithDeadline(ctx, stopDeadline)
	if err := controller.Trigger(playCtx, Play); err != nil {
		cancelPlay()
		return fmt.Errorf("daw: play: %w", err)
	}
	cancelPlay()
	wait := duration
	if remaining := time.Until(stopDeadline); wait > remaining {
		wait = remaining
	}
	if wait < 0 {
		wait = 0
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := controller.Trigger(ctx, Stop); err != nil {
		return fmt.Errorf("daw: final stop: %w", err)
	}
	stopped = true
	return nil
}
```

The deferred Stop uses a fresh bounded context, so cancellation or Ctrl-C cannot suppress the cleanup attempt.

- [ ] **Step 4: Add duration and cancellation tests**

Add tests asserting:

```go
func TestPulsePlaybackRejectsDurationOverOneSecond(t *testing.T) {
	fake := &fakeController{}
	if err := PulsePlayback(
		context.Background(), fake, time.Second+time.Nanosecond,
	); err == nil {
		t.Fatal("PulsePlayback succeeded")
	}
	if len(fake.actions) != 0 {
		t.Fatalf("invalid duration sent actions %v", fake.actions)
	}
}

type cancelingController struct {
	fakeController
	cancel context.CancelFunc
}

func (f *cancelingController) Trigger(ctx context.Context, action Action) error {
	err := f.fakeController.Trigger(ctx, action)
	if action == Play {
		f.cancel()
	}
	return err
}

func TestPulsePlaybackStopsAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	fake := &cancelingController{cancel: cancel}
	err := PulsePlayback(ctx, fake, time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("PulsePlayback = %v, want context.Canceled", err)
	}
	want := []Action{Stop, Play, Stop}
	if !reflect.DeepEqual(fake.actions, want) {
		t.Fatalf("actions = %v, want %v", fake.actions, want)
	}
}
```

The cancellation test uses an already-cancelable context, starts `PulsePlayback` with a one-second duration, waits until the fake records Play, cancels, and asserts the final action is Stop.

- [ ] **Step 5: Run the safety tests with the race detector**

Run:

```bash
go test -race ./internal/daw -run TestPulsePlayback -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit the safety wrapper**

```bash
git add internal/daw/playback.go internal/daw/playback_test.go
git commit -m "feat(daw): guarantee stop after playback probe"
```

---

### Task 6: Add the Unpublished `ucdawpoc` Command

**Files:**

- Create: `cmd/ucdawpoc/main.go`
- Create: `cmd/ucdawpoc/main_test.go`

**Interfaces:**

- Consumes: `daw.Scan`, `daw.SelectStudioPro`, `daw.Connect`, `daw.PulsePlayback`, and Cobra.
- Produces: `ucdawpoc discover`, `ucdawpoc transport play`, and `ucdawpoc transport stop`.

- [ ] **Step 1: Write command tests against injected seams**

In `cmd/ucdawpoc/main.go`, expose package seams with their production defaults:

```go
var (
	scanDAWs = daw.Scan
	connectDAW = func(ctx context.Context, endpoint daw.Endpoint) (dawClient, error) {
		return daw.Connect(ctx, endpoint)
	}
)

type dawClient interface {
	daw.Controller
	Close() error
}
```

In `cmd/ucdawpoc/main_test.go`, replace those seams with fakes and test:

```go
package main

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/steveclarke/ucmix/internal/daw"
)

type fakeDAWClient struct {
	actions []daw.Action
	closed bool
}

func (f *fakeDAWClient) Trigger(
	_ context.Context, action daw.Action,
) error {
	f.actions = append(f.actions, action)
	return nil
}
func (f *fakeDAWClient) Close() error {
	f.closed = true
	return nil
}

func useCommandFakes(
	t *testing.T, endpoints []daw.Endpoint,
) *fakeDAWClient {
	t.Helper()
	client := &fakeDAWClient{}
	oldScan, oldConnect := scanDAWs, connectDAW
	scanDAWs = func(
		context.Context, time.Duration,
	) ([]daw.Endpoint, error) {
		return endpoints, nil
	}
	connectDAW = func(
		context.Context, daw.Endpoint,
	) (dawClient, error) {
		return client, nil
	}
	t.Cleanup(func() {
		scanDAWs, connectDAW = oldScan, oldConnect
	})
	return client
}

func executeCommand(t *testing.T, args ...string) (string, error) {
	t.Helper()
	command := newRootCmd()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs(args)
	err := command.ExecuteContext(context.Background())
	return output.String(), err
}

func testEndpoint(identity string) daw.Endpoint {
	return daw.Endpoint{
		Product: "Studio Pro",
		Version: "8.1.1.113103 macOS Arm64",
		DeviceClass: "DAW",
		Identity: identity,
		Name: "Test Mac mini",
		IP: "192.0.2.20",
		ControlPort: 49601,
		ServerID: 0x6b,
	}
}

func TestDiscoverPrintsOnlyParsedDAWs(t *testing.T) {
	useCommandFakes(t, []daw.Endpoint{
		testEndpoint("sanitized.studioapp8"),
	})
	output, err := executeCommand(t, "discover")
	if err != nil {
		t.Fatal(err)
	}
	want := "Studio Pro 8.1.1.113103 macOS Arm64 — " +
		"Test Mac mini (192.0.2.20:49601)\n"
	if output != want {
		t.Fatalf("output = %q, want %q", output, want)
	}
}

func TestTransportStopDiscoversConnectsAndStops(t *testing.T) {
	client := useCommandFakes(t, []daw.Endpoint{
		testEndpoint("sanitized.studioapp8"),
	})
	output, err := executeCommand(t, "transport", "stop")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(client.actions, []daw.Action{daw.Stop}) {
		t.Fatalf("actions = %v", client.actions)
	}
	if output != "stop confirmed\n" || !client.closed {
		t.Fatalf("output = %q, closed = %v", output, client.closed)
	}
}

func TestTransportPlayUsesGuardedPulse(t *testing.T) {
	client := useCommandFakes(t, []daw.Endpoint{
		testEndpoint("sanitized.studioapp8"),
	})
	output, err := executeCommand(
		t, "transport", "play", "--duration", "0s",
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []daw.Action{daw.Stop, daw.Play, daw.Stop}
	if !reflect.DeepEqual(client.actions, want) {
		t.Fatalf("actions = %v, want %v", client.actions, want)
	}
	if output != "playback confirmed; Studio Pro stopped\n" {
		t.Fatalf("output = %q", output)
	}
}

func TestTransportRejectsZeroMatches(t *testing.T) {
	useCommandFakes(t, nil)
	_, err := executeCommand(t, "transport", "stop")
	if !errors.Is(err, daw.ErrNoStudioPro) {
		t.Fatalf("error = %v", err)
	}
}

func TestTransportRejectsMultipleMatches(t *testing.T) {
	useCommandFakes(t, []daw.Endpoint{
		testEndpoint("one.studioapp8"),
		testEndpoint("two.studioapp8"),
	})
	_, err := executeCommand(t, "transport", "stop")
	if !errors.Is(err, daw.ErrMultipleStudioPro) {
		t.Fatalf("error = %v", err)
	}
}
```

For Play, assert the fake client records `Stop, Play, Stop`. For Stop, assert exactly one `Stop`. Capture command output with a `bytes.Buffer`; do not use process-global stdout in tests.

- [ ] **Step 2: Run the command tests and verify they fail**

Run:

```bash
go test ./cmd/ucdawpoc -count=1
```

Expected: FAIL because the command does not exist.

- [ ] **Step 3: Implement the Cobra command tree**

Create `cmd/ucdawpoc/main.go` with:

```go
func newRootCmd() *cobra.Command {
	var scanTimeout time.Duration
	root := &cobra.Command{
		Use: "ucdawpoc",
		Short: "Probe Studio Pro transport over UCNET",
		SilenceUsage: true,
		SilenceErrors: true,
	}
	root.PersistentFlags().DurationVar(
		&scanTimeout, "scan-timeout", 3*time.Second, "DAW discovery window",
	)
	root.AddCommand(
		newDiscoverCmd(&scanTimeout),
		newTransportCmd(&scanTimeout),
	)
	return root
}
```

Implement the subcommands as:

```go
func newDiscoverCmd(scanTimeout *time.Duration) *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{
		Use: "discover",
		Short: "Find Studio Pro DAWs",
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			endpoints, err := scanDAWs(command.Context(), *scanTimeout)
			if err != nil {
				return err
			}
			if jsonOutput {
				encoder := json.NewEncoder(command.OutOrStdout())
				encoder.SetIndent("", "  ")
				return encoder.Encode(endpoints)
			}
			for _, endpoint := range endpoints {
				fmt.Fprintf(
					command.OutOrStdout(), "%s %s — %s (%s:%d)\n",
					endpoint.Product, endpoint.Version, endpoint.Name,
					endpoint.IP, endpoint.ControlPort,
				)
			}
			return nil
		},
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "emit JSON")
	return command
}

func newTransportCmd(scanTimeout *time.Duration) *cobra.Command {
	transportCommand := &cobra.Command{
		Use: "transport",
		Short: "Control Studio Pro transport",
	}
	var duration time.Duration
	playCommand := &cobra.Command{
		Use: "play",
		Short: "Play briefly, then stop",
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			err := withClient(
				command.Context(), *scanTimeout,
				func(client dawClient) error {
					return daw.PulsePlayback(
						command.Context(), client, duration,
					)
				},
			)
			if err == nil {
				fmt.Fprintln(
					command.OutOrStdout(),
					"playback confirmed; Studio Pro stopped",
				)
			}
			return err
		},
	}
	playCommand.Flags().DurationVar(
		&duration, "duration", time.Second,
		"playback duration (maximum 1s)",
	)
	stopCommand := &cobra.Command{
		Use: "stop",
		Short: "Stop playback",
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			err := withClient(
				command.Context(), *scanTimeout,
				func(client dawClient) error {
					return client.Trigger(command.Context(), daw.Stop)
				},
			)
			if err == nil {
				fmt.Fprintln(command.OutOrStdout(), "stop confirmed")
			}
			return err
		},
	}
	transportCommand.AddCommand(playCommand, stopCommand)
	return transportCommand
}
```

`discover` prints this text form:

```text
Studio Pro 8.1.1.113103 macOS Arm64 — Test Mac mini (192.0.2.20:49601)
```

Both transport commands call:

```go
func withClient(ctx context.Context, scanTimeout time.Duration, fn func(dawClient) error) error {
	endpoints, err := scanDAWs(ctx, scanTimeout)
	if err != nil {
		return err
	}
	endpoint, err := daw.SelectStudioPro(endpoints)
	if err != nil {
		return err
	}
	client, err := connectDAW(ctx, endpoint)
	if err != nil {
		return err
	}
	defer client.Close()
	return fn(client)
}
```

`main` must install a signal-aware context:

```go
func main() {
	ctx, cancel := signal.NotifyContext(
		context.Background(), os.Interrupt, syscall.SIGTERM,
	)
	defer cancel()
	cmd := newRootCmd()
	cmd.SetContext(ctx)
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
```

- [ ] **Step 4: Run command tests**

Run:

```bash
go test ./cmd/ucdawpoc -count=1
```

Expected: PASS.

- [ ] **Step 5: Run local help without contacting a device**

Run:

```bash
go run ./cmd/ucdawpoc --help
go run ./cmd/ucdawpoc transport play --help
```

Expected: both commands exit 0, list only the designed command surface, and state the one-second maximum.

- [ ] **Step 6: Commit the command**

```bash
git add cmd/ucdawpoc/main.go cmd/ucdawpoc/main_test.go
git commit -m "feat: add Studio Pro UCNET transport PoC"
```

---

### Task 7: Verify the Repository and the Live DAW

**Files:**

- Modify only if a verification failure identifies a defect in files from Tasks 1–6.
- Do not add packet captures, volatile endpoint values, or live license data.

**Interfaces:**

- Consumes: completed `ucdawpoc` command and the safe Studio Pro session.
- Produces: test evidence plus live UCNET Stop, Play, Stop confirmation.

- [ ] **Step 1: Format and inspect the diff**

Run:

```bash
gofmt -w internal/daw internal/proto/pe.go internal/proto/pe_test.go internal/proto/um.go internal/proto/um_test.go cmd/ucdawpoc
git diff --check
git status --short
```

Expected: `git diff --check` exits 0 and status contains only intended PoC files.

- [ ] **Step 2: Run focused and full automated verification**

Run:

```bash
go test -race ./internal/daw ./internal/proto ./cmd/ucdawpoc
go test ./...
go vet ./...
```

Expected: every command exits 0.

- [ ] **Step 3: Discover the live Studio Pro endpoint**

Run:

```bash
go run ./cmd/ucdawpoc discover --scan-timeout 5s
```

Expected: exactly one Studio Pro 8 endpoint on the Mac Mini, with the TCP port decoded from the announcement rather than the UDP source port.

- [ ] **Step 4: Send and confirm Stop**

Run:

```bash
go run ./cmd/ucdawpoc transport stop --scan-timeout 5s
```

Expected: output `stop confirmed`, backed by inbound `PV transport/stop=1`.

- [ ] **Step 5: Run the bounded playback proof**

Run:

```bash
go run ./cmd/ucdawpoc transport play --duration 1s --scan-timeout 5s
```

Expected:

1. Initial Stop is confirmed.
2. Playback starts and inbound `PV transport/start=1` confirms receipt.
3. Stop is sent no later than one second after Play confirmation.
4. Inbound `PV transport/stop=1` confirms the final state.
5. Output is `playback confirmed; Studio Pro stopped`.

- [ ] **Step 6: Confirm no mixer control path was used**

Inspect the live command trace or debug log and assert:

```text
DAW endpoint class: DAW
DAW TCP destination: announced control port
Transport paths sent: transport/start, transport/stop
Mixer TCP destination 53000: not contacted
Mixer parameter paths: not sent
```

- [ ] **Step 7: Remove the temporary iPad capture interface**

Run:

```bash
sudo rvictl -x 00008103-001A01593404C01E
```

Expected: `rvi0` is removed. This cleans up the diagnostic capture setup; it does not change the DAW, mixer, or repository.

- [ ] **Step 8: Commit any verification-only fixes, then verify the final tree**

If live verification required a code correction, stage only the changed PoC
files from this explicit set and commit them:

```bash
git add internal/daw internal/proto/frame.go internal/proto/pe.go internal/proto/pe_test.go internal/proto/um.go internal/proto/um_test.go cmd/ucdawpoc
git commit -m "fix(daw): correct live UCNET transport exchange"
```

Then run:

```bash
git status --short
go test ./...
```

Expected: clean status and all tests passing.
