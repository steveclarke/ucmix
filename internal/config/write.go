package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/steveclarke/ucmix/internal/errs"
	"gopkg.in/yaml.v3"
)

// Writer mutates config.yml in place while preserving its comments, key order,
// and any keys the tool does not manage. It edits the file's own YAML node tree
// rather than round-tripping through a struct (which would drop comments).
// Mutations target config.yml specifically — not the config.local.yml overlay —
// so `ucmix profile add` writes to the file a user would expect to edit.
type Writer struct {
	path string
}

// NewWriter returns a Writer for the primary config file of f.
func (f File) NewWriter() Writer { return Writer{path: f.Path()} }

// AddProfile creates or replaces the named profile's host. A missing config file
// is created; a `profiles:` map is added if absent.
func (w Writer) AddProfile(name, host string) error {
	return w.edit(func(root *yaml.Node) error {
		profiles := ensureMap(root, "profiles")
		body := newMappingNode()
		setScalar(body, "host", host)
		setMapValue(profiles, name, body)
		return nil
	})
}

// RemoveProfile deletes the named profile. It errors if the profile is not
// present in config.yml. If the removed profile was current, `current:` is
// cleared too.
func (w Writer) RemoveProfile(name string) error {
	return w.edit(func(root *yaml.Node) error {
		profiles := findValue(root, "profiles")
		if profiles == nil || !deleteMapKey(profiles, name) {
			return errs.CLIError{
				Message: fmt.Sprintf("no profile named %q in %s", name, w.path),
				Hint:    "list profiles with `ucmix profile ls`",
			}
		}
		if cur := findValue(root, "current"); cur != nil && cur.Value == name {
			deleteMapKey(root, "current")
		}
		return nil
	})
}

// RenameProfile changes a profile's key from old to newName, preserving its
// value (and any comments on it). It errors if old is absent or newName exists.
// A current pointer at old is updated to newName.
func (w Writer) RenameProfile(old, newName string) error {
	return w.edit(func(root *yaml.Node) error {
		profiles := findValue(root, "profiles")
		if profiles == nil {
			return errs.CLIError{Message: fmt.Sprintf("no profile named %q in %s", old, w.path)}
		}
		keyNode, _ := findPair(profiles, old)
		if keyNode == nil {
			return errs.CLIError{Message: fmt.Sprintf("no profile named %q in %s", old, w.path)}
		}
		if k, _ := findPair(profiles, newName); k != nil {
			return errs.CLIError{Message: fmt.Sprintf("a profile named %q already exists", newName)}
		}
		keyNode.Value = newName
		if cur := findValue(root, "current"); cur != nil && cur.Value == old {
			cur.Value = newName
		}
		return nil
	})
}

// SetCurrent points current: at name.
func (w Writer) SetCurrent(name string) error {
	return w.edit(func(root *yaml.Node) error {
		setScalar(root, "current", name)
		return nil
	})
}

// edit loads config.yml's node tree (synthesizing an empty document when the
// file is absent), applies fn to the root mapping node, and writes the result
// back atomically with a trailing newline and 2-space indent.
func (w Writer) edit(fn func(root *yaml.Node) error) error {
	root, err := w.loadRoot()
	if err != nil {
		return err
	}
	if err := fn(root); err != nil {
		return err
	}
	return w.writeRoot(root)
}

// loadRoot returns the top-level mapping node of config.yml. A missing or empty
// file yields a fresh mapping node.
func (w Writer) loadRoot() (*yaml.Node, error) {
	data, err := os.ReadFile(w.path)
	if err != nil {
		if os.IsNotExist(err) {
			return newMappingNode(), nil
		}
		return nil, fmt.Errorf("config: reading %s: %w", w.path, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("config: parsing %s: %w", w.path, err)
	}
	if len(doc.Content) == 0 {
		return newMappingNode(), nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, errs.CLIError{Message: fmt.Sprintf("config: %s is not a YAML mapping", w.path)}
	}
	return root, nil
}

// writeRoot encodes root as a YAML document and writes it to config.yml,
// creating the directory if needed and replacing the file atomically.
func (w Writer) writeRoot(root *yaml.Node) error {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		return fmt.Errorf("config: encoding %s: %w", w.path, err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("config: encoding %s: %w", w.path, err)
	}
	if err := os.MkdirAll(filepath.Dir(w.path), 0o755); err != nil {
		return fmt.Errorf("config: creating config dir: %w", err)
	}
	tmp := w.path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("config: writing %s: %w", w.path, err)
	}
	if err := os.Rename(tmp, w.path); err != nil {
		return fmt.Errorf("config: replacing %s: %w", w.path, err)
	}
	return nil
}

// --- yaml.Node helpers: a mapping node stores [key0, val0, key1, val1, ...]. ---

func newMappingNode() *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
}

func scalarNode(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

// findPair returns the key and value nodes for key in a mapping node, or nils.
func findPair(m *yaml.Node, key string) (keyNode, valNode *yaml.Node) {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil, nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i], m.Content[i+1]
		}
	}
	return nil, nil
}

// findValue returns the value node for key in a mapping node, or nil.
func findValue(m *yaml.Node, key string) *yaml.Node {
	_, v := findPair(m, key)
	return v
}

// setScalar sets key to a string scalar value, replacing an existing value or
// appending a new key/value pair.
func setScalar(m *yaml.Node, key, value string) {
	setMapValue(m, key, scalarNode(value))
}

// setMapValue sets key to valNode, replacing an existing value or appending a
// new key/value pair to the mapping.
func setMapValue(m *yaml.Node, key string, valNode *yaml.Node) {
	if k, _ := findPair(m, key); k != nil {
		for i := 0; i+1 < len(m.Content); i += 2 {
			if m.Content[i] == k {
				m.Content[i+1] = valNode
				return
			}
		}
	}
	m.Content = append(m.Content, scalarNode(key), valNode)
}

// ensureMap returns the mapping node at key, creating an empty one if absent.
func ensureMap(m *yaml.Node, key string) *yaml.Node {
	if v := findValue(m, key); v != nil && v.Kind == yaml.MappingNode {
		return v
	}
	child := newMappingNode()
	setMapValue(m, key, child)
	return child
}

// deleteMapKey removes the key/value pair for key from a mapping node. It
// returns true if a pair was removed.
func deleteMapKey(m *yaml.Node, key string) bool {
	if m == nil || m.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return true
		}
	}
	return false
}
