package installer

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gentooinstall/assets"
	"gentooinstall/lib/config"
	"gentooinstall/lib/disklayout"
)

// BlkidUUIDForID resolves id and returns its filesystem UUID.
func BlkidUUIDForID(ctx *Context, id string) (string, error) {
	dev, err := resolveID(ctx, id)
	if err != nil {
		return "", err
	}
	if ctx.BlkidUUID != nil {
		return ctx.BlkidUUID(dev)
	}
	fsUUID, err := disklayout.GetBlkidField("UUID", dev)
	if err != nil {
		return "", fmt.Errorf("could not get UUID from blkid for device=%s: %w", dev, err)
	}
	return fsUUID, nil
}

// KernelCmdline assembles the kernel command line for boot entries.
func KernelCmdline(ctx *Context) (string, error) {
	parts := []string{"rd.vconsole.keymap=" + ctx.Cfg.System.KeymapInitramfs}
	parts = append(parts, ctx.Layout.DracutCmdline...)
	if !ctx.Layout.Flags.UsedZFS {
		fsUUID, err := BlkidUUIDForID(ctx, ctx.Layout.RootID)
		if err != nil {
			return "", err
		}
		parts = append(parts, "root=UUID="+fsUUID)
	}
	return strings.Join(parts, " "), nil
}

// FindNewestKernel returns the basename of the newest kernel in /boot
// (find + sort -V | tail -1).
func FindNewestKernel(ctx *Context) (string, error) {
	entries, err := ctx.readDir("/boot")
	if err != nil {
		return "", fmt.Errorf("could not list /boot: %w", err)
	}
	var kernels []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "vmlinuz-") || strings.HasPrefix(name, "kernel-") {
			kernels = append(kernels, name)
		}
	}
	if len(kernels) == 0 {
		return "", fmt.Errorf("could not find any kernel in /boot")
	}
	sort.Slice(kernels, func(leftIdx, rightIdx int) bool {
		return VersionLess(kernels[leftIdx], kernels[rightIdx])
	})
	return kernels[len(kernels)-1], nil
}

// VersionLess reports whether kernel title a sorts before b by version
// (port of the find + sort -V | tail -1 logic).
func VersionLess(left, right string) bool {
	leftParts, rightParts := splitVersion(left), splitVersion(right)
	for index := 0; index < len(leftParts) && index < len(rightParts); index++ {
		numA, isA := atoiSafe(leftParts[index])
		numB, isB := atoiSafe(rightParts[index])
		if isA && isB {
			if numA != numB {
				return numA < numB
			}
		} else if compared := strings.Compare(leftParts[index], rightParts[index]); compared != 0 {
			return compared < 0
		}
	}
	return len(leftParts) < len(rightParts)
}

var versionSplit = regexp.MustCompile(`([0-9]+|[^0-9]+)`)

func splitVersion(str string) []string {
	return versionSplit.FindAllString(str, -1)
}

func atoiSafe(str string) (int, bool) {
	num := 0
	for _, digit := range str {
		if digit < '0' || digit > '9' {
			return 0, false
		}
		num = num*10 + int(digit-'0')
	}
	return num, true
}

