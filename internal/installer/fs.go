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
func (c *Context) path(p string) string {
	if c.Root == "" {
		return p
	}
	return filepath.Join(c.Root, p)
}

func (c *Context) writeFile(p string, data []byte, mode os.FileMode) error {
	return os.WriteFile(c.path(p), data, mode)
}

func (c *Context) appendFile(p, line string) error {
	f, err := os.OpenFile(c.path(p), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("could not write to %s: %w", p, err)
	}
	defer f.Close()
	if _, err := f.WriteString(line + "\n"); err != nil {
		return err
	}
	return nil
}

// touchFile creates path if missing without truncating existing content
// (byte-compatible with the bash `touch` of configure_portage).
func (c *Context) touchFile(p string) error {
	f, err := os.OpenFile(c.path(p), os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}

func (c *Context) mkdirAll(p string, mode os.FileMode) error {
	return os.MkdirAll(c.path(p), mode)
}

func (c *Context) chmod(p string, mode os.FileMode) error {
	return os.Chmod(c.path(p), mode)
}

func (c *Context) readFile(p string) ([]byte, error) {
	return os.ReadFile(c.path(p))
}

func (c *Context) readDir(p string) ([]os.DirEntry, error) {
	return os.ReadDir(c.path(p))
}

func (c *Context) readlink(p string) (string, error) {
	return os.Readlink(c.path(p))
}

func (c *Context) removeAll(p string) error {
	return os.RemoveAll(c.path(p))
}

func (c *Context) evalSymlinks(p string) (string, error) {
	if c.EvalSymlinks != nil {
		return c.EvalSymlinks(p)
	}
	return filepath.EvalSymlinks(p)
}
