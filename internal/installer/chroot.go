package installer

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gentooinstall/internal/disklayout"
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

// MountEfiVars mounts efivarfs when not already present. It is only called
// for EFI layouts and fails fast when the live system is not running under
// UEFI, where efivarfs cannot exist.
func MountEfiVars(c *Context) error {
	if c.isMountpoint("/sys/firmware/efi/efivars") {
		return nil
	}
	if !c.hostHasEFI() {
		return fmt.Errorf("cannot mount efivarfs: the live system was not booted " +
			"in UEFI mode (/sys/firmware/efi is missing) but the configuration " +
			`requests an EFI install (disk.boot_type = "efi"); boot the live medium ` +
			`under UEFI or set disk.boot_type = "bios" (e.g. builds/bios.toml)`)
	}
	c.R.log("Mounting efivars")
	if err := c.mkdirAll("/sys/firmware/efi/efivars", 0o755); err != nil {
		return fmt.Errorf("could not create efivars mountpoint: %w", err)
	}
	if err := c.R.Try("mount", "-t", "efivarfs", "efivarfs",
		"/sys/firmware/efi/efivars"); err != nil {
		return fmt.Errorf("could not mount efivarfs: %w", err)
	}
	return nil
}

// CheckHostBootMode fails fast when the configured boot type is not
// supported by the running firmware: an EFI layout on a non-UEFI boot can
// never mount efivarfs or register boot entries later, so it is rejected
// before any destructive partitioning happens.
func CheckHostBootMode(c *Context) error {
	if c.IsEFI() && !c.hostHasEFI() {
		return fmt.Errorf("configuration uses an EFI boot partition but the live " +
			"system was not booted in UEFI mode (/sys/firmware/efi is missing); " +
			"boot the live medium under UEFI or set disk.boot_type = \"bios\" " +
			"(e.g. builds/bios.toml)")
	}
	return nil
}

// MountSource returns the source device currently mounted at path, or ""
// when path is not a mountpoint (or /proc/mounts is unreadable).
func MountSource(path string) string {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && unescapeMount(fields[1]) == path {
			return unescapeMount(fields[0])
		}
	}
	return ""
}

// MountByID mounts the device identified by id at mountpoint.
func MountByID(c *Context, id, mountpoint string) error {
	if c.isMountpoint(mountpoint) {
		// Already mounted: reuse it only when it is the expected device.
		// A stale mount from a previous layout must never be silently
		// reused (a later mkfs/cleanup would hit the wrong filesystem).
		dev, err := resolveID(c, id)
		if err != nil {
			return err
		}
		if src := MountSource(mountpoint); src != "" &&
			disklayout.Canonicalize(src) != dev {
			return fmt.Errorf("'%s' is already mounted from '%s', expected '%s' "+
				"(id=%s): unmount the stale mount before retrying",
				mountpoint, src, dev, id)
		}
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
	// devpts must come after /dev: portage, sshd and other tools expect
	// /dev/pts, and the live ISO's devtmpfs-only /dev never provides it.
	{"/dev/pts", []string{"-t", "devpts", "devpts", "-o", "gid=5,mode=620"}, false},
}

// devSymlinks are the standard /dev entries portage's bash helpers rely on
// (process substitution <(...) needs /dev/fd). The live ISO runs a
// devtmpfs-only /dev without udev, so these symlinks may be absent on the
// host; a bare --rbind would then propagate the gap into the chroot and
// emerge-webrsync fails with "/dev/fd/63: No such file or directory".
var devSymlinks = []struct{ name, target string }{
	{"fd", "/proc/self/fd"},
	{"stdin", "/proc/self/fd/0"},
	{"stdout", "/proc/self/fd/1"},
	{"stderr", "/proc/self/fd/2"},
}

// EnsureDevSymlinks creates the standard /dev/{fd,stdin,stdout,stderr}
// symlinks under chrootDir/dev when missing. Existing entries of any type
// are never clobbered.
func EnsureDevSymlinks(chrootDir string) error {
	devDir := filepath.Join(chrootDir, "dev")
	if err := os.MkdirAll(devDir, 0o755); err != nil {
		return fmt.Errorf("could not create '%s': %w", devDir, err)
	}
	for _, s := range devSymlinks {
		p := filepath.Join(devDir, s.name)
		if _, err := os.Lstat(p); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("could not inspect '%s': %w", p, err)
		}
		if err := os.Symlink(s.target, p); err != nil && !os.IsExist(err) {
			return fmt.Errorf("could not create '%s' symlink: %w", p, err)
		}
	}
	return nil
}

