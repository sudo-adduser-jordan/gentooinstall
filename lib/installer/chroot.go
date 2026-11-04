package installer

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gentooinstall/lib/disklayout"
	"gentooinstall/lib/sysinfo"
)

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

func unescapeMount(str string) string {
	str = strings.ReplaceAll(str, `\040`, " ")
	str = strings.ReplaceAll(str, `\011`, "\t")
	return str
}

func (ctx *Context) isMountpoint(path string) bool {
	if ctx.IsMountpoint != nil {
		return ctx.IsMountpoint(path)
	}
	return IsMountpoint(path)
}

func MountEfiVars(ctx *Context) error {
	if ctx.isMountpoint("/sys/firmware/efi/efivars") {
		return nil
	}
	if !ctx.hostHasEFI() {
		return fmt.Errorf("cannot mount efivarfs: the live system was not booted " +
			"in UEFI mode (/sys/firmware/efi is missing) but the configuration " +
			`requests an EFI install (disk.boot_type = "efi"); the live ISO is ` +
			`hybrid BIOS+UEFI, so either reboot it under UEFI (bare metal: enable ` +
			`UEFI boot; QEMU: add OVMF firmware, e.g. -drive ` +
			`if=pflash,format=raw,readonly=on,file=/usr/share/OVMF/x64/OVMF_CODE.4m.fd ` +
			`-drive if=pflash,format=raw,file=/tmp/OVMF_VARS.fd) or switch to a ` +
			`legacy-BIOS install with disk.boot_type = "bios" (e.g. builds/bios.toml)`)
	}
	ctx.Runner.log("Mounting efivars")
	if err := ctx.mkdirAll("/sys/firmware/efi/efivars", 0o755); err != nil {
		return fmt.Errorf("could not create efivars mountpoint: %w", err)
	}
	if err := ctx.Runner.Try("mount", "-t", "efivarfs", "efivarfs",
		"/sys/firmware/efi/efivars"); err != nil {
		return fmt.Errorf("could not mount efivarfs: %w", err)
	}
	return nil
}

func CheckHostBootMode(ctx *Context) error {
	if ctx.IsEFI() && !ctx.hostHasEFI() {
		return fmt.Errorf("configuration uses an EFI boot partition but the live " +
			"system was not booted in UEFI mode (/sys/firmware/efi is missing); " +
			"the live ISO is hybrid BIOS+UEFI, so either reboot it under UEFI " +
			"(bare metal: enable UEFI boot; QEMU: add OVMF firmware, e.g. -drive " +
			"if=pflash,format=raw,readonly=on,file=/usr/share/OVMF/x64/OVMF_CODE.4m.fd " +
			"-drive if=pflash,format=raw,file=/tmp/OVMF_VARS.fd) or set " +
			"disk.boot_type = \"bios\" (e.g. builds/bios.toml)")
	}
	return nil
}

func (ctx *Context) SupportsFilesystem(fs string) bool {
	if ctx.Filesystems != nil {
		return ctx.Filesystems(fs)
	}
	return sysinfo.SupportsFilesystem(fs)
}

func CheckFilesystemSupport(ctx *Context) error {
	if ctx.Layout == nil {
		return nil
	}
	mountpoint := "/boot/bios"
	if ctx.IsEFI() {
		mountpoint = "/boot/efi"
	}
	if ctx.SupportsFilesystem("vfat") {
		return nil
	}
	return fmt.Errorf("the live kernel has no vfat support so %s cannot be mounted "+
		"(mkfs.fat formats the boot partition as FAT32); rebuild the live ISO on a host "+
		"whose kernel provides vfat (CONFIG_VFAT_FS=y, or =m with the modules installed "+
		"so release.sh can bundle vfat.ko and the live boot logs \"live: loaded module vfat\"); "+
		"retrying this install cannot help", mountpoint)
}

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

func hasNameserver(data []byte) bool {
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "nameserver") {
			return true
		}
	}
	return false
}

func MountByID(ctx *Context, id, mountpoint string) error {
	if ctx.isMountpoint(mountpoint) {
		dev, err := resolveID(ctx, id)
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
	ctx.Runner.logf("Mounting device with id=%s to '%s'", id, mountpoint)
	if err := ctx.mkdirAll(mountpoint, 0o755); err != nil {
		return fmt.Errorf("could not create mountpoint directory '%s': %w", mountpoint, err)
	}
	dev, err := resolveID(ctx, id)
	if err != nil {
		return err
	}
	if err := ctx.Runner.Try("mount", dev, mountpoint); err != nil {
		return fmt.Errorf("could not mount device '%s': %w", dev, err)
	}
	return nil
}

var virtualFS = []struct {
	mountpoint string
	proc       []string
	rbind      bool
}{
	{"/proc", []string{"-t", "proc", "/proc"}, false},
	{"/run", nil, true},
	{"/tmp", nil, true},
	{"/sys", nil, true},
	{"/dev", nil, true},
	{"/dev/pts", []string{"-t", "devpts", "devpts", "-o", "gid=5,mode=620"}, false},
}

var devSymlinks = []struct{ name, target string }{
	{"fd", "/proc/self/fd"},
	{"stdin", "/proc/self/fd/0"},
	{"stdout", "/proc/self/fd/1"},
	{"stderr", "/proc/self/fd/2"},
}

func EnsureDevSymlinks(chrootDir string) error {
	devDir := filepath.Join(chrootDir, "dev")
	if err := os.MkdirAll(devDir, 0o755); err != nil {
		return fmt.Errorf("could not create '%s': %w", devDir, err)
	}
	for _, symlink := range devSymlinks {
		path := filepath.Join(devDir, symlink.name)
		if _, err := os.Lstat(path); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("could not inspect '%s': %w", path, err)
		}
		if err := os.Symlink(symlink.target, path); err != nil && !os.IsExist(err) {
			return fmt.Errorf("could not create '%s' symlink: %w", path, err)
		}
	}
	return nil
}

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

