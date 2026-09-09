// Full in-chroot installation sequences (MainInstallGentooInChroot).
package tests

import (
	"fmt"
	"strings"
	"testing"

	"gentooinstall/lib/installer"
)

// kernelAndBootScaffold plants the /boot, /usr/src/linux symlink and the
// /sys/class/block partition info InstallKernelEFI/BIOS needs.
func kernelAndBootScaffold(testingT *testing.T, ctx *installer.Context, kver string, partNum string) {
	testingT.Helper()
	mkScratchDir(testingT, ctx, "/boot")
	writeScratch(testingT, ctx, "/boot/vmlinuz-"+kver, "kernel image\n")
	mkScratchDir(testingT, ctx, "/boot/efi")
	mkScratchDir(testingT, ctx, "/boot/bios")
	symlinkScratch(testingT, ctx, "linux-"+kver, "/usr/src/linux")
	if partNum != "" {
		mkScratchDir(testingT, ctx, "/sys/class/block/fake-part_efi")
		writeScratch(testingT, ctx, "/sys/class/block/fake-part_efi/partition", partNum+"\n")
	}
}

// fstabRow mirrors the row layout GenerateFstab uses so tests can assert the
// exact stored content.
func fstabRow(fs, mountpoint, typ, opts, dumpPass string) string {
	return fmt.Sprintf("%-46s  %-24s  %-6s  %-96s %s",
		fs, mountpoint, typ, opts, dumpPass)
}

