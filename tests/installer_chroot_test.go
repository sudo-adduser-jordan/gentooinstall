package tests

import (
	"fmt"
	"strings"
	"testing"

	"gentooinstall/internal/installer"
)

// kernelAndBootScaffold plants the /boot, /usr/src/linux symlink and the
// /sys/class/block partition info InstallKernelEFI/BIOS needs.
func kernelAndBootScaffold(t *testing.T, c *installer.Context, kver string, partNum string) {
	t.Helper()
	mkScratchDir(t, c, "/boot")
	writeScratch(t, c, "/boot/vmlinuz-"+kver, "kernel image\n")
	mkScratchDir(t, c, "/boot/efi")
	mkScratchDir(t, c, "/boot/bios")
	symlinkScratch(t, c, "linux-"+kver, "/usr/src/linux")
	if partNum != "" {
		mkScratchDir(t, c, "/sys/class/block/fake-part_efi")
		writeScratch(t, c, "/sys/class/block/fake-part_efi/partition", partNum+"\n")
	}
}

// fstabRow mirrors the row layout GenerateFstab uses so tests can assert the
// exact stored content.
func fstabRow(fs, mountpoint, typ, opts, dumpPass string) string {
	return fmt.Sprintf("%-46s  %-24s  %-6s  %-96s %s",
		fs, mountpoint, typ, opts, dumpPass)
}

