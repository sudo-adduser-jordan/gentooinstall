package installer

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gentooinstall/assets"
	"gentooinstall/internal/disklayout"
)

// BlkidUUIDForID resolves id and returns its filesystem UUID.
func BlkidUUIDForID(c *Context, id string) (string, error) {
	dev, err := resolveID(c, id)
	if err != nil {
		return "", err
	}
	u, err := disklayout.GetBlkidField("UUID", dev)
	if err != nil {
		return "", fmt.Errorf("could not get UUID from blkid for device=%s: %w", dev, err)
	}
	return u, nil
}

// KernelCmdline assembles the kernel command line for boot entries.
func KernelCmdline(c *Context) (string, error) {
	parts := []string{"rd.vconsole.keymap=" + c.Cfg.System.KeymapInitramfs}
	parts = append(parts, c.Layout.DracutCmdline...)
	if !c.Layout.Flags.UsedZFS {
		u, err := BlkidUUIDForID(c, c.Layout.RootID)
		if err != nil {
			return "", err
		}
		parts = append(parts, "root=UUID="+u)
	}
	return strings.Join(parts, " "), nil
}

// findNewestKernel returns the basename of the newest kernel in /boot
// (find + sort -V | tail -1).
func findNewestKernel(c *Context) (string, error) {
	entries, err := os.ReadDir("/boot")
	if err != nil {
		return "", fmt.Errorf("could not list /boot: %w", err)
	}
	var kernels []string
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, "vmlinuz-") || strings.HasPrefix(n, "kernel-") {
			kernels = append(kernels, n)
		}
	}
	if len(kernels) == 0 {
		return "", fmt.Errorf("could not find any kernel in /boot")
	}
	sort.Slice(kernels, func(i, j int) bool { return VersionLess(kernels[i], kernels[j]) })
	return kernels[len(kernels)-1], nil
}

// VersionLess reports whether kernel title a sorts before b by version
// (port of the find + sort -V | tail -1 logic).
func VersionLess(a, b string) bool {
	as, bs := splitVersion(a), splitVersion(b)
	for i := 0; i < len(as) && i < len(bs); i++ {
		ai, aok := atoiSafe(as[i])
		bi, bok := atoiSafe(bs[i])
		if aok && bok {
			if ai != bi {
				return ai < bi
			}
		} else if c := strings.Compare(as[i], bs[i]); c != 0 {
			return c < 0
		}
	}
	return len(as) < len(bs)
}

var versionSplit = regexp.MustCompile(`([0-9]+|[^0-9]+)`)

func splitVersion(s string) []string {
	return versionSplit.FindAllString(s, -1)
}

func atoiSafe(s string) (int, bool) {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, true
}