func TestMainInstallGentooInChrootSystemdEFILuks(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", true, false)
	cfg.System.Timezone = "UTC"
	cfg.System.Keymap = "us"
	cfg.System.KeymapInitramfs = "us"
	ctx, stub := testContext(testingT, cfg, classicSeeds())
	mkScratchDir(testingT, ctx, "/etc")

	rootUUID, efiUUID, swapUUID :=
		"aaaaaaa1-0000-0000-0000-000000000001",
		"aaaaaaa1-0000-0000-0000-000000000002",
		"aaaaaaa1-0000-0000-0000-000000000003"
	ctx.BlkidUUID = func(dev string) (string, error) {
		switch dev {
		case "/dev/fake-part_luks_root":
			return rootUUID, nil
		case "/dev/fake-part_efi":
			return efiUUID, nil
		case "/dev/fake-part_swap":
			return swapUUID, nil
		}
		return "", fmt.Errorf("unexpected device for blkid: %s", dev)
	}
	ctx.EvalSymlinks = func(path string) (string, error) {
		// Fakes the realpath of the EFI partition's sysfs parent so
		// InstallKernelEFI falls back to the registered GPT table.
		if path == "/sys/class/block/fake-part_efi/.." {
			return "/sys/class/block/fake-part_efi", nil
		}
		return path, nil
	}
	kernelAndBootScaffold(testingT, ctx, "6.6.13-gentoo", "3")

	if err := installer.MainInstallGentooInChroot(ctx); err != nil {
		testingT.Fatal(err)
	}

	cmdline := "rd.vconsole.keymap=us rd.luks.uuid=" + uLuksRoot +
		" root=UUID=" + rootUUID
	assertCmds(testingT, stub,
		"passwd -d root",
		"emerge-webrsync",
		"mount -t efivarfs efivarfs /sys/firmware/efi/efivars",
		"mount /dev/fake-part_efi /boot/efi",
		"locale-gen",
		"systemd-machine-id-setup",
		"ln -sfn ../usr/share/zoneinfo/UTC /etc/localtime",
		"env-update",
		"emerge --verbose dev-vcs/git",
		"emerge --sync",
		"ssh-keygen -A",
		"emerge --verbose sys-kernel/dracut sys-kernel/gentoo-kernel-bin app-arch/zstd",
		"emerge --verbose sys-fs/cryptsetup",
		"emerge --verbose --changed-use --oneshot sys-apps/systemd",
		"emerge --verbose sys-boot/efibootmgr",
		"cp /boot/vmlinuz-6.6.13-gentoo /boot/efi/vmlinuz.efi",
		"dracut --kver 6.6.13-gentoo --zstd --no-hostonly --ro-mnt --add bash crypt crypt-gpg --force /boot/efi/initramfs.img",
		"mdadm --detail --scan /dev/fake-part_efi",
		"efibootmgr --verbose --create --disk /dev/fake-gpt --part 3 --label gentoo --loader \\vmlinuz.efi --unicode initrd=\\initramfs.img "+cmdline,
		"emerge --verbose linux-firmware",
		"emerge --verbose app-portage/gentoolkit",
		"systemctl enable systemd-networkd",
		"systemctl enable systemd-resolved",
		"chown root:systemd-network /etc/systemd/network/20-wired.network",
		"chmod 640 /etc/systemd/network/20-wired.network",
		"systemctl enable sshd",
		"passwd -d root",
	)

	// Files produced in the scratch root.
	if got := readScratch(testingT, ctx, "/etc/hostname"); got != "gentoo\n" {
		testingT.Fatalf("/etc/hostname = %q", got)
	}
	if got := readScratch(testingT, ctx, "/etc/vconsole.conf"); got != "KEYMAP=us\n" {
		testingT.Fatalf("/etc/vconsole.conf = %q", got)
	}
	if got := readScratch(testingT, ctx, "/etc/locale.conf"); got != "LANG=C.UTF-8\n" {
		testingT.Fatalf("/etc/locale.conf = %q", got)
	}
	if got := readScratch(testingT, ctx, "/etc/locale.gen"); got != "C.UTF-8 UTF-8\n" {
		testingT.Fatalf("/etc/locale.gen = %q", got)
	}
	if got := readScratch(testingT, ctx, "/etc/portage/package.use/installkernel"); got != "sys-kernel/installkernel dracut\n" {
		testingT.Fatalf("/etc/portage/package.use/installkernel = %q", got)
	}
	if got := readScratch(testingT, ctx, "/etc/portage/package.use/systemd"); got != "sys-apps/systemd cryptsetup\n" {
		testingT.Fatalf("/etc/portage/package.use/systemd = %q", got)
	}

	// repos.conf written for git sync.
	repos := readScratch(testingT, ctx, "/etc/portage/repos.conf/gentoo.conf")
	for _, want := range []string{"main-repo = gentoo", "sync-type = git",
		"sync-depth = 1", "sync-uri = https://anongit.gentoo.org/git/repo/sync/gentoo.git"} {
		if !strings.Contains(repos, want) {
			testingT.Fatalf("gentoo.conf missing %q:\n%s", want, repos)
		}
	}

	// make.conf accumulates MAKEOPTS + ACCEPT_KEYWORDS.
	makeConf := readScratch(testingT, ctx, "/etc/portage/make.conf")
	for _, want := range []string{"MAKEOPTS=\"-j8\"", "ACCEPT_KEYWORDS=\"~amd64\""} {
		if !strings.Contains(makeConf, want) {
			testingT.Fatalf("/etc/portage/make.conf missing %q:\n%s", want, makeConf)
		}
	}

	// networkd unit.
	net := readScratch(testingT, ctx, "/etc/systemd/network/20-wired.network")
	if !strings.Contains(net, "DHCP=yes") {
		testingT.Fatalf("networkd unit missing DHCP:\n%s", net)
	}

	// sshd configuration present.
	if got := readScratch(testingT, ctx, "/etc/ssh/sshd_config"); !strings.Contains(got, "PermitRootLogin") {
		testingT.Fatalf("sshd_config looks wrong:\n%s", got)
	}

	// fstab rows from the layout + blkid stub.
	fstab := readScratch(testingT, ctx, "/etc/fstab")
	for _, row := range []string{
		fstabRow("UUID="+rootUUID, "/", "ext4", "defaults,noatime,errors=remount-ro,discard", "0 1"),
		fstabRow("UUID="+efiUUID, "/boot/efi", "vfat", "defaults,noatime,fmask=0177,dmask=0077,noexec,nodev,nosuid,discard", "0 2"),
		fstabRow("UUID="+swapUUID, "none", "swap", "defaults,discard", "0 0"),
	} {
		if !strings.Contains(fstab, row) {
			testingT.Fatalf("/etc/fstab missing row %q:\n%s", row, fstab)
		}
	}

	// efibootmgr re-run script recorded the same entry.
	script := readScratch(testingT, ctx, "/boot/efi/efibootmgr_add_entry.sh")
	if !strings.Contains(script, "efibootmgr --verbose --create --disk /dev/fake-gpt --part 3") {
		testingT.Fatalf("efibootmgr_add_entry.sh:\n%s", script)
	}
	// initramfs regenerator helper carries the initramfs path.
	helper := readScratch(testingT, ctx, "/boot/efi/generate_initramfs.sh")
	if !strings.Contains(helper, "/boot/efi/initramfs.img") {
		testingT.Fatalf("generate_initramfs.sh:\n%s", helper)
	}
}

