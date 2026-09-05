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
	// Stage3ScratchDir is where the verified stage3 tarball is staged, inside
	// the mounted target root filesystem. TmpDir sits on the live system's
	// RAM-backed initramfs, which cannot hold a ~400MB tarball on low-memory
	// hosts; the target disk can.
	Stage3ScratchDir = RootMountpoint + "/.gentoo-stage3"
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

	// BlkidUUID, when non-nil, resolves a device path to its filesystem UUID
	// (used by KernelCmdline, GenerateFstab and the kernel installers).
	// Production code leaves it nil so BlkidUUIDForID falls back to
	// disklayout.GetBlkidField; tests inject a stub to avoid invoking blkid.
	BlkidUUID func(dev string) (string, error)

	// EvalSymlinks canonicalizes a block device path. Production leaves it
	// nil so filepath.EvalSymlinks is used; tests inject a stub because the
	// scratch devices they operate on are not real symlinks in /dev.
	EvalSymlinks func(p string) (string, error)

	// IsMountpoint reports whether a path is currently mounted. Production
	// leaves it nil so /proc/mounts is consulted; tests inject a stub to
	// keep the recorded command sequence independent of the host.
	IsMountpoint func(path string) bool

	// Root, when non-empty, is prefixed onto every static absolute path the
	// installer writes or reads, so tests can run against a scratch
	// directory without touching the real filesystem. Production builds
	// leave it empty and keep the current behavior. Paths passed in as
	// parameters (mountpoints, chroot dirs, tar paths) are never prefixed.
	Root string

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