// GenerateInitramfs builds the initramfs with dracut and writes the
// regenerate helper script next to it (port of generate_initramfs).
func GenerateInitramfs(c *Context, output string) error {
	c.R.log("Generating initramfs")

	var modules []string
	if c.Layout.Flags.UsedRaid {
		modules = append(modules, "mdraid")
	}
	if c.Layout.Flags.UsedLuks {
		modules = append(modules, "crypt", "crypt-gpg")
	}
	if c.Layout.Flags.UsedBtrfs {
		modules = append(modules, "btrfs")
	}
	if c.Layout.Flags.UsedZFS {
		modules = append(modules, "zfs")
	}

	link, err := os.Readlink("/usr/src/linux")
	if err != nil {
		return fmt.Errorf("could not figure out kernel version from /usr/src/linux symlink: %w", err)
	}
	kver := strings.TrimPrefix(filepath.Base(link), "linux-")

	dracutOpts := []string{}
	addSSHD := c.Cfg.UsesSystemd() && c.Cfg.System.InitramfsSSHD
	if addSSHD {
		prev := c.R.Dir
		c.R.Dir = "/tmp"
		err = c.R.Try("git", "clone", "https://github.com/gsauthof/dracut-sshd")
		c.R.Dir = prev
		if err != nil {
			return err
		}
		if err := c.R.Try("cp", "-r", "/tmp/dracut-sshd/46sshd",
			"/usr/lib/dracut/modules.d"); err != nil {
			return err
		}
		svc := "/usr/lib/dracut/modules.d/46sshd/sshd.service"
		data, err := os.ReadFile(svc)
		if err != nil {
			return err
		}
		fixed := strings.ReplaceAll(string(data), "Type=notify", "Type=simple")
		fixed = strings.Replace(fixed, "ExecStart=/usr/sbin/sshd -D",
			"ExecStart=/usr/sbin/sshd -e -D", 1)
		if err := os.WriteFile(svc, []byte(fixed), 0o644); err != nil {
			return fmt.Errorf("could not replace sshd options in service file: %w", err)
		}
		dracutOpts = append(dracutOpts,
			"--install", "/etc/systemd/network/20-wired.network")
		modules = append(modules, "systemd-networkd")
	}

	args := []string{
		"--kver", kver,
		"--zstd",
		"--no-hostonly",
		"--ro-mnt",
		"--add", strings.Join(append([]string{"bash"}, modules...), " "),
	}
	args = append(args, dracutOpts...)
	args = append(args, "--force", output)
	if err := c.R.Try("dracut", args...); err != nil {
		return err
	}

	var sb strings.Builder
	sb.WriteString("#!/bin/bash\n")
	sb.WriteString("kver=\"$1\"\n")
	fmt.Fprintf(&sb, "output=\"$2\" # At setup time, this was %q\n", output)
	sb.WriteString("[[ -n \"$kver\" ]] || { echo \"usage $0 <kernel_version> <output>\" >&2; exit 1; }\n")
	sb.WriteString("dracut \\\n\t--kver          \"$kver\" \\\n\t--zstd \\\n" +
		"\t--no-hostonly \\\n\t--ro-mnt \\\n")
	fmt.Fprintf(&sb, "\t--add           %q \\\n",
		strings.Join(append([]string{"bash"}, modules...), " "))
	for _, o := range dracutOpts {
		fmt.Fprintf(&sb, "\t%q \\\n", o)
	}
	sb.WriteString("\t--force \\\n\t\"$output\"\n")
	helper := filepath.Join(filepath.Dir(output), "generate_initramfs.sh")
	if err := os.WriteFile(helper, []byte(sb.String()), 0o755); err != nil {
		return err
	}
	return nil
}

// EfiBootmgrArgs builds the efibootmgr argument vector (as used by
// InstallKernelEFI).
func EfiBootmgrArgs(disk, part, cmdline string) []string {
	return []string{
		"--verbose", "--create",
		"--disk", disk, "--part", part,
		"--label", "gentoo",
		"--loader", `\vmlinuz.efi`,
		"--unicode", `initrd=\initramfs.img ` + cmdline,
	}
}

