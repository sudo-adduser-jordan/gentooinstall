package installer

import (
	"fmt"
	"os"
	"path/filepath"

	"gentooinstall/lib/config"
	"gentooinstall/lib/disklayout"
)

const (
	TmpDir              = "/tmp/gentoo-install"
	RootMountpoint      = TmpDir + "/root"
	RepoBind            = TmpDir + "/bind"
	UUIDStorageDir      = TmpDir + "/uuids"
	LuksHeaderBackupDir = TmpDir + "/luks-headers"
	Stage3ScratchDir    = RootMountpoint + "/.gentoo-stage3"
)

type Context struct {
	Runner *Runner

	Cfg    *config.Config
	Layout *disklayout.Layout

	Resolver *disklayout.Resolver

	SourceConfig string

	EncryptionKey string
	InChroot      bool

	BlkidUUID func(dev string) (string, error)

	EvalSymlinks func(p string) (string, error)

	IsMountpoint func(path string) bool

	TargetResources func(device string) (mounts, swaps []string)

	Stat func(path string) (os.FileInfo, error)

	Filesystems func(fs string) bool

	Root string

	Stage3File string
	NProc      int
}

func EnsureTmpDirs() error {
	return os.MkdirAll(TmpDir, 0o755)
}

func MustExist(path, what string) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("%s not found: %s: %w", what, path, err)
	}
	return nil
}

func BinInBind() string { return filepath.Join(RepoBind, "gentooinstall-self") }

func ConfigInBind() string { return filepath.Join(RepoBind, "config.toml") }

func StageBind(ctx *Context) error {
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

	cfgData, err := os.ReadFile(ctx.SourceConfig)
	if err != nil {
		return fmt.Errorf("could not read config: %w", err)
	}
	return os.WriteFile(ConfigInBind(), cfgData, 0o644)
}

func (ctx *Context) IsEFI() bool { return ctx.Layout.EFIID != "" }
