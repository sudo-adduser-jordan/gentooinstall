package installer

import (
	"fmt"
	"os"
	"path/filepath"
)

// path resolves a static absolute path against the context root. It is only
// meant for hardcoded locations like /etc/portage/make.conf, /boot, or the
// package constants in context.go; explicit parameter paths (mountpoints,
// chroot dirs, tarball locations) are passed to commands as-is and never run
// through here.
func (ctx *Context) path(path string) string {
	if ctx.Root == "" {
		return path
	}
	return filepath.Join(ctx.Root, path)
}

func (ctx *Context) writeFile(path string, data []byte, mode os.FileMode) error {
	return os.WriteFile(ctx.path(path), data, mode)
}

func (ctx *Context) appendFile(path, line string) error {
	file, err := os.OpenFile(ctx.path(path), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("could not write to %s: %w", path, err)
	}
	defer file.Close()
	if _, err := file.WriteString(line + "\n"); err != nil {
		return err
	}
	return nil
}

// touchFile creates path if missing without truncating existing content
// (byte-compatible with the bash `touch` of configure_portage).
func (ctx *Context) touchFile(path string) error {
	file, err := os.OpenFile(ctx.path(path), os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	return file.Close()
}

func (ctx *Context) stat(path string) (os.FileInfo, error) {
	if ctx.Stat != nil {
		return ctx.Stat(path)
	}
	return os.Stat(path)
}

// hostHasEFI reports whether the running firmware is UEFI-capable: the
// kernel exposes /sys/firmware/efi only on a UEFI boot.
func (ctx *Context) hostHasEFI() bool {
	_, err := ctx.stat("/sys/firmware/efi")
	return err == nil
}

// HostHasEFI is the exported form of hostHasEFI for preflight logging in
// main.go: it honors the Stat stub so tests stay host-independent.
func (ctx *Context) HostHasEFI() bool { return ctx.hostHasEFI() }

// HostBootMode renders the running firmware as "UEFI" or "BIOS/legacy" for
// install summaries and logs.
func (ctx *Context) HostBootMode() string {
	if ctx.hostHasEFI() {
		return "UEFI"
	}
	return "BIOS/legacy (no /sys/firmware/efi)"
}

func (ctx *Context) mkdirAll(path string, mode os.FileMode) error {
	return os.MkdirAll(ctx.path(path), mode)
}

func (ctx *Context) chmod(path string, mode os.FileMode) error {
	return os.Chmod(ctx.path(path), mode)
}

func (ctx *Context) readFile(path string) ([]byte, error) {
	return os.ReadFile(ctx.path(path))
}

func (ctx *Context) readDir(path string) ([]os.DirEntry, error) {
	return os.ReadDir(ctx.path(path))
}

func (ctx *Context) readlink(path string) (string, error) {
	return os.Readlink(ctx.path(path))
}

func (ctx *Context) removeAll(path string) error {
	return os.RemoveAll(ctx.path(path))
}

func (ctx *Context) evalSymlinks(path string) (string, error) {
	if ctx.EvalSymlinks != nil {
		return ctx.EvalSymlinks(path)
	}
	return filepath.EvalSymlinks(path)
}
