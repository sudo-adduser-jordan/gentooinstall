// Kernel install, initramfs, cmdline and fstab generation.
package tests

import (
	"fmt"
	"strings"
	"testing"

	"gentooinstall/lib/config"
	"gentooinstall/lib/installer"
)

func TestFindNewestKernel(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	ctx, _ := testContext(testingT, cfg, nil)
	mkScratchDir(testingT, ctx, "/boot")
	for _, kernel := range []string{"vmlinuz-6.1.0", "vmlinuz-6.8.2", "vmlinuz-6.8.11"} {
		writeScratch(testingT, ctx, "/boot/"+kernel, "")
	}

	got, err := installer.FindNewestKernel(ctx)
	if err != nil {
		testingT.Fatal(err)
	}
	if got != "vmlinuz-6.8.11" {
		testingT.Fatalf("FindNewestKernel = %q, want vmlinuz-6.8.11", got)
	}
}

func TestFindNewestKernelMissing(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	ctx, _ := testContext(testingT, cfg, nil)
	mkScratchDir(testingT, ctx, "/boot")

	if _, err := installer.FindNewestKernel(ctx); err == nil {
		testingT.Fatal("expected error for empty /boot")
	}
}

func TestGenerateInitramfsLuks(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", true, false)
	ctx, stub := testContext(testingT, cfg, nil)
	symlinkScratch(testingT, ctx, "linux-6.6.13-gentoo", "/usr/src/linux")
	mkScratchDir(testingT, ctx, "/boot/efi")

	if err := installer.GenerateInitramfs(ctx, "/boot/efi/initramfs.img"); err != nil {
		testingT.Fatal(err)
	}
	assertCmdContains(testingT, stub, []string{
		"dracut --kver 6.6.13-gentoo --zstd --no-hostonly --ro-mnt --add bash crypt crypt-gpg --force /boot/efi/initramfs.img",
	})
	helper := readScratch(testingT, ctx, "/boot/efi/generate_initramfs.sh")
	for _, want := range []string{"--add", "\"bash crypt crypt-gpg\"", "/boot/efi/initramfs.img"} {
		if !strings.Contains(helper, want) {
			testingT.Fatalf("helper missing %q:\n%s", want, helper)
		}
	}
}

func TestGenerateInitramfsSSHD(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.System.InitramfsSSHD = true
	ctx, stub := testContext(testingT, cfg, nil)
	symlinkScratch(testingT, ctx, "linux-6.6.13-gentoo", "/usr/src/linux")
	mkScratchDir(testingT, ctx, "/boot/efi")
	writeScratch(testingT, ctx,
		"/usr/lib/dracut/modules.d/46sshd/sshd.service",
		"[Service]\nType=notify\nExecStart=/usr/sbin/sshd -D\n")

	if err := installer.GenerateInitramfs(ctx, "/boot/efi/initramfs.img"); err != nil {
		testingT.Fatal(err)
	}
	assertCmdContains(testingT, stub, []string{
		"git clone https://github.com/gsauthof/dracut-sshd",
		"cp -r /tmp/dracut-sshd/46sshd /usr/lib/dracut/modules.d",
		"dracut --kver 6.6.13-gentoo --zstd --no-hostonly --ro-mnt --add bash systemd-networkd --install /etc/systemd/network/20-wired.network --force /boot/efi/initramfs.img",
	})
	svc := readScratch(testingT, ctx, "/usr/lib/dracut/modules.d/46sshd/sshd.service")
	if strings.Contains(svc, "Type=notify") || !strings.Contains(svc, "Type=simple") {
		testingT.Fatalf("sshd.service not fixed:\n%s", svc)
	}
	if !strings.Contains(svc, "ExecStart=/usr/sbin/sshd -e -D") {
		testingT.Fatalf("sshd.service missing -e flag:\n%s", svc)
	}
}

func TestKernelCmdlineClassicLuks(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", true, false)
	cfg.System.KeymapInitramfs = "us"
	ctx, _ := testContext(testingT, cfg, classicSeeds())
	ctx.BlkidUUID = func(dev string) (string, error) {
		return "aaaaaaa1-0000-0000-0000-000000000001", nil
	}

	cmdline, err := installer.KernelCmdline(ctx)
	if err != nil {
		testingT.Fatal(err)
	}
	want := "rd.vconsole.keymap=us rd.luks.uuid=" + uLuksRoot +
		" root=UUID=aaaaaaa1-0000-0000-0000-000000000001"
	if cmdline != want {
		testingT.Fatalf("cmdline = %q, want %q", cmdline, want)
	}
}

