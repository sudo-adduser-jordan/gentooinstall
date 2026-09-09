// EFI firmware checks and efivarfs mounting.
package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gentooinstall/lib/installer"
)

// nonEFIHost makes the context report a host that was not booted under UEFI.
func nonEFIHost(c *installer.Context) {
	c.Stat = func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
}

// efiMountLine is the exact efivarfs mount invocation recorded by the stub.
const efiMountLine = "mount -t efivarfs efivarfs /sys/firmware/efi/efivars"

func TestMountEfiVarsHostNotUEFI(t *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	c, s := testContext(t, cfg, classicSeeds())
	nonEFIHost(c)

	err := installer.MountEfiVars(c)
	if err == nil {
		t.Fatal("expected error on a non-UEFI host, got nil")
	}
	for _, want := range []string{"efivarfs", "UEFI", "disk.boot_type", "bios"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error should mention %q: %v", want, err)
		}
	}
	if got := s.Lines(); len(got) != 0 {
		t.Fatalf("no commands should run on a non-UEFI host, got %q", got)
	}
}

func TestMountEfiVarsCreatesMountpointBeforeMount(t *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	c, s := testContext(t, cfg, classicSeeds())

	if err := installer.MountEfiVars(c); err != nil {
		t.Fatal(err)
	}
	if got := s.Lines(); !strings.Contains(strings.Join(got, "\n"), efiMountLine) {
		t.Fatalf("expected efivarfs mount, got %q", got)
	}
	mp := filepath.Join(c.Root, "sys", "firmware", "efi", "efivars")
	if _, err := os.Stat(mp); err != nil {
		t.Fatalf("efivars mountpoint was not created: %v", err)
	}
}

func TestMountEfiVarsAlreadyMountedIsNoop(t *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	c, s := testContext(t, cfg, classicSeeds())
	c.IsMountpoint = func(p string) bool { return p == "/sys/firmware/efi/efivars" }

	if err := installer.MountEfiVars(c); err != nil {
		t.Fatal(err)
	}
	if got := s.Lines(); len(got) != 0 {
		t.Fatalf("already-mounted efivars should skip every command, got %q", got)
	}
}

func TestCheckHostBootMode(t *testing.T) {
	efiCfg := classicCfg("/dev/sdX", false, false)
	efiLayoutCtx, _ := testContext(t, efiCfg, classicSeeds())

	biosCfg := classicCfg("/dev/sdX", false, false)
	biosCfg.Disk.BootType = "bios"
	biosLayoutCtx, _ := testContext(t, biosCfg,
		map[string]string{"gpt": uGpt, "part_bios": uEfi, "part_root": uRoot})

	if err := installer.CheckHostBootMode(efiLayoutCtx); err != nil {
		t.Fatalf("EFI layout on an EFI host should pass, got %v", err)
	}
	if err := installer.CheckHostBootMode(biosLayoutCtx); err != nil {
		t.Fatalf("BIOS layout should always pass, got %v", err)
	}

	nonEFIHost(efiLayoutCtx)
	err := installer.CheckHostBootMode(efiLayoutCtx)
	if err == nil {
		t.Fatal("expected mismatch error for EFI layout on a non-UEFI host")
	}
	for _, want := range []string{"EFI", "UEFI", "disk.boot_type"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error should mention %q: %v", want, err)
		}
	}
	if err := installer.CheckHostBootMode(biosLayoutCtx); err != nil {
		t.Fatalf("BIOS layout should pass even on a non-UEFI host, got %v", err)
	}
}

func TestCheckFilesystemSupport(t *testing.T) {
	withVfat := func(c *installer.Context) {
		c.Filesystems = func(string) bool { return true }
	}
	withoutVfat := func(c *installer.Context) {
		c.Filesystems = func(string) bool { return false }
	}

	efiCfg := classicCfg("/dev/sdX", false, false)
	efiCtx, _ := testContext(t, efiCfg, classicSeeds())
	withVfat(efiCtx)
	if err := installer.CheckFilesystemSupport(efiCtx); err != nil {
		t.Fatalf("EFI layout with vfat should pass, got %v", err)
	}

	biosCfg := classicCfg("/dev/sdX", false, false)
	biosCfg.Disk.BootType = "bios"
	biosCtx, _ := testContext(t, biosCfg,
		map[string]string{"gpt": uGpt, "part_bios": uEfi, "part_root": uRoot})
	withVfat(biosCtx)
	if err := installer.CheckFilesystemSupport(biosCtx); err != nil {
		t.Fatalf("BIOS layout with vfat should pass, got %v", err)
	}

	withoutVfat(efiCtx)
	if err := installer.CheckFilesystemSupport(efiCtx); err == nil {
		t.Fatal("EFI layout without vfat should fail before partitioning")
	} else {
		for _, want := range []string{"vfat", "/boot/efi", "rebuild"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error should mention %q: %v", want, err)
			}
		}
	}

	withoutVfat(biosCtx)
	if err := installer.CheckFilesystemSupport(biosCtx); err == nil {
		t.Fatal("BIOS layout without vfat should fail before partitioning")
	} else {
		for _, want := range []string{"vfat", "/boot/bios", "retrying"} {
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(want)) {
				t.Fatalf("error should mention %q: %v", want, err)
			}
		}
	}
}