// GenerateInitramfs builds the initramfs with dracut and writes the
// regenerate helper script next to it (port of generate_initramfs).
func GenerateInitramfs(ctx *Context, output string) error {
	ctx.Runner.log("Generating initramfs")

	var modules []string
	if ctx.Layout.Flags.UsedRaid {
		modules = append(modules, "mdraid")
	}
	if ctx.Layout.Flags.UsedLuks {
		modules = append(modules, "crypt", "crypt-gpg")
	}
	if ctx.Layout.Flags.UsedBtrfs {
		modules = append(modules, "btrfs")
	}
	if ctx.Layout.Flags.UsedZFS {
		modules = append(modules, "zfs")
	}

	link, err := ctx.readlink("/usr/src/linux")
	if err != nil {
		return fmt.Errorf("could not figure out kernel version from /usr/src/linux symlink: %w", err)
	}
	kver := strings.TrimPrefix(filepath.Base(link), "linux-")

	dracutOpts := []string{}
	addSSHD := ctx.Cfg.UsesSystemd() && ctx.Cfg.System.InitramfsSSHD
	if addSSHD {
		prev := ctx.Runner.Dir
		ctx.Runner.Dir = "/tmp"
		err = ctx.Runner.Try("git", "clone", "https://github.com/gsauthof/dracut-sshd")
		ctx.Runner.Dir = prev
		if err != nil {
			return err
		}
		if err := ctx.Runner.Try("cp", "-r", "/tmp/dracut-sshd/46sshd",
			"/usr/lib/dracut/modules.d"); err != nil {
			return err
		}
		svc := "/usr/lib/dracut/modules.d/46sshd/sshd.service"
		data, err := ctx.readFile(svc)
		if err != nil {
			return err
		}
		fixed := strings.ReplaceAll(string(data), "Type=notify", "Type=simple")
		fixed = strings.Replace(fixed, "ExecStart=/usr/sbin/sshd -D",
			"ExecStart=/usr/sbin/sshd -e -D", 1)
		if err := ctx.writeFile(svc, []byte(fixed), 0o644); err != nil {
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
	if err := ctx.Runner.Try("dracut", args...); err != nil {
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
	for _, opt := range dracutOpts {
		fmt.Fprintf(&sb, "\t%q \\\n", opt)
	}
	sb.WriteString("\t--force \\\n\t\"$output\"\n")
	helper := filepath.Join(filepath.Dir(output), "generate_initramfs.sh")
	if err := ctx.writeFile(helper, []byte(sb.String()), 0o755); err != nil {
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
func InstallKernelEFI(ctx *Context) error {
	if err := ctx.Runner.Try("emerge", "--verbose", "sys-boot/efibootmgr"); err != nil {
		return err
	}

	kernelFile, err := FindNewestKernel(ctx)
	if err != nil {
		return err
	}
	if err := ctx.Runner.Try("cp", "/boot/"+kernelFile, "/boot/efi/vmlinuz.efi"); err != nil {
		return err
	}
	if err := GenerateInitramfs(ctx, "/boot/efi/initramfs.img"); err != nil {
		return err
	}

	ctx.Runner.log("Creating EFI boot entry")
	efipartdev, err := resolveID(ctx, ctx.Layout.EFIID)
	if err != nil {
		return err
	}
	if efipartdev, err = ctx.evalSymlinks(efipartdev); err != nil {
		return fmt.Errorf("error in realpath '%s': %w", efipartdev, err)
	}
	sysEfiPart := "/sys/class/block/" + filepath.Base(efipartdev)

	efipartnum := "1"
	if data, err := ctx.readFile(filepath.Join(sysEfiPart, "partition")); err == nil {
		efipartnum = strings.TrimSpace(string(data))
	} else {
		ctx.Runner.logf("Assuming partition 1 for RAID-based EFI on device %s", efipartdev)
	}

	cmdline, err := KernelCmdline(ctx)
	if err != nil {
		return err
	}

	var disks []RaidMember

	isMD := regexp.MustCompile(`^/dev/md[0-9]+$`).MatchString(efipartdev)
	scanOut, _ := ctx.Runner.QuietRun("mdadm", "--detail", "--scan", efipartdev)
	isRAID := isMD && strings.Contains(scanOut, "ARRAY "+efipartdev+" ")

	if isRAID {
		detail, err := ctx.Runner.QuietRun("mdadm", "--detail", efipartdev)
		if err != nil {
			return err
		}
		re := regexp.MustCompile(`active sync[^/]*/dev/\S+`)
		seen := map[string]bool{}
		for _, line := range strings.Split(detail, "\n") {
			if match := re.FindString(line); match != "" {
				dev := "/dev/" + match[strings.Index(match, "/dev/")+len("/dev/"):]
				if !seen[dev] {
					seen[dev] = true
					disks = append(disks, RaidMember{Disk: dev})
				}
			}
		}
		if len(disks) == 0 {
			return fmt.Errorf("RAID setup detected, but no valid member disks found for %s", efipartdev)
		}
		ctx.Runner.logf("RAID detected. RAID members: %s", DiskNames(disks))
	} else {
		parent := ""
		if real, err := ctx.evalSymlinks(filepath.Join(sysEfiPart, "..")); err == nil {
			parent = "/dev/" + filepath.Base(real)
		}
		if parent == "/dev/block" || parent == "" || !fileExists(parent) {
			gptID, ok := ctx.Layout.ParentGPTOf(ctx.Layout.EFIID)
			if !ok {
				return fmt.Errorf("could not determine parent device for %s", efipartdev)
			}
			parent, err = resolveID(ctx, gptID)
			if err != nil {
				return err
			}
		}
		disks = []RaidMember{{Disk: parent}}
	}

	var lastDisk, lastPart string
	for _, disk := range disks {
		lastDisk, lastPart = disk.Disk, efipartnum
		ctx.Runner.logf("Adding EFI boot entry on %s", disk.Disk)
		if err := ctx.Runner.Try("efibootmgr", EfiBootmgrArgs(disk.Disk, efipartnum, cmdline)...); err != nil {
			return err
		}
	}

	script := "#!/bin/bash\n# This is the command that was used to create the efibootmgr entry when the\n" +
		"# system was installed using gentoo-install.\n" +
		"efibootmgr " + strings.Join(EfiBootmgrArgs(lastDisk, lastPart, cmdline), " ") + "\n"
	return ctx.writeFile("/boot/efi/efibootmgr_add_entry.sh", []byte(script), 0o755)
}

// RaidMember is a physical disk of a RAID array used for an EFI boot entry.
type RaidMember struct{ Disk string }

// DiskNames renders RAID member disk paths joined by spaces (for logs).
func DiskNames(entries []RaidMember) string {
	var names []string
	for _, member := range entries {
		names = append(names, member.Disk)
	}
	return strings.Join(names, " ")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
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
func InstallKernelBIOS(ctx *Context) error {
	if err := ctx.Runner.Try("emerge", "--verbose", "sys-boot/syslinux"); err != nil {
		return err
	}
	kernelFile, err := FindNewestKernel(ctx)
	if err != nil {
		return err
	}
	if err := ctx.Runner.Try("cp", "/boot/"+kernelFile, "/boot/bios/vmlinuz-current"); err != nil {
		return err
	}
	if err := GenerateInitramfs(ctx, "/boot/bios/initramfs.img"); err != nil {
		return err
	}

	ctx.Runner.log("Installing syslinux")
	biosdev, err := resolveID(ctx, ctx.Layout.BIOSID)
	if err != nil {
		return err
	}
	if err := ctx.mkdirAll("/boot/bios/syslinux", 0o700); err != nil {
		return err
	}
	if err := ctx.Runner.Try("syslinux", "--directory", "syslinux", "--install", biosdev); err != nil {
		return err
	}

	cmdline, err := KernelCmdline(ctx)
	if err != nil {
		return err
	}
	cfg := fmt.Sprintf(syslinuxCfgTemplate, cmdline)
	if err := ctx.writeFile("/boot/bios/syslinux/syslinux.cfg", []byte(cfg), 0o644); err != nil {
		return fmt.Errorf("could not save generated syslinux.cfg: %w", err)
	}

	ctx.Runner.log("Copying syslinux MBR record")
	gptID, ok := ctx.Layout.ParentGPTOf(ctx.Layout.BIOSID)
	if !ok {
		return fmt.Errorf("no gpt table registered for bios partition %s", ctx.Layout.BIOSID)
	}
	gptdev, err := resolveID(ctx, gptID)
	if err != nil {
		return err
	}
	return ctx.Runner.Try("dd", "bs=440", "conv=notrunc", "count=1",
		"if=/usr/share/syslinux/gptmbr.bin", "of="+gptdev)
}

// InstallKernel installs the kernel and makes the system bootable.
func InstallKernel(ctx *Context) error {
	ctx.Runner.log("Installing vanilla kernel and related tools")
	if ctx.IsEFI() {
		if err := InstallKernelEFI(ctx); err != nil {
			return err
		}
	} else {
		if err := InstallKernelBIOS(ctx); err != nil {
			return err
		}
	}

	if ctx.Cfg.Packages.KernelDeblob {
		ctx.Runner.log("Skipping linux-firmware (deblob kernel)")
		return nil
	}
	if !ctx.Cfg.Packages.InstallFirmware {
		ctx.Runner.log("Skipping linux-firmware (disabled in config)")
		return nil
	}

	ctx.Runner.log("Installing linux-firmware")
	if err := ctx.appendFile("/etc/portage/package.license",
		"sys-kernel/linux-firmware linux-fw-redistributable no-source-code"); err != nil {
		return err
	}
	if err := ctx.Runner.Try("emerge", "--verbose", "--getbinpkg", "linux-firmware"); err != nil {
		return err
	}
	return pruneFirmwareSections(ctx)
}

// pruneFirmwareSections removes the firmware directories under /lib/firmware
// that belong to catalog sections the user did not select. An empty selection
// installs the whole package, so there is nothing to prune.
func pruneFirmwareSections(ctx *Context) error {
	if len(ctx.Cfg.Packages.FirmwareSections) == 0 {
		return nil
	}
	selected := map[string]bool{}
	for _, name := range ctx.Cfg.Packages.FirmwareSections {
		selected[name] = true
	}
	ctx.Runner.log("Removing unselected linux-firmware sections")
	for _, section := range config.FirmwareSections {
		if selected[section.Name] {
			continue
		}
		if err := ctx.Runner.Try("rm", "-rf", filepath.Join("/lib/firmware", section.Name)); err != nil {
			return fmt.Errorf("could not remove firmware section %s: %w", section.Name, err)
		}
	}
	return nil
}

// addFstabEntry appends one formatted fstab row.
func addFstabEntry(ctx *Context, fs, mountpoint, typ, opts, dumpPass string) error {
	row := fmt.Sprintf("%-46s  %-24s  %-6s  %-96s %s",
		fs, mountpoint, typ, opts, dumpPass)
	return ctx.appendFile("/etc/fstab", row)
}

// GenerateFstab writes /etc/fstab from the layout roles
// (port of generate_fstab).
func GenerateFstab(ctx *Context) error {
	ctx.Runner.log("Generating fstab")
	if err := ctx.writeFile("/etc/fstab", []byte(assets.Fstab), 0o644); err != nil {
		return fmt.Errorf("could not overwrite /etc/fstab: %w", err)
	}

	if !ctx.Layout.Flags.UsedZFS && ctx.Layout.RootFSType != "" {
		fsUUID, err := BlkidUUIDForID(ctx, ctx.Layout.RootID)
		if err != nil {
			return err
		}
		if err := addFstabEntry(ctx, "UUID="+fsUUID, "/", ctx.Layout.RootFSType,
			ctx.Layout.RootMountOpts, "0 1"); err != nil {
			return err
		}
	}

	if ctx.IsEFI() {
		fsUUID, err := BlkidUUIDForID(ctx, ctx.Layout.EFIID)
		if err != nil {
			return err
		}
		if err := addFstabEntry(ctx, "UUID="+fsUUID, "/boot/efi", "vfat",
			"defaults,noatime,fmask=0177,dmask=0077,noexec,nodev,nosuid,discard", "0 2"); err != nil {
			return err
		}
	} else {
		fsUUID, err := BlkidUUIDForID(ctx, ctx.Layout.BIOSID)
		if err != nil {
			return err
		}
		if err := addFstabEntry(ctx, "UUID="+fsUUID, "/boot/bios", "vfat",
			"defaults,noatime,fmask=0177,dmask=0077,noexec,nodev,nosuid,discard", "0 2"); err != nil {
			return err
		}
	}

	if ctx.Layout.SwapID != "" {
		fsUUID, err := BlkidUUIDForID(ctx, ctx.Layout.SwapID)
		if err != nil {
			return err
		}
		if err := addFstabEntry(ctx, "UUID="+fsUUID, "none", "swap",
			"defaults,discard", "0 0"); err != nil {
			return err
		}
	}
	return nil
}