func TestKernelCmdlineZFSNoRootUUID(testingT *testing.T) {
	cfg := config.Default(true)
	cfg.Disk.Scheme = config.SchemeZFSCentric
	cfg.Disk.Devices = []string{"/dev/sda"}
	cfg.Disk.UseSwap = false
	cfg.System.KeymapInitramfs = "us"
	ctx, _ := testContext(testingT, cfg, map[string]string{
		"gpt_dev0": uGPT0, "part_efi_dev0": uEfi0, "part_root_dev0": uRoot0, "root_dev1": uRoot1,
	})

	cmdline, err := installer.KernelCmdline(ctx)
	if err != nil {
		testingT.Fatal(err)
	}
	if !strings.HasPrefix(cmdline, "rd.vconsole.keymap=us") {
		testingT.Fatalf("cmdline = %q", cmdline)
	}
	if strings.Contains(cmdline, "root=UUID=") {
		testingT.Fatalf("zfs cmdline must not carry root=UUID: %q", cmdline)
	}
}

func TestBlkidUUIDForIDError(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	ctx, _ := testContext(testingT, cfg, classicSeeds())
	ctx.BlkidUUID = func(string) (string, error) {
		return "", fmt.Errorf("no such device")
	}

	if _, err := installer.BlkidUUIDForID(ctx, ctx.Layout.RootID); err == nil {
		testingT.Fatal("expected error from blkid stub")
	}
}

func TestGenerateFstabClassicLuks(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", true, false)
	ctx, _ := testContext(testingT, cfg, classicSeeds())
	mkScratchDir(testingT, ctx, "/etc")
	ctx.BlkidUUID = func(dev string) (string, error) {
		uuid := map[string]string{
			"/dev/fake-part_luks_root": "r",
			"/dev/fake-part_efi":       "e",
			"/dev/fake-part_swap":      "s",
		}[dev]
		if uuid == "" {
			return "", fmt.Errorf("device %s not expected", dev)
		}
		return "aaaaaaa1-0000-0000-0000-0000000000" + uuid, nil
	}

	if err := installer.GenerateFstab(ctx); err != nil {
		testingT.Fatal(err)
	}
	fstab := readScratch(testingT, ctx, "/etc/fstab")
	for _, want := range []string{
		fstabRow("UUID=aaaaaaa1-0000-0000-0000-0000000000r", "/", "ext4", "defaults,noatime,errors=remount-ro,discard", "0 1"),
		fstabRow("UUID=aaaaaaa1-0000-0000-0000-0000000000e", "/boot/efi", "vfat", "defaults,noatime,fmask=0177,dmask=0077,noexec,nodev,nosuid,discard", "0 2"),
		fstabRow("UUID=aaaaaaa1-0000-0000-0000-0000000000s", "none", "swap", "defaults,discard", "0 0"),
	} {
		if !strings.Contains(fstab, want) {
			testingT.Fatalf("fstab missing row %q:\n%s", want, fstab)
		}
	}
}

func TestGenerateFstabZFS(testingT *testing.T) {
	cfg := config.Default(true)
	cfg.Disk.Scheme = config.SchemeZFSCentric
	cfg.Disk.Devices = []string{"/dev/sda"}
	cfg.Disk.UseSwap = true
	cfg.Disk.UseLuks = false
	ctx, _ := testContext(testingT, cfg, map[string]string{
		"gpt_dev0": uGPT0, "part_efi_dev0": uEfi0, "part_root_dev0": uRoot0, "root_dev1": uRoot1,
	})
	mkScratchDir(testingT, ctx, "/etc")
	ctx.BlkidUUID = func(dev string) (string, error) {
		return "aaaaaaa1-0000-0000-0000-0000000000x", nil
	}

	if err := installer.GenerateFstab(ctx); err != nil {
		testingT.Fatal(err)
	}
	fstab := readScratch(testingT, ctx, "/etc/fstab")
	if strings.Contains(fstab, " /  ") {
		testingT.Fatalf("zfs fstab must not describe a root device:\n%s", fstab)
	}
	// EFI + swap rows are still written for zfs layouts.
	for _, want := range []string{"/boot/efi", "swap"} {
		if !strings.Contains(fstab, want) {
			testingT.Fatalf("fstab missing %q:\n%s", want, fstab)
		}
	}
}