func PrepareChrootEnv(ctx *Context, chrootDir string) error {
	ctx.Runner.log("Preparing chroot environment")
	dst := filepath.Join(chrootDir, "etc/resolv.conf")
	src, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		return fmt.Errorf("could not read host resolv.conf (live network not up?): %w", err)
	}
	if !hasNameserver(src) {
		return fmt.Errorf("host resolv.conf has no nameserver entry, chroot would have no DNS")
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(dst, src, 0o644); err != nil {
		return fmt.Errorf("could not copy resolv.conf: %w", err)
	}

	ctx.Runner.log("Mounting virtual filesystems")
	for _, vfs := range virtualFS {
		mp := filepath.Join(chrootDir, vfs.mountpoint)
		if ctx.isMountpoint(mp) {
			continue
		}
		if err := os.MkdirAll(mp, 0o755); err != nil {
			return err
		}
		var err error
		if vfs.rbind {
			err = ctx.Runner.Try("mount", "--rbind", vfs.mountpoint, mp)
			if err == nil {
				err = ctx.Runner.Try("mount", "--make-rslave", mp)
			}
		} else {
			args := append([]string{}, vfs.proc...)
			args = append(args, mp)
			err = ctx.Runner.Try("mount", args...)
		}
		if err != nil {
			return fmt.Errorf("could not mount virtual filesystems (%s): %w",
				vfs.mountpoint, err)
		}
	}

	if err := EnsureDevSymlinks(chrootDir); err != nil {
		return err
	}
	if err := CheckChrootEnv(chrootDir); err != nil {
		return err
	}

	return ctx.Resolver.CacheLsblkOutput()
}

func UnmountChroot(ctx *Context, chrootDir string) error {
	var firstErr error
	unmount := func(mp string) {
		if !ctx.isMountpoint(mp) {
			return
		}
		if err := ctx.Runner.Try("umount", "-l", mp); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("could not unmount '%s': %w", mp, err)
		}
	}
	for index := len(virtualFS) - 1; index >= 0; index-- {
		unmount(filepath.Join(chrootDir, virtualFS[index].mountpoint))
	}
	unmount(chrootDir)
	return firstErr
}

func EnterChroot(ctx *Context, chrootDir string, args ...string) error {
	if err := CheckChrootEnv(chrootDir); err != nil {
		return err
	}
	if err := StageBind(ctx); err != nil {
		return err
	}
	fullArgs := []string{chrootDir, BinInBind(),
		"--in-chroot", "--config", ConfigInBind()}
	fullArgs = append(fullArgs, args...)

	env := append(os.Environ(),
		"EXECUTED_IN_CHROOT=true",
		"TMP_DIR="+TmpDir,
		"GENTOO_CACHED_LSBLK="+ctx.Resolver.CachedEnvValue(),
	)
	ctx.Runner.log("Chrooting...")
	cmd := exec.Command("chroot", fullArgs...)
	if ctx.Runner.NonInteractive {
		env = append(env, NonInteractiveEnv+"=1")
		cmd.Stdin = nil
	} else {
		cmd.Stdin = os.Stdin
	}
	outTW := NewTailWriter(ctx.Runner.stdout(), maxTailLines)
	errTW := NewTailWriter(ctx.Runner.stderr(), maxTailLines)
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

func ChrootShell(ctx *Context, chrootDir string, args ...string) error {
	initScript := filepath.Join(TmpDir, "chroot-init.sh")
	script := ("source /etc/profile 2>/dev/null; " +
		"export PS1='(chroot) \\u@\\h \\w \\$ '; " +
		"export PS1=\"\\[\\033[0;31m\\]\\u\\[\\033[1;31m\\]@\\h \\[\\033[1;34m\\]\\w \\[\\033[m\\]\\$ \\[\\033[m\\]\"")
	if err := ctx.writeFile(initScript, []byte(script+"\n"), 0o644); err != nil {
		return err
	}
	cmdArgs := []string{chrootDir, "/bin/bash", "--init-file", initScript}
	if len(args) > 0 {
		cmdArgs = append(cmdArgs, "-c", strings.Join(args, " "))
	}
	ctx.Runner.logf("Chrooting into %s ...", chrootDir)
	ctx.Runner.logf("To later unmount all virtual filesystems, simply use umount -l -R %q", chrootDir)
	cmd := exec.Command("chroot", cmdArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
