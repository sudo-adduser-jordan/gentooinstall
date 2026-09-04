package installer

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// IsMountpoint reports whether path appears in /proc/mounts.
func IsMountpoint(path string) bool {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && unescapeMount(fields[1]) == path {
			return true
		}
	}
	return false
}

func unescapeMount(s string) string {
	s = strings.ReplaceAll(s, `\040`, " ")
	s = strings.ReplaceAll(s, `\011`, "\t")
	return s
}

func (c *Context) isMountpoint(path string) bool {
	if c.IsMountpoint != nil {
		return c.IsMountpoint(path)
	}
	return IsMountpoint(path)
}

// MountEfiVars mounts efivarfs when not already present.
func MountEfiVars(c *Context) error {
	if c.isMountpoint("/sys/firmware/efi/efivars") {
		return nil
	}
	c.R.log("Mounting efivars")
	if err := c.R.Try("mount", "-t", "efivarfs", "efivarfs",
		"/sys/firmware/efi/efivars"); err != nil {
		return fmt.Errorf("could not mount efivarfs: %w", err)
	}
	return nil
}

// MountByID mounts the device identified by id at mountpoint.
func MountByID(c *Context, id, mountpoint string) error {
	if c.isMountpoint(mountpoint) {
		return nil
	}
	c.R.logf("Mounting device with id=%s to '%s'", id, mountpoint)
	if err := c.mkdirAll(mountpoint, 0o755); err != nil {
		return fmt.Errorf("could not create mountpoint directory '%s': %w", mountpoint, err)
	}
	dev, err := resolveID(c, id)
	if err != nil {
		return err
	}
	if err := c.R.Try("mount", dev, mountpoint); err != nil {
		return fmt.Errorf("could not mount device '%s': %w", dev, err)
	}
	return nil
}

var virtualFS = []struct {
	mountpoint string
	proc       []string // plain mount
	rbind      bool     // rbind + make-rslave
}{
	{"/proc", []string{"-t", "proc", "/proc"}, false},
	{"/run", nil, true},
	{"/tmp", nil, true},
	{"/sys", nil, true},
	{"/dev", nil, true},
}

// PrepareChrootEnv copies resolv.conf and mounts the virtual filesystems
// inside chrootDir (port of gentoo_chroot's environment setup).
func PrepareChrootEnv(c *Context, chrootDir string) error {
	c.R.log("Preparing chroot environment")
	dst := filepath.Join(chrootDir, "etc/resolv.conf")
	src, _ := os.ReadFile("/etc/resolv.conf")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(dst, src, 0o644); err != nil {
		return fmt.Errorf("could not copy resolv.conf: %w", err)
	}

	c.R.log("Mounting virtual filesystems")
	for _, vfs := range virtualFS {
		mp := filepath.Join(chrootDir, vfs.mountpoint)
		if IsMountpoint(mp) {
			continue
		}
		if err := os.MkdirAll(mp, 0o755); err != nil {
			return err
		}
		var err error
		if vfs.rbind {
			err = c.R.Try("mount", "--rbind", vfs.mountpoint, mp)
			if err == nil {
				err = c.R.Try("mount", "--make-rslave", mp)
			}
		} else {
			args := append([]string{}, vfs.proc...)
			args = append(args, mp)
			err = c.R.Try("mount", args...)
		}
		if err != nil {
			return fmt.Errorf("could not mount virtual filesystems (%s): %w",
				vfs.mountpoint, err)
		}
	}

	// lsblk output must be cached before entering the chroot because it
	// returns almost no information from within.
	return c.Resolver.CacheLsblkOutput()
}

// EnterChroot re-executes this binary inside the chroot to run the
// in-chroot phase (port of exec chroot ... dispatch_chroot.sh).
func EnterChroot(c *Context, chrootDir string, args ...string) error {
	if err := StageBind(c); err != nil {
		return err
	}
	fullArgs := []string{"--", chrootDir, BinInBind(),
		"--in-chroot", "--config", ConfigInBind()}
	fullArgs = append(fullArgs, args...)

	env := append(os.Environ(),
		"EXECUTED_IN_CHROOT=true",
		"TMP_DIR="+TmpDir,
		"GENTOO_CACHED_LSBLK="+c.Resolver.CachedEnvValue(),
	)
	c.R.log("Chrooting...")
	cmd := exec.Command("chroot", fullArgs...)
	if c.R.NonInteractive {
		env = append(env, NonInteractiveEnv+"=1")
		cmd.Stdin = nil // null device
	} else {
		cmd.Stdin = os.Stdin
	}
	cmd.Stdout = c.R.stdout()
	cmd.Stderr = c.R.stderr()
	cmd.Env = env
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("chroot phase failed with exit code %d: %w",
				ee.ExitCode(), err)
		}
		return fmt.Errorf("failed to chroot into '%s': %w", chrootDir, err)
	}
	return nil
}

// ChrootShell drops into an interactive bash within chrootDir
// (port of `install --chroot DIR` without command).
func ChrootShell(c *Context, chrootDir string, args ...string) error {
	initScript := filepath.Join(TmpDir, "chroot-init.sh")
	script := ("source /etc/profile 2>/dev/null; " +
		"export PS1='(chroot) \\u@\\h \\w \\$ '; " +
		"export PS1=\"\\[\\033[0;31m\\]\\u\\[\\033[1;31m\\]@\\h \\[\\033[1;34m\\]\\w \\[\\033[m\\]\\$ \\[\\033[m\\]\"")
	if err := c.writeFile(initScript, []byte(script+"\n"), 0o644); err != nil {
		return err
	}
	cmdArgs := []string{"--", chrootDir, "/bin/bash", "--init-file", initScript}
	if len(args) > 0 {
		cmdArgs = append(cmdArgs, "-c", strings.Join(args, " "))
	}
	c.R.logf("Chrooting into %s ...", chrootDir)
	c.R.logf("To later unmount all virtual filesystems, simply use umount -l -R %q", chrootDir)
	cmd := exec.Command("chroot", cmdArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
