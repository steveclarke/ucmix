// Package config resolves the mixer host address the CLI connects to. The
// resolution order is: the --host flag, then the UCMIX_HOST environment
// variable, then a config file (~/.config/ucmix/config.yml with an optional
// config.local.yml deep-merged over it), and finally an error. It also fills in
// the default UCNET port when a resolved host carries none.
package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/steveclarke/ucmix/internal/errs"
	"gopkg.in/yaml.v3"
)

// DefaultPort is the UCNET TCP port StudioLive Series III mixers listen on. It
// is appended to a resolved host that has no explicit port.
const DefaultPort = "53000"

// EnvHost is the environment variable checked after the --host flag.
const EnvHost = "UCMIX_HOST"

// File is the config resolver's view of the filesystem and environment. The
// zero value uses the real OS; tests set the fields to redirect lookups.
type File struct {
	// Env, when non-nil, is consulted instead of os.Getenv.
	Env func(string) string
	// ConfigDir, when non-empty, overrides the ~/.config base directory.
	ConfigDir string
}

// ResolveHost returns the mixer address using the documented precedence:
// flagHost > UCMIX_HOST > config file host > error. The returned address always
// includes a port (DefaultPort is appended when none is present).
func (f File) ResolveHost(flagHost string) (string, error) {
	if h := strings.TrimSpace(flagHost); h != "" {
		return withPort(h), nil
	}
	if h := strings.TrimSpace(f.getenv(EnvHost)); h != "" {
		return withPort(h), nil
	}
	cfg, err := f.loadConfig()
	if err != nil {
		return "", err
	}
	if h := strings.TrimSpace(cfg.Host); h != "" {
		return withPort(h), nil
	}
	return "", errs.CLIError{
		Message: "no mixer host configured",
		Hint:    "set --host, the UCMIX_HOST env var, or a host: in ~/.config/ucmix/config.yml",
	}
}

// ResolveHost resolves against the real OS environment and config directory.
func ResolveHost(flagHost string) (string, error) {
	return File{}.ResolveHost(flagHost)
}

func (f File) getenv(key string) string {
	if f.Env != nil {
		return f.Env(key)
	}
	return os.Getenv(key)
}

// settings is the subset of the config file the CLI reads.
type settings struct {
	Host string `yaml:"host"`
}

// loadConfig reads config.yml and, if present, deep-merges config.local.yml over
// it. A missing base file is not an error (it just yields empty settings); a
// present-but-unreadable or malformed file is.
func (f File) loadConfig() (settings, error) {
	dir := f.configDir()
	base, err := readYAML(filepath.Join(dir, "config.yml"))
	if err != nil {
		return settings{}, err
	}
	local, err := readYAML(filepath.Join(dir, "config.local.yml"))
	if err != nil {
		return settings{}, err
	}
	merged := deepMerge(base, local)

	var out settings
	// Re-encode the merged map and decode into the typed struct so field
	// mapping stays in one place (the yaml tags above).
	blob, err := yaml.Marshal(merged)
	if err != nil {
		return settings{}, fmt.Errorf("config: re-encoding merged config: %w", err)
	}
	if err := yaml.Unmarshal(blob, &out); err != nil {
		return settings{}, fmt.Errorf("config: decoding merged config: %w", err)
	}
	return out, nil
}

// configDir returns the directory holding the config files.
func (f File) configDir() string {
	if f.ConfigDir != "" {
		return f.ConfigDir
	}
	if base, err := os.UserConfigDir(); err == nil {
		return filepath.Join(base, "ucmix")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "ucmix")
}

// readYAML decodes a YAML file into a generic map. A missing file yields a nil
// map and no error; any other read/parse failure is returned.
func readYAML(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("config: reading %s: %w", path, err)
	}
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("config: parsing %s: %w", path, err)
	}
	return m, nil
}

// deepMerge overlays override onto base and returns the result. Nested maps are
// merged recursively; any non-map value in override replaces the base value.
// Neither input is mutated.
func deepMerge(base, override map[string]any) map[string]any {
	out := make(map[string]any, len(base))
	for k, v := range base {
		out[k] = v
	}
	for k, ov := range override {
		if bv, ok := out[k]; ok {
			if bm, okB := bv.(map[string]any); okB {
				if om, okO := ov.(map[string]any); okO {
					out[k] = deepMerge(bm, om)
					continue
				}
			}
		}
		out[k] = ov
	}
	return out
}

// withPort appends DefaultPort to host when it carries no explicit port. Hosts
// that already include a port (and bracketed IPv6 literals) pass through.
func withPort(host string) string {
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	return net.JoinHostPort(host, DefaultPort)
}
