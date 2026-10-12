package daw

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	"github.com/steveclarke/ucmix/internal/proto"
)

// Command is one Studio Pro command as Studio Pro advertises it.
type Command struct {
	Category string `json:"category"`
	Name     string `json:"name"`
	Display  string `json:"displayName,omitempty"`
}

// Change is one parameter update applied to the model.
type Change struct {
	Seq  uint64
	Path string
}

const changeLogSize = 8192

// Limits on what Studio Pro can make ucdaw hold. The real model has about
// 156,000 paths and a 180 KB command catalog; these leave wide headroom while
// keeping a misbehaving peer from exhausting memory.
const (
	// MaxInflated caps one decompressed message.
	MaxInflated = 32 << 20
	// MaxPaths caps distinct parameter paths in the model.
	MaxPaths = 400_000
)

// ErrTooLarge reports a message or model over its limit.
var ErrTooLarge = errors.New("daw: Studio Pro sent more data than ucdaw accepts")

// Model is the live song state Studio Pro reports: every parameter value it
// has sent (wire values, 0..1 for ranged parameters), its strings, and its
// command catalog. It is safe for concurrent use.
type Model struct {
	mu       sync.RWMutex
	nums     map[string]float64
	strs     map[string]string
	commands []Command
	dialog   *Dialog
	events   map[string][]Event
	seq      uint64
	log      []Change
	changed  chan struct{}
}

// NewModel returns an empty model.
func NewModel() *Model {
	return &Model{
		nums:    map[string]float64{},
		events:  map[string][]Event{},
		strs:    map[string]string{},
		changed: make(chan struct{}),
	}
}

// Changed returns a channel closed at the next change to the model.
func (m *Model) Changed() <-chan struct{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.changed
}

// Seq is the sequence number of the latest change.
func (m *Model) Seq() uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.seq
}

// ChangesSince returns the distinct paths changed after seq, in order.
func (m *Model) ChangesSince(seq uint64) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	seen := map[string]bool{}
	var out []string
	for _, c := range m.log {
		if c.Seq > seq && !seen[c.Path] {
			seen[c.Path] = true
			out = append(out, c.Path)
		}
	}
	return out
}

// Num returns a numeric (wire) value.
func (m *Model) Num(path string) (float64, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.nums[path]
	return v, ok
}

// Str returns a string value.
func (m *Model) Str(path string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.strs[path]
	return v, ok
}

// Dialog is a modal dialog Studio Pro has open. While one is open, Studio Pro
// does not run other commands.
type Dialog struct {
	Title   string   `json:"title"`
	Text    string   `json:"text,omitempty"`
	Buttons []string `json:"buttons,omitempty"`
}

// Dialog returns the dialog Studio Pro reports open, if any.
func (m *Model) Dialog() (Dialog, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.dialog == nil {
		return Dialog{}, false
	}
	return *m.dialog, true
}

// Commands returns Studio Pro's advertised command catalog.
func (m *Model) Commands() []Command {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]Command(nil), m.commands...)
}

