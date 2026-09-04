package tests

import (
	"fmt"
	"strings"
	"testing"

	"gentooinstall/internal/config"
	"gentooinstall/internal/installer"
)

func TestFindNewestKernel(t *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	c, _ := testContext(t, cfg, nil)
	mkScratchDir(t, c, "/boot")
	for _, k := range []string{"vmlinuz-6.1.0", "vmlinuz-6.8.2", "vmlinuz-6.8.11"} {
		writeScratch(t, c, "/boot/"+k, "")
	}

	got, err := installer.FindNewestKernel(c)
	if err != nil {
		t.Fatal(err)
	}
	if got != "vmlinuz-6.8.11" {
		t.Fatalf("FindNewestKernel = %q, want vmlinuz-6.8.11", got)
	}
}

func TestFindNewestKernelMissing(t *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	c, _ := testContext(t, cfg, nil)
	mkScratchDir(t, c, "/boot")

	if _, err := installer.FindNewestKernel(c); err == nil {
		t.Fatal("expected error for empty /boot")
	}
}

func TestGenerateInitramfsLuks(t *testing.T) {
	cfg := classicCfg("/dev/sdX", true, false)
	c, s := testContext(t, cfg, nil)
	symlinkScratch(t, c, "linux-6.6.13-gentoo", "/usr/src/linux")
	mkScratchDir(t, c, "/boot/efi")

	if err := installer.GenerateInitramfs(c, "/boot/efi/initramfs.img"); err != nil {
		t.Fatal(err)
	}
	assertCmdContains(t, s, []string{
		"dracut --kver 6.6.13-gentoo --zstd --no-hostonly --ro-mnt --add bash crypt crypt-gpg --force /boot/efi/initramfs.img",
	})
	helper := readScratch(t, c, "/boot/efi/generate_initramfs.sh")
	for _, want := range []string{"--add", "\"bash crypt crypt-gpg\"", "/boot/efi/initramfs.img"} {
		if !strings.Contains(helper, want) {
			t.Fatalf("helper missing %q:\n%s", want, helper)
		}
	}
}

func TestGenerateInitramfsSSHD(t *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.System.InitramfsSSHD = true
	c, s := testContext(t, cfg, nil)
	symlinkScratch(t, c, "linux-6.6.13-gentoo", "/usr/src/linux")
	mkScratchDir(t, c, "/boot/efi")
	writeScratch(t, c,
		"/usr/lib/dracut/modules.d/46sshd/sshd.service",
		"[Service]\nType=notify\nExecStart=/usr/sbin/sshd -D\n")

	if err := installer.GenerateInitramfs(c, "/boot/efi/initramfs.img"); err != nil {
		t.Fatal(err)
	}
	assertCmdContains(t, s, []string{
		"git clone https://github.com/gsauthof/dracut-sshd",
		"cp -r /tmp/dracut-sshd/46sshd /usr/lib/dracut/modules.d",
		"dracut --kver 6.6.13-gentoo --zstd --no-hostonly --ro-mnt --add bash systemd-networkd --install /etc/systemd/network/20-wired.network --force /boot/efi/initramfs.img",
	})
	svc := readScratch(t, c, "/usr/lib/dracut/modules.d/46sshd/sshd.service")
	if strings.Contains(svc, "Type=notify") || !strings.Contains(svc, "Type=simple") {
		t.Fatalf("sshd.service not fixed:\n%s", svc)
	}
	if !strings.Contains(svc, "ExecStart=/usr/sbin/sshd -e -D") {
		t.Fatalf("sshd.service missing -e flag:\n%s", svc)
	}
}

func TestKernelCmdlineClassicLuks(t *testing.T) {
	cfg := classicCfg("/dev/sdX", true, false)
	cfg.System.KeymapInitramfs = "us"
	c, _ := testContext(t, cfg, classicSeeds())
	c.BlkidUUID = func(dev string) (string, error) {
		return "aaaaaaa1-0000-0000-0000-000000000001", nil
	}

	cmdline, err := installer.KernelCmdline(c)
	if err != nil {
		t.Fatal(err)
	}
	want := "rd.vconsole.keymap=us rd.luks.uuid=" + uLuksRoot +
		" root=UUID=aaaaaaa1-0000-0000-0000-000000000001"
	if cmdline != want {
		t.Fatalf("cmdline = %q, want %q", cmdline, want)
	}
}