// CheckChrootEnv verifies the virtual filesystems a chroot needs are usable:
// /proc must be mounted (proc/self resolves) and /dev/fd must resolve
// through to it. It fails fast with an actionable message instead of
// letting portage die later on "/dev/fd/63: No such file or directory".
func CheckChrootEnv(chrootDir string) error {
	if fi, err := os.Stat(filepath.Join(chrootDir, "proc", "self")); err != nil || !fi.IsDir() {
		return fmt.Errorf("chroot at '%s' has no usable /proc "+
			"(run PrepareChrootEnv or mount -t proc proc '%s' first)",
			chrootDir, filepath.Join(chrootDir, "proc"))
	}
	if _, err := os.Stat(filepath.Join(chrootDir, "dev", "fd")); err != nil {
		return fmt.Errorf("chroot at '%s' has no usable /dev/fd "+
			"(run PrepareChrootEnv or bind /dev and create the fd symlinks first): %w",
			chrootDir, err)
	}
	return nil
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
		if c.isMountpoint(mp) {
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

	// The live ISO's devtmpfs-only /dev may lack the fd symlinks portage
	// needs, so create them explicitly, then verify the whole environment
	// before any chrooted command can fail opaquely on /dev/fd.
	if err := EnsureDevSymlinks(chrootDir); err != nil {
		return err
	}
	if err := CheckChrootEnv(chrootDir); err != nil {
		return err
	}

	// lsblk output must be cached before entering the chroot because it
	// returns almost no information from within.
	return c.Resolver.CacheLsblkOutput()
}

// UnmountChroot lazily unmounts the chroot environment at chrootDir: the
// virtual filesystems in reverse mount order, then chrootDir itself.
// Missing mountpoints are skipped; the first error is returned after all
// attempts. Callers use it for best-effort cleanup on failure paths so a
// re-run does not trip over leaked binds.
func UnmountChroot(c *Context, chrootDir string) error {
	var firstErr error
	unmount := func(mp string) {
		if !c.isMountpoint(mp) {
			return
		}
		if err := c.R.Try("umount", "-l", mp); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("could not unmount '%s': %w", mp, err)
		}
	}
	for i := len(virtualFS) - 1; i >= 0; i-- {
		unmount(filepath.Join(chrootDir, virtualFS[i].mountpoint))
	}
	unmount(chrootDir)
	return firstErr
}

// EnterChroot re-executes this binary inside the chroot to run the
// in-chroot phase (port of exec chroot ... dispatch_chroot.sh). The child's
// output is streamed as usual, but also retained so a failure carries the
// tail back in the returned error instead of only living in scrollback.
func EnterChroot(c *Context, chrootDir string, args ...string) error {
	if err := CheckChrootEnv(chrootDir); err != nil {
		return err
	}
	if err := StageBind(c); err != nil {
		return err
	}
	fullArgs := []string{chrootDir, BinInBind(),
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
	outTW := NewTailWriter(c.R.stdout(), maxTailLines)
	errTW := NewTailWriter(c.R.stderr(), maxTailLines)
	cmd.Stdout = outTW
	cmd.Stderr = errTW
	cmd.Env = env
	err := cmd.Run()
	outTW.Flush()
	errTW.Flush()
	if err != nil {
		tail := append(outTW.Tail(), errTW.Tail()...)
		if len(tail) > maxTailLines {
			tail = tail[len(tail)-maxTailLines:]
		}
		detail := ""
		if len(tail) > 0 {
			detail = "\n\nRecent output:\n" + strings.Join(tail, "\n")
		}
		if ee, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("chroot phase failed with exit code %d: %w%s",
				ee.ExitCode(), err, detail)
		}
		return fmt.Errorf("failed to chroot into '%s': %w%s", chrootDir, err, detail)
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
	cmdArgs := []string{chrootDir, "/bin/bash", "--init-file", initScript}
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
