package installer

import (
	"fmt"
	"os"
	"path/filepath"

	"gentooinstall/internal/config"
	"gentooinstall/internal/disklayout"
)

// Standard locations (port of scripts/config.sh constants).
const (
	TmpDir              = "/tmp/gentoo-install"
	RootMountpoint      = TmpDir + "/root"
	RepoBind            = TmpDir + "/bind"
	UUIDStorageDir      = TmpDir + "/uuids"
	LuksHeaderBackupDir = TmpDir + "/luks-headers"
)

// Context carries all state through the install phases.
type Context struct {
	R *Runner

	Cfg    *config.Config
	Layout *disklayout.Layout

	Resolver *disklayout.Resolver

	// SourceConfig is the absolute path of the config on the live system.
	SourceConfig string

	EncryptionKey string
	InChroot      bool

	Stage3File string
	NProc      int
}

// EnsureTmpDirs creates the standard working directories.
func EnsureTmpDirs() error {
	return os.MkdirAll(TmpDir, 0o755)
}

// MustExist errors when path is missing.
func MustExist(path, what string) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("%s not found: %s: %w", what, path, err)
	}
	return nil
}

// BinInBind is where the gentooinstall binary lives inside the chroot.
func BinInBind() string { return filepath.Join(RepoBind, "gentooinstall-self") }

// ConfigInBind is where the config file lives inside the chroot.
func ConfigInBind() string { return filepath.Join(RepoBind, "config.toml") }

// StageBind copies the gentooinstall binary and the config file into the bind
// directory so both are reachable from within the chroot. This replaces
// the bash repo-dir bind mount.
func StageBind(c *Context) error {
	if err := os.MkdirAll(RepoBind, 0o755); err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("could not locate own executable: %w", err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		return fmt.Errorf("could not read own executable: %w", err)
	}
	if err := os.WriteFile(BinInBind(), data, 0o755); err != nil {
		return err
	}

	cfgData, err := os.ReadFile(c.SourceConfig)
	if err != nil {
		return fmt.Errorf("could not read config: %w", err)
	}
	return os.WriteFile(ConfigInBind(), cfgData, 0o644)
}

// IsEFI resolves the boot mode from layout roles.
func (c *Context) IsEFI() bool { return c.Layout.EFIID != "" }