// InstallKernelEFI installs kernel+initramfs to the ESP and creates the
// efibootmgr entry, handling RAID1 members (port of install_kernel_efi).
func InstallKernelEFI(c *Context) error {
	if err := c.R.Try("emerge", "--verbose", "sys-boot/efibootmgr"); err != nil {
		return err
	}

	kernelFile, err := findNewestKernel(c)
	if err != nil {
		return err
	}
	if err := c.R.Try("cp", "/boot/"+kernelFile, "/boot/efi/vmlinuz.efi"); err != nil {
		return err
	}
	if err := GenerateInitramfs(c, "/boot/efi/initramfs.img"); err != nil {
		return err
	}

	c.R.log("Creating EFI boot entry")
	efipartdev, err := resolveID(c, c.Layout.EFIID)
	if err != nil {
		return err
	}
	if efipartdev, err = filepath.EvalSymlinks(efipartdev); err != nil {
		return fmt.Errorf("error in realpath '%s': %w", efipartdev, err)
	}
	sysEfiPart := "/sys/class/block/" + filepath.Base(efipartdev)

	efipartnum := "1"
	if data, err := os.ReadFile(filepath.Join(sysEfiPart, "partition")); err == nil {
		efipartnum = strings.TrimSpace(string(data))
	} else {
		c.R.logf("Assuming partition 1 for RAID-based EFI on device %s", efipartdev)
	}

	cmdline, err := KernelCmdline(c)
	if err != nil {
		return err
	}

	var disks []RaidMember

	isMD := regexp.MustCompile(`^/dev/md[0-9]+$`).MatchString(efipartdev)
	scanOut, _ := c.R.QuietRun("mdadm", "--detail", "--scan", efipartdev)
	isRAID := isMD && strings.Contains(scanOut, "ARRAY "+efipartdev+" ")

	if isRAID {
		detail, err := c.R.QuietRun("mdadm", "--detail", efipartdev)
		if err != nil {
			return err
		}
		re := regexp.MustCompile(`active sync[^/]*/dev/\S+`)
		seen := map[string]bool{}
		for _, line := range strings.Split(detail, "\n") {
			if m := re.FindString(line); m != "" {
				dev := "/dev/" + m[strings.Index(m, "/dev/")+len("/dev/"):]
				if !seen[dev] {
					seen[dev] = true
					disks = append(disks, RaidMember{Disk: dev})
				}
			}
		}
		if len(disks) == 0 {
			return fmt.Errorf("RAID setup detected, but no valid member disks found for %s", efipartdev)
		}
		c.R.logf("RAID detected. RAID members: %s", DiskNames(disks))
	} else {
		parent := ""
		if real, err := filepath.EvalSymlinks(filepath.Join(sysEfiPart, "..")); err == nil {
			parent = "/dev/" + filepath.Base(real)
		}
		if parent == "/dev/block" || parent == "" || !fileExists(parent) {
			gptID, ok := c.Layout.ParentGPTOf(c.Layout.EFIID)
			if !ok {
				return fmt.Errorf("could not determine parent device for %s", efipartdev)
			}
			parent, err = resolveID(c, gptID)
			if err != nil {
				return err
			}
		}
		disks = []RaidMember{{Disk: parent}}
	}

	var lastDisk, lastPart string
	for _, d := range disks {
		lastDisk, lastPart = d.Disk, efipartnum
		c.R.logf("Adding EFI boot entry on %s", d.Disk)
		if err := c.R.Try("efibootmgr", EfiBootmgrArgs(d.Disk, efipartnum, cmdline)...); err != nil {
			return err
		}
	}

	script := "#!/bin/bash\n# This is the command that was used to create the efibootmgr entry when the\n" +
		"# system was installed using gentoo-install.\n" +
		"efibootmgr " + strings.Join(EfiBootmgrArgs(lastDisk, lastPart, cmdline), " ") + "\n"
	return os.WriteFile("/boot/efi/efibootmgr_add_entry.sh", []byte(script), 0o755)
}

// RaidMember is a physical disk of a RAID array used for an EFI boot entry.
type RaidMember struct{ Disk string }