func TestMainInstallGentooInChrootSystemdEFILuks(t *testing.T) {
	cfg := classicCfg("/dev/sdX", true, false)
	cfg.System.Timezone = "UTC"
	cfg.System.Keymap = "us"
	cfg.System.KeymapInitramfs = "us"
	c, s := testContext(t, cfg, classicSeeds())
	mkScratchDir(t, c, "/etc")

	rootUUID, efiUUID, swapUUID :=
		"aaaaaaa1-0000-0000-0000-000000000001",
		"aaaaaaa1-0000-0000-0000-000000000002",
		"aaaaaaa1-0000-0000-0000-000000000003"
	c.BlkidUUID = func(dev string) (string, error) {
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
	c.EvalSymlinks = func(p string) (string, error) {
		// Fakes the realpath of the EFI partition's sysfs parent so
		// InstallKernelEFI falls back to the registered GPT table.
		if p == "/sys/class/block/fake-part_efi/.." {
			return "/sys/class/block/fake-part_efi", nil
		}
		return p, nil
	}
	kernelAndBootScaffold(t, c, "6.6.13-gentoo", "3")

	if err := installer.MainInstallGentooInChroot(c); err != nil {
		t.Fatal(err)
	}

	cmdline := "rd.vconsole.keymap=us rd.luks.uuid=" + uLuksRoot +
		" root=UUID=" + rootUUID
	assertCmds(t, s,
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
		"emerge --verbose dev-vcs/git",
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
	if got := readScratch(t, c, "/etc/hostname"); got != "gentoo\n" {
		t.Fatalf("/etc/hostname = %q", got)
	}
	if got := readScratch(t, c, "/etc/vconsole.conf"); got != "KEYMAP=us\n" {
		t.Fatalf("/etc/vconsole.conf = %q", got)
	}
	if got := readScratch(t, c, "/etc/locale.conf"); got != "LANG=C.UTF-8\n" {
		t.Fatalf("/etc/locale.conf = %q", got)
	}
	if got := readScratch(t, c, "/etc/locale.gen"); got != "C.UTF-8 UTF-8\n" {
		t.Fatalf("/etc/locale.gen = %q", got)
	}
	if got := readScratch(t, c, "/etc/portage/package.use/installkernel"); got != "sys-kernel/installkernel dracut\n" {
		t.Fatalf("/etc/portage/package.use/installkernel = %q", got)
	}
	if got := readScratch(t, c, "/etc/portage/package.use/systemd"); got != "sys-apps/systemd cryptsetup\n" {
		t.Fatalf("/etc/portage/package.use/systemd = %q", got)
	}

	// repos.conf written for git sync.
	repos := readScratch(t, c, "/etc/portage/repos.conf/gentoo.conf")
	for _, want := range []string{"main-repo = gentoo", "sync-type = git",
		"sync-depth = 1", "sync-uri = https://anongit.gentoo.org/git/repo/sync/gentoo.git"} {
		if !strings.Contains(repos, want) {
			t.Fatalf("gentoo.conf missing %q:\n%s", want, repos)
		}
	}

	// make.conf accumulates MAKEOPTS + ACCEPT_KEYWORDS.
	makeConf := readScratch(t, c, "/etc/portage/make.conf")
	for _, want := range []string{"MAKEOPTS=\"-j8\"", "ACCEPT_KEYWORDS=\"~amd64\""} {
		if !strings.Contains(makeConf, want) {
			t.Fatalf("/etc/portage/make.conf missing %q:\n%s", want, makeConf)
		}
	}

	// networkd unit.
	net := readScratch(t, c, "/etc/systemd/network/20-wired.network")
	if !strings.Contains(net, "DHCP=yes") {
		t.Fatalf("networkd unit missing DHCP:\n%s", net)
	}

	// sshd configuration present.
	if got := readScratch(t, c, "/etc/ssh/sshd_config"); !strings.Contains(got, "PermitRootLogin") {
		t.Fatalf("sshd_config looks wrong:\n%s", got)
	}

	// fstab rows from the layout + blkid stub.
	fstab := readScratch(t, c, "/etc/fstab")
	for _, row := range []string{
		fstabRow("UUID="+rootUUID, "/", "ext4", "defaults,noatime,errors=remount-ro,discard", "0 1"),
		fstabRow("UUID="+efiUUID, "/boot/efi", "vfat", "defaults,noatime,fmask=0177,dmask=0077,noexec,nodev,nosuid,discard", "0 2"),
		fstabRow("UUID="+swapUUID, "none", "swap", "defaults,discard", "0 0"),
	} {
		if !strings.Contains(fstab, row) {
			t.Fatalf("/etc/fstab missing row %q:\n%s", row, fstab)
		}
	}

	// efibootmgr re-run script recorded the same entry.
	script := readScratch(t, c, "/boot/efi/efibootmgr_add_entry.sh")
	if !strings.Contains(script, "efibootmgr --verbose --create --disk /dev/fake-gpt --part 3") {
		t.Fatalf("efibootmgr_add_entry.sh:\n%s", script)
	}
	// initramfs regenerator helper carries the initramfs path.
	helper := readScratch(t, c, "/boot/efi/generate_initramfs.sh")
	if !strings.Contains(helper, "/boot/efi/initramfs.img") {
		t.Fatalf("generate_initramfs.sh:\n%s", helper)
	}
}

func TestMainInstallGentooInChrootOpenRCBIOS(t *testing.T) {
	cfg := classicCfg("/dev/sdX", false, true)
	cfg.Gentoo.Stage3Variant = "openrc"
	cfg.Disk.BootType = "bios"
	cfg.Disk.UseSwap = false
	cfg.System.Timezone = "UTC"
	cfg.System.Keymap = "de"
	cfg.System.KeymapInitramfs = "us"
	c, s := testContext(t, cfg, map[string]string{"gpt": uGpt, "part_bios": uEfi, "part_root": uRoot})
	mkScratchDir(t, c, "/etc")

	// OpenRC configurators rewrite existing conf.d files in place.
	writeScratch(t, c, "/etc/conf.d/hostname", "hostname=\"gentoo\"\n")
	writeScratch(t, c, "/etc/conf.d/keymaps", "keymap=\"us\"\n")
	c.BlkidUUID = func(dev string) (string, error) {
		return "aaaaaaa1-0000-0000-0000-000000000005", nil
	}
	c.EvalSymlinks = func(p string) (string, error) { return p, nil }
	kernelAndBootScaffold(t, c, "6.8.11-gentoo-dist", "")

	if err := installer.MainInstallGentooInChroot(c); err != nil {
		t.Fatal(err)
	}

	assertCmds(t, s,
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
		"emerge --verbose dev-vcs/git",
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
	if got := readScratch(t, c, "/etc/conf.d/hostname"); got != "hostname=\"gentoo\"\n" {
		t.Fatalf("/etc/conf.d/hostname = %q", got)
	}
	if got := readScratch(t, c, "/etc/conf.d/keymaps"); got != "keymap=\"de\"\n" {
		t.Fatalf("/etc/conf.d/keymaps = %q", got)
	}
	if got := readScratch(t, c, "/etc/timezone"); got != "UTC\n" {
		t.Fatalf("/etc/timezone = %q", got)
	}

	// syslinux.cfg embeds the root UUID in the APPEND line.
	cfgContent := readScratch(t, c, "/boot/bios/syslinux/syslinux.cfg")
	if !strings.Contains(cfgContent, "root=UUID=aaaaaaa1-0000-0000-0000-000000000005") {
		t.Fatalf("syslinux.cfg missing root=UUID line:\n%s", cfgContent)
	}

	// No EFI partition rows in fstab for BIOS installs.
	fstab := readScratch(t, c, "/etc/fstab")
	if strings.Contains(fstab, "/boot/efi") {
		t.Fatalf("/etc/fstab mentions /boot/efi on a BIOS install:\n%s", fstab)
	}
}

func TestMainInstallGentooInChrootPropagatesEmergeError(t *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Disk.UseSwap = false
	cfg.Disk.BootType = "bios"
	c, s := testContext(t, cfg, map[string]string{"gpt": uGpt, "part_bios": uEfi, "part_root": uRoot})
	s.FailOn = []string{"emerge-webrsync"}

	err := installer.MainInstallGentooInChroot(c)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "emerge-webrsync") {
		t.Fatalf("error should mention emerge-webrsync: %v", err)
	}
}

func TestAskYesNoNonInteractiveUsesDefault(t *testing.T) {
	stub := NewExecStub()
	r := installer.NewRunner(discardWriter{t}, discardWriter{t})
	r.Exec = stub
	r.NonInteractive = true

	yes, err := installer.AskYesNo(r, "Proceed?", true)
	if err != nil || !yes {
		t.Fatalf("expected default yes, got %v err %v", yes, err)
	}
	no, err := installer.AskYesNo(r, "Proceed?", false)
	if err != nil || no {
		t.Fatalf("expected default no, got %v err %v", no, err)
	}
}

func TestConfigProfilePackagesSelected(t *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Gentoo.Profile = "default/linux/amd64/23.0/desktop"
	cfg.System.Timezone = "UTC"
	cfg.System.Keymap = "us"
	cfg.Disk.BootType = "bios"
	cfg.Disk.UseSwap = false
	c, s := testContext(t, cfg, map[string]string{"gpt": uGpt, "part_bios": uEfi, "part_root": uRoot})
	mkScratchDir(t, c, "/etc")
	kernelAndBootScaffold(t, c, "6.8.11-gentoo-dist", "")
	c.EvalSymlinks = func(p string) (string, error) { return p, nil }

	if err := installer.MainInstallGentooInChroot(c); err != nil {
		t.Fatal(err)
	}
	pkgs := cfg.ProfilePackages()
	if len(pkgs) == 0 {
		t.Fatal("workstation profile should add packages")
	}
	want := append([]string{"emerge", "--verbose",
		"--autounmask-continue=y", "--"}, pkgs...)
	assertCmdContains(t, s, []string{installer.CommandLine(want[0], want[1:]...)})
}