// Paths returns every path with a value, sorted.
func (m *Model) Paths(prefix string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []string
	for k := range m.nums {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	for k := range m.strs {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// mutate runs fn under the write lock and wakes waiters if fn changed paths.
func (m *Model) mutate(fn func(record func(string))) {
	m.mu.Lock()
	changed := false
	fn(func(path string) {
		changed = true
		m.seq++
		m.log = append(m.log, Change{Seq: m.seq, Path: path})
		if len(m.log) > changeLogSize {
			m.log = append([]Change(nil), m.log[len(m.log)-changeLogSize/2:]...)
		}
	})
	if changed {
		close(m.changed)
		m.changed = make(chan struct{})
	}
	m.mu.Unlock()
}

// full reports whether path would be a new key beyond MaxPaths.
func (m *Model) full(path string) bool {
	if len(m.nums)+len(m.strs) < MaxPaths {
		return false
	}
	_, num := m.nums[path]
	_, str := m.strs[path]
	return !num && !str
}

func (m *Model) setNum(path string, v float64, record func(string)) {
	if old, ok := m.nums[path]; (ok && old == v) || m.full(path) {
		return
	}
	m.nums[path] = v
	record(path)
}

func (m *Model) setStr(path, v string, record func(string)) {
	if old, ok := m.strs[path]; (ok && old == v) || m.full(path) {
		return
	}
	m.strs[path] = v
	record(path)
}

// ApplyFrame folds one inbound frame into the model. It reports whether the
// frame was a model update.
func (m *Model) ApplyFrame(f proto.Frame) error {
	switch f.Code {
	case proto.CodePV:
		key, v, err := proto.UnmarshalPV(f.Payload)
		if err != nil {
			return err
		}
		m.mutate(func(r func(string)) { m.setNum(key, float64(v), r) })
	case proto.CodePS:
		key, v, err := proto.UnmarshalPS(f.Payload)
		if err != nil {
			return err
		}
		m.mutate(func(r func(string)) { m.setStr(key, v, r) })
	case proto.CodePC:
		key, raw, err := proto.UnmarshalPC(f.Payload)
		if err != nil {
			return err
		}
		if len(raw) >= 4 {
			v := float64(binary.LittleEndian.Uint32(raw[:4]))
			m.mutate(func(r func(string)) { m.setNum(key, v, r) })
		}
	case codeBO:
		if !bytes.HasPrefix(f.Payload, []byte("EvDL")) {
			return nil
		}
		list, err := ParseEventList(f.Payload)
		if err != nil {
			return err
		}
		if len(m.events) >= 16 && m.events[list.Kind] == nil {
			return nil // only a handful of kinds exist
		}
		m.mutate(func(r func(string)) {
			m.events[list.Kind] = list.Events
			r("@events/" + list.Kind)
		})
	case codeZM:
		body, err := DecodeZM(f.Payload)
		if err != nil {
			return err
		}
		return m.ApplyJSON(body)
	}
	return nil
}

var (
	codeZM = [2]byte{'Z', 'M'}
	codeBO = [2]byte{'B', 'O'}
)

// Events returns one event list: "Mark" (markers) or "Arng" (arranger
// sections).
func (m *Model) Events(kind string) []Event {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]Event(nil), m.events[kind]...)
}

// DecodeZM inflates a ZM payload: a 4-byte little-endian inflated length
// followed by a zlib stream of JSON.
func DecodeZM(payload []byte) ([]byte, error) {
	if len(payload) < 6 {
		return nil, fmt.Errorf("daw: ZM payload too short (%d bytes)", len(payload))
	}
	return inflate(payload[4:])
}

func inflate(data []byte) ([]byte, error) {
	r, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("daw: inflating: %w", err)
	}
	defer func() { _ = r.Close() }()
	out, err := io.ReadAll(io.LimitReader(r, MaxInflated+1))
	if err != nil {
		return nil, fmt.Errorf("daw: inflating: %w", err)
	}
	if len(out) > MaxInflated {
		return nil, fmt.Errorf("%w: message inflates past %d MB", ErrTooLarge, MaxInflated>>20)
	}
	return out, nil
}

type treeNode struct {
	Values   map[string]json.RawMessage `json:"values"`
	Children map[string]treeNode        `json:"children"`
}

// ApplyJSON folds one decoded JSON message (Synchronize, SynchronizePart,
// AdviseCommands) into the model. Other ids are ignored.
func (m *Model) ApplyJSON(body []byte) error {
	var head struct {
		ID           string `json:"id"`
		Part         string `json:"part"`
		DialogTitle  string `json:"dialogTitle"`
		DialogText   string `json:"dialogText"`
		FirstButton  string `json:"firstButton"`
		SecondButton string `json:"secondButton"`
		Categories   []struct {
			Commands []Command `json:"commands"`
		} `json:"categories"`
	}
	if err := json.Unmarshal(body, &head); err != nil {
		return fmt.Errorf("daw: decoding message: %w", err)
	}
	switch head.ID {
	case "BeginDialog":
		// Buttons stay in their slots, an unlabeled one as "", because
		// ConfirmDialog answers by slot. Only trailing empty slots go.
		d := &Dialog{Title: head.DialogTitle, Text: head.DialogText}
		d.Buttons = []string{head.FirstButton, head.SecondButton}
		for len(d.Buttons) > 0 && d.Buttons[len(d.Buttons)-1] == "" {
			d.Buttons = d.Buttons[:len(d.Buttons)-1]
		}
		if len(d.Buttons) == 0 {
			d.Buttons = nil
		}
		m.mutate(func(r func(string)) {
			m.dialog = d
			r("@dialog")
		})
	case "EndDialog":
		m.mutate(func(r func(string)) {
			m.dialog = nil
			r("@dialog")
		})
	case "AdviseCommands":
		var commands []Command
		for _, c := range head.Categories {
			commands = append(commands, c.Commands...)
		}
		m.mutate(func(r func(string)) {
			m.commands = commands
			r("@commands")
		})
	case "Synchronize", "SynchronizePart":
		var tree treeNode
		if err := json.Unmarshal(body, &tree); err != nil {
			return fmt.Errorf("daw: decoding %s: %w", head.ID, err)
		}
		prefix := strings.Trim(head.Part, "/")
		if prefix != "" {
			prefix += "/"
		}
		m.mutate(func(r func(string)) { m.applyTree(prefix, tree, r) })
	}
	return nil
}

func (m *Model) applyTree(prefix string, n treeNode, record func(string)) {
	for key, raw := range n.Values {
		if strings.HasPrefix(key, "_") {
			continue
		}
		var num float64
		if err := json.Unmarshal(raw, &num); err == nil {
			m.setNum(prefix+key, num, record)
			continue
		}
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			m.setStr(prefix+key, s, record)
		}
	}
	for name, child := range n.Children {
		m.applyTree(prefix+name+"/", child, record)
	}
}
