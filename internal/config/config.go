// Package config resolves the mixer host address the CLI connects to and
// manages saved connection profiles. Resolution order is: the --host flag, then
// a named --profile, then the UCMIX_HOST environment variable, then the current
// profile in the config file, then a legacy top-level host: key, and finally an
// error. The config file is ~/.config/ucmix/config.yml with an optional
// config.local.yml deep-merged over it. Profile mutations are written back to
// config.yml, preserving its comments and unknown keys. A resolved host with no
// explicit port gets the default UCNET port.
package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/steveclarke/ucmix/internal/errs"
	"gopkg.in/yaml.v3"
)

// DefaultPort is the UCNET TCP port StudioLive Series III mixers listen on. It
// is appended to a resolved host that has no explicit port.
const DefaultPort = "53000"

// EnvHost is the environment variable checked after --host and --profile.
const EnvHost = "UCMIX_HOST"

// File is the config resolver's view of the filesystem and environment. The
// zero value uses the real OS; tests set the fields to redirect lookups.
type File struct {
	// Env, when non-nil, is consulted instead of os.Getenv.
	Env func(string) string
	// ConfigDir, when non-empty, overrides the ~/.config base directory.
	ConfigDir string
}

// Profile is one saved mixer target. Only the host is stored today; the struct
// leaves room for per-profile settings later without a breaking change.
type Profile struct {
	Host string `yaml:"host"`
}

// Resolve returns the mixer address using the documented precedence:
// flagHost > flagProfile > UCMIX_HOST > current profile > legacy host: > error.
// The returned address always includes a port (DefaultPort is appended when
// none is present). Passing both flagHost and flagProfile is an error.
func (f File) Resolve(flagHost, flagProfile string) (string, error) {
	flagHost = strings.TrimSpace(flagHost)
	flagProfile = strings.TrimSpace(flagProfile)
	if flagHost != "" && flagProfile != "" {
		return "", errs.CLIError{
			Message: "--host and --profile are mutually exclusive",
			Hint:    "pass a literal address with --host, or a saved profile with --profile, not both",
		}
	}
	if flagHost != "" {
		return withPort(flagHost), nil
	}

	cfg, err := f.loadConfig()
	if err != nil {
		return "", err
	}

	if flagProfile != "" {
		p, ok := cfg.Profiles[flagProfile]
		if !ok {
			return "", errs.CLIError{
				Message: fmt.Sprintf("no profile named %q", flagProfile),
				Hint:    "list profiles with `ucmix profile ls`",
			}
		}
		if h := strings.TrimSpace(p.Host); h != "" {
			return withPort(h), nil
		}
		return "", errs.CLIError{Message: fmt.Sprintf("profile %q has no host", flagProfile)}
	}

	if h := strings.TrimSpace(f.getenv(EnvHost)); h != "" {
		return withPort(h), nil
	}

	if cur := strings.TrimSpace(cfg.Current); cur != "" {
		p, ok := cfg.Profiles[cur]
		if !ok {
			return "", errs.CLIError{
				Message: fmt.Sprintf("current profile %q does not exist", cur),
				Hint:    "set a valid current profile with `ucmix profile use <name>`",
			}
		}
		if h := strings.TrimSpace(p.Host); h != "" {
			return withPort(h), nil
		}
	}

	if h := strings.TrimSpace(cfg.Host); h != "" {
		return withPort(h), nil
	}

	return "", errs.CLIError{
		Message: "no mixer host configured",
		Hint:    "run `ucmix setup` to find a mixer, add one with `ucmix profile add`, or set --host / UCMIX_HOST",
	}
}

// Resolve resolves against the real OS environment and config directory.
func Resolve(flagHost, flagProfile string) (string, error) {
	return File{}.Resolve(flagHost, flagProfile)
}

func (f File) getenv(key string) string {
	if f.Env != nil {
		return f.Env(key)
	}
	return os.Getenv(key)
}

// Config is the merged view of the config files: the legacy top-level host, the
// current-profile pointer, and the named profiles.
type Config struct {
	Host     string             `yaml:"host,omitempty"`
	Current  string             `yaml:"current,omitempty"`
	Profiles map[string]Profile `yaml:"profiles,omitempty"`
}

// Load returns the merged config (config.yml overlaid with config.local.yml).
func (f File) Load() (Config, error) { return f.loadConfig() }

// loadConfig reads config.yml and, if present, deep-merges config.local.yml over
// it. A missing base file is not an error (it yields an empty Config); a
// present-but-unreadable or malformed file is.
func (f File) loadConfig() (Config, error) {
	dir := f.configDir()
	base, err := readYAML(filepath.Join(dir, "config.yml"))
	if err != nil {
		return Config{}, err
	}
	local, err := readYAML(filepath.Join(dir, "config.local.yml"))
	if err != nil {
		return Config{}, err
	}
	merged := deepMerge(base, local)

	var out Config
	// Re-encode the merged map and decode into the typed struct so field
	// mapping stays in one place (the yaml tags above).
	blob, err := yaml.Marshal(merged)
	if err != nil {
		return Config{}, fmt.Errorf("config: re-encoding merged config: %w", err)
	}
	if err := yaml.Unmarshal(blob, &out); err != nil {
		return Config{}, fmt.Errorf("config: decoding merged config: %w", err)
	}
	return out, nil
}

// ProfileNames returns the saved profile names in sorted order.
func (c Config) ProfileNames() []string {
	names := make([]string, 0, len(c.Profiles))
	for n := range c.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Path returns the path to the primary config file (config.yml), whether or not
// it exists yet. Profile mutations and `config path`/`config edit` use it.
func (f File) Path() string {
	return filepath.Join(f.configDir(), "config.yml")
}

// configDir returns the directory holding the config files. It honors
// XDG_CONFIG_HOME and otherwise uses ~/.config/ucmix on every platform, matching
// the path every hint and doc names (os.UserConfigDir would point at
// ~/Library/Application Support on macOS, which the docs do not mention).
func (f File) configDir() string {
	if f.ConfigDir != "" {
		return f.ConfigDir
	}
	if xdg := strings.TrimSpace(f.getenv("XDG_CONFIG_HOME")); xdg != "" {
		return filepath.Join(xdg, "ucmix")
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