// DiskNames renders RAID member disk paths joined by spaces (for logs).
func DiskNames(entries []RaidMember) string {
	var names []string
	for _, e := range entries {
		names = append(names, e.Disk)
	}
	return strings.Join(names, " ")
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

const syslinuxCfgTemplate = `DEFAULT gentoo
PROMPT 0
TIMEOUT 0

LABEL gentoo
	LINUX ../vmlinuz-current
	APPEND initrd=../initramfs.img %s
`

// InstallKernelBIOS installs syslinux based booting
// (port of install_kernel_bios).
func InstallKernelBIOS(c *Context) error {
	if err := c.R.Try("emerge", "--verbose", "sys-boot/syslinux"); err != nil {
		return err
	}
	kernelFile, err := findNewestKernel(c)
	if err != nil {
		return err
	}
	if err := c.R.Try("cp", "/boot/"+kernelFile, "/boot/bios/vmlinuz-current"); err != nil {
		return err
	}
	if err := GenerateInitramfs(c, "/boot/bios/initramfs.img"); err != nil {
		return err
	}

	c.R.log("Installing syslinux")
	biosdev, err := resolveID(c, c.Layout.BIOSID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll("/boot/bios/syslinux", 0o700); err != nil {
		return err
	}
	if err := c.R.Try("syslinux", "--directory", "syslinux", "--install", biosdev); err != nil {
		return err
	}

	cmdline, err := KernelCmdline(c)
	if err != nil {
		return err
	}
	cfg := fmt.Sprintf(syslinuxCfgTemplate, cmdline)
	if err := os.WriteFile("/boot/bios/syslinux/syslinux.cfg", []byte(cfg), 0o644); err != nil {
		return fmt.Errorf("could not save generated syslinux.cfg: %w", err)
	}

	c.R.log("Copying syslinux MBR record")
	gptID, ok := c.Layout.ParentGPTOf(c.Layout.BIOSID)
	if !ok {
		return fmt.Errorf("no gpt table registered for bios partition %s", c.Layout.BIOSID)
	}
	gptdev, err := resolveID(c, gptID)
	if err != nil {
		return err
	}
	return c.R.Try("dd", "bs=440", "conv=notrunc", "count=1",
		"if=/usr/share/syslinux/gptmbr.bin", "of="+gptdev)
}

// InstallKernel installs the kernel and makes the system bootable.
func InstallKernel(c *Context) error {
	c.R.log("Installing vanilla kernel and related tools")
	if c.IsEFI() {
		if err := InstallKernelEFI(c); err != nil {
			return err
		}
	} else {
		if err := InstallKernelBIOS(c); err != nil {
			return err
		}
	}

	if c.Cfg.Packages.KernelDeblob {
		c.R.log("Skipping linux-firmware (deblob kernel)")
		return nil
	}

	c.R.log("Installing linux-firmware")
	f, err := os.OpenFile("/etc/portage/package.license",
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("could not write to /etc/portage/package.license: %w", err)
	}
	if _, err := f.WriteString(
		"sys-kernel/linux-firmware linux-fw-redistributable no-source-code\n"); err != nil {
		f.Close()
		return err
	}
	f.Close()
	return c.R.Try("emerge", "--verbose", "linux-firmware")
}

func addFstabEntry(c *Context, fs, mountpoint, typ, opts, dumpPass string) error {
	f, err := os.OpenFile("/etc/fstab", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("could not append entry to fstab: %w", err)
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "%-46s  %-24s  %-6s  %-96s %s\n",
		fs, mountpoint, typ, opts, dumpPass)
	return err
}

// GenerateFstab writes /etc/fstab from the layout roles
// (port of generate_fstab).
func GenerateFstab(c *Context) error {
	c.R.log("Generating fstab")
	if err := os.WriteFile("/etc/fstab", []byte(assets.Fstab), 0o644); err != nil {
		return fmt.Errorf("could not overwrite /etc/fstab: %w", err)
	}

	if !c.Layout.Flags.UsedZFS && c.Layout.RootFSType != "" {
		u, err := BlkidUUIDForID(c, c.Layout.RootID)
		if err != nil {
			return err
		}
		if err := addFstabEntry(c, "UUID="+u, "/", c.Layout.RootFSType,
			c.Layout.RootMountOpts, "0 1"); err != nil {
			return err
		}
	}

	if c.IsEFI() {
		u, err := BlkidUUIDForID(c, c.Layout.EFIID)
		if err != nil {
			return err
		}
		if err := addFstabEntry(c, "UUID="+u, "/boot/efi", "vfat",
			"defaults,noatime,fmask=0177,dmask=0077,noexec,nodev,nosuid,discard", "0 2"); err != nil {
			return err
		}
	} else {
		u, err := BlkidUUIDForID(c, c.Layout.BIOSID)
		if err != nil {
			return err
		}
		if err := addFstabEntry(c, "UUID="+u, "/boot/bios", "vfat",
			"defaults,noatime,fmask=0177,dmask=0077,noexec,nodev,nosuid,discard", "0 2"); err != nil {
			return err
		}
	}

	if c.Layout.SwapID != "" {
		u, err := BlkidUUIDForID(c, c.Layout.SwapID)
		if err != nil {
			return err
		}
		if err := addFstabEntry(c, "UUID="+u, "none", "swap",
			"defaults,discard", "0 0"); err != nil {
			return err
		}
	}
	return nil
}