func TestKernelCmdlineZFSNoRootUUID(t *testing.T) {
	cfg := config.Default(true)
	cfg.Disk.Scheme = config.SchemeZFSCentric
	cfg.Disk.Devices = []string{"/dev/sda"}
	cfg.Disk.UseSwap = false
	cfg.System.KeymapInitramfs = "us"
	c, _ := testContext(t, cfg, map[string]string{
		"gpt_dev0": uGPT0, "part_efi_dev0": uEfi0, "part_root_dev0": uRoot0, "root_dev1": uRoot1,
	})

	cmdline, err := installer.KernelCmdline(c)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(cmdline, "rd.vconsole.keymap=us") {
		t.Fatalf("cmdline = %q", cmdline)
	}
	if strings.Contains(cmdline, "root=UUID=") {
		t.Fatalf("zfs cmdline must not carry root=UUID: %q", cmdline)
	}
}

func TestBlkidUUIDForIDError(t *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	c, _ := testContext(t, cfg, classicSeeds())
	c.BlkidUUID = func(string) (string, error) {
		return "", fmt.Errorf("no such device")
	}

	if _, err := installer.BlkidUUIDForID(c, c.Layout.RootID); err == nil {
		t.Fatal("expected error from blkid stub")
	}
}

func TestGenerateFstabClassicLuks(t *testing.T) {
	cfg := classicCfg("/dev/sdX", true, false)
	c, _ := testContext(t, cfg, classicSeeds())
	mkScratchDir(t, c, "/etc")
	c.BlkidUUID = func(dev string) (string, error) {
		u := map[string]string{
			"/dev/fake-part_luks_root": "r",
			"/dev/fake-part_efi":       "e",
			"/dev/fake-part_swap":      "s",
		}[dev]
		if u == "" {
			return "", fmt.Errorf("device %s not expected", dev)
		}
		return "aaaaaaa1-0000-0000-0000-0000000000" + u, nil
	}

	if err := installer.GenerateFstab(c); err != nil {
		t.Fatal(err)
	}
	fstab := readScratch(t, c, "/etc/fstab")
	for _, want := range []string{
		fstabRow("UUID=aaaaaaa1-0000-0000-0000-0000000000r", "/", "ext4", "defaults,noatime,errors=remount-ro,discard", "0 1"),
		fstabRow("UUID=aaaaaaa1-0000-0000-0000-0000000000e", "/boot/efi", "vfat", "defaults,noatime,fmask=0177,dmask=0077,noexec,nodev,nosuid,discard", "0 2"),
		fstabRow("UUID=aaaaaaa1-0000-0000-0000-0000000000s", "none", "swap", "defaults,discard", "0 0"),
	} {
		if !strings.Contains(fstab, want) {
			t.Fatalf("fstab missing row %q:\n%s", want, fstab)
		}
	}
}

func TestGenerateFstabZFS(t *testing.T) {
	cfg := config.Default(true)
	cfg.Disk.Scheme = config.SchemeZFSCentric
	cfg.Disk.Devices = []string{"/dev/sda"}
	cfg.Disk.UseSwap = true
	cfg.Disk.UseLuks = false
	c, _ := testContext(t, cfg, map[string]string{
		"gpt_dev0": uGPT0, "part_efi_dev0": uEfi0, "part_root_dev0": uRoot0, "root_dev1": uRoot1,
	})
	mkScratchDir(t, c, "/etc")
	c.BlkidUUID = func(dev string) (string, error) {
		return "aaaaaaa1-0000-0000-0000-0000000000x", nil
	}

	if err := installer.GenerateFstab(c); err != nil {
		t.Fatal(err)
	}
	fstab := readScratch(t, c, "/etc/fstab")
	if strings.Contains(fstab, " /  ") {
		t.Fatalf("zfs fstab must not describe a root device:\n%s", fstab)
	}
	// EFI + swap rows are still written for zfs layouts.
	for _, want := range []string{"/boot/efi", "swap"} {
		if !strings.Contains(fstab, want) {
			t.Fatalf("fstab missing %q:\n%s", want, fstab)
		}
	}
}
