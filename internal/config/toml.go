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
	c := &Config{}
	if err := toml.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return c, nil
}

// LoadOrDefault loads path; missing files yield the default configuration
// with Timezone/Keymap left for the caller to fill from system detection.
func LoadOrDefault(path string, hasEFI bool) (*Config, bool, error) {
	c := Default(hasEFI)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return c, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if err := toml.Unmarshal(data, c); err != nil {
		return nil, false, fmt.Errorf("parse %s: %w", path, err)
	}
	return c, true, nil
}

// String returns the config serialised as TOML.
func (c *Config) String() string {
	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	enc.Indent = "    "
	if err := enc.Encode(c); err != nil {
		return "# error encoding config"
	}
	return buf.String()
}

// Save writes the config as TOML.
func (c *Config) Save(path string) error {
	return os.WriteFile(path, []byte(c.String()), 0o644)
}

// DefaultConfigName and CustomConfigName are the shipped template and the
// user-owned build file inside builds/.
const (
	DefaultConfigName = "default.toml"
	CustomConfigName  = "custom.toml"
)

// ResolveSavePath returns the path a save should be written to. Saving onto
// the shipped default template is redirected to the sibling custom.toml so
// the template is never overwritten; any other path is returned unchanged.
func ResolveSavePath(path string) string {
	if filepath.Base(path) != DefaultConfigName {
		return path
	}
	return filepath.Join(filepath.Dir(path), CustomConfigName)
}
