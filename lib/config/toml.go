package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	toml "github.com/BurntSushi/toml"
)

// Load reads and decodes a gentooinstall build configuration file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := &Config{}
	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

// LoadOrDefault loads path; missing files yield the default configuration
// with Timezone/Keymap left for the caller to fill from system detection.
func LoadOrDefault(path string, hasEFI bool) (*Config, bool, error) {
	cfg := Default(hasEFI)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cfg, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, false, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, true, nil
}

// String returns the config serialised as TOML.
func (cfg *Config) String() string {
	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	enc.Indent = "    "
	if err := enc.Encode(cfg); err != nil {
		return "# error encoding config"
	}
	return buf.String()
}

// Save writes the config as TOML, creating missing parent directories (e.g.
// when saving to /builds/custom.toml on the live ISO whose initramfs ships no
// builds/ dir). A directory that cannot be created is the real failure and is
// reported as such, naming the parent directory instead of letting WriteFile
// surface a cryptic error for the file itself.
func (cfg *Config) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config directory %s: %w", filepath.Dir(path), err)
	}
	return os.WriteFile(path, []byte(cfg.String()), 0o644)
}

// DefaultConfigName and CustomConfigName are the shipped template and the
// user-owned build file inside builds/.
const (
	DefaultConfigName = "default.toml"
	CustomConfigName  = "custom.toml"
)

// ResolveSavePath returns the path a save should be written to. Saving onto a
// shipped template is redirected to the sibling custom.toml so the read-only
// templates are never overwritten: this applies to default.toml anywhere, and
// to every template living inside a builds/ directory (openrc.toml, musl.toml,
// desktop-systemd.toml, ...). Any other path (e.g. a user file already named
// custom.toml) is returned unchanged.
func ResolveSavePath(path string) string {
	dir := filepath.Dir(path)
	if filepath.Base(path) == DefaultConfigName || filepath.Base(dir) == "builds" {
		return filepath.Join(dir, CustomConfigName)
	}
	return path
}
