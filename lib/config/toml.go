package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	toml "github.com/BurntSushi/toml"
)

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

func (cfg *Config) String() string {
	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	enc.Indent = "    "
	if err := enc.Encode(cfg); err != nil {
		return "# error encoding config"
	}
	return buf.String()
}

func (cfg *Config) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config directory %s: %w", filepath.Dir(path), err)
	}
	return os.WriteFile(path, []byte(cfg.String()), 0o644)
}

const (
	DefaultConfigName = "default.toml"
	CustomConfigName  = "custom.toml"
)

func ResolveSavePath(path string) string {
	dir := filepath.Dir(path)
	if filepath.Base(path) == DefaultConfigName || filepath.Base(dir) == "builds" {
		return filepath.Join(dir, CustomConfigName)
	}
	return path
}