func TestMainInstallGentooInChrootOpenRCBIOS(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, true)
	cfg.Gentoo.Stage3Variant = "openrc"
	cfg.Disk.BootType = "bios"
	cfg.Disk.UseSwap = false
	cfg.System.Timezone = "UTC"
	cfg.System.Keymap = "de"
	cfg.System.KeymapInitramfs = "us"
	ctx, stub := testContext(testingT, cfg, map[string]string{"gpt": uGpt, "part_bios": uEfi, "part_root": uRoot})
	mkScratchDir(testingT, ctx, "/etc")

	// OpenRC configurators rewrite existing conf.d files in place.
	writeScratch(testingT, ctx, "/etc/conf.d/hostname", "hostname=\"gentoo\"\n")
	writeScratch(testingT, ctx, "/etc/conf.d/keymaps", "keymap=\"us\"\n")
	ctx.BlkidUUID = func(dev string) (string, error) {
		return "aaaaaaa1-0000-0000-0000-000000000005", nil
	}
	ctx.EvalSymlinks = func(path string) (string, error) { return path, nil }
	kernelAndBootScaffold(testingT, ctx, "6.8.11-gentoo-dist", "")

	if err := installer.MainInstallGentooInChroot(ctx); err != nil {
		testingT.Fatal(err)
	}

	assertCmds(testingT, stub,
		"passwd -d root",
		"emerge-webrsync",
		"mount /dev/fake-part_bios /boot/bios",
		"locale-gen",
		"emerge -v --config sys-libs/timezone-data",
		"eselect locale set C.UTF-8",
		"env-update",
		"emerge --verbose dev-vcs/git",
		"emerge --sync",
		"ssh-keygen -A",
		"emerge --verbose sys-kernel/dracut sys-kernel/gentoo-kernel-bin app-arch/zstd",
		"emerge --verbose sys-fs/btrfs-progs",
		"emerge --verbose sys-boot/syslinux",
		"cp /boot/vmlinuz-6.8.11-gentoo-dist /boot/bios/vmlinuz-current",
		"dracut --kver 6.8.11-gentoo-dist --zstd --no-hostonly --ro-mnt --add bash btrfs --force /boot/bios/initramfs.img",
		"syslinux --directory syslinux --install /dev/fake-part_bios",
		"dd bs=440 conv=notrunc count=1 if=/usr/share/syslinux/gptmbr.bin of=/dev/fake-gpt",
		"emerge --verbose linux-firmware",
		"emerge --verbose app-portage/gentoolkit",
		"emerge --verbose net-misc/dhcpcd",
		"rc-update add dhcpcd default",
		"rc-update add sshd default",
		"passwd -d root",
	)

	// conf.d rewrites.
	if got := readScratch(testingT, ctx, "/etc/conf.d/hostname"); got != "hostname=\"gentoo\"\n" {
		testingT.Fatalf("/etc/conf.d/hostname = %q", got)
	}
	if got := readScratch(testingT, ctx, "/etc/conf.d/keymaps"); got != "keymap=\"de\"\n" {
		testingT.Fatalf("/etc/conf.d/keymaps = %q", got)
	}
	if got := readScratch(testingT, ctx, "/etc/timezone"); got != "UTC\n" {
		testingT.Fatalf("/etc/timezone = %q", got)
	}

	// syslinux.cfg embeds the root UUID in the APPEND line.
	cfgContent := readScratch(testingT, ctx, "/boot/bios/syslinux/syslinux.cfg")
	if !strings.Contains(cfgContent, "root=UUID=aaaaaaa1-0000-0000-0000-000000000005") {
		testingT.Fatalf("syslinux.cfg missing root=UUID line:\n%s", cfgContent)
	}

	// No EFI partition rows in fstab for BIOS installs.
	fstab := readScratch(testingT, ctx, "/etc/fstab")
	if strings.Contains(fstab, "/boot/efi") {
		testingT.Fatalf("/etc/fstab mentions /boot/efi on a BIOS install:\n%s", fstab)
	}
}

func TestMainInstallGentooInChrootPropagatesEmergeError(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Disk.UseSwap = false
	cfg.Disk.BootType = "bios"
	ctx, stub := testContext(testingT, cfg, map[string]string{"gpt": uGpt, "part_bios": uEfi, "part_root": uRoot})
	stub.FailOn = []string{"emerge-webrsync"}

	err := installer.MainInstallGentooInChroot(ctx)
	if err == nil {
		testingT.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "emerge-webrsync") {
		testingT.Fatalf("error should mention emerge-webrsync: %v", err)
	}
}

func TestAskYesNoNonInteractiveUsesDefault(testingT *testing.T) {
	stub := NewExecStub()
	runner := installer.NewRunner(discardWriter{testingT}, discardWriter{testingT})
	runner.Exec = stub
	runner.NonInteractive = true

	yes, err := installer.AskYesNo(runner, "Proceed?", true)
	if err != nil || !yes {
		testingT.Fatalf("expected default yes, got %v err %v", yes, err)
	}
	no, err := installer.AskYesNo(runner, "Proceed?", false)
	if err != nil || no {
		testingT.Fatalf("expected default no, got %v err %v", no, err)
	}
}

func TestConfigProfilePackagesSelected(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Gentoo.Profile = "default/linux/amd64/23.0/desktop"
	cfg.System.Timezone = "UTC"
	cfg.System.Keymap = "us"
	cfg.Disk.BootType = "bios"
	cfg.Disk.UseSwap = false
	ctx, stub := testContext(testingT, cfg, map[string]string{"gpt": uGpt, "part_bios": uEfi, "part_root": uRoot})
	mkScratchDir(testingT, ctx, "/etc")
	kernelAndBootScaffold(testingT, ctx, "6.8.11-gentoo-dist", "")
	ctx.EvalSymlinks = func(path string) (string, error) { return path, nil }

	if err := installer.MainInstallGentooInChroot(ctx); err != nil {
		testingT.Fatal(err)
	}
	pkgs := cfg.ProfilePackages()
	if len(pkgs) == 0 {
		testingT.Fatal("workstation profile should add packages")
	}
	want := append([]string{"emerge", "--verbose",
		"--autounmask-continue=y", "--"}, pkgs...)
	assertCmdContains(testingT, stub, []string{installer.CommandLine(want[0], want[1:]...)})
}
