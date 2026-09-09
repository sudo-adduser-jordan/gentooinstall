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
func nonEFIHost(ctx *installer.Context) {
	ctx.Stat = func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
}

// efiMountLine is the exact efivarfs mount invocation recorded by the stub.
const efiMountLine = "mount -t efivarfs efivarfs /sys/firmware/efi/efivars"

func TestMountEfiVarsHostNotUEFI(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	ctx, stub := testContext(testingT, cfg, classicSeeds())
	nonEFIHost(ctx)

	err := installer.MountEfiVars(ctx)
	if err == nil {
		testingT.Fatal("expected error on a non-UEFI host, got nil")
	}
	for _, want := range []string{"efivarfs", "UEFI", "disk.boot_type", "bios"} {
		if !strings.Contains(err.Error(), want) {
			testingT.Fatalf("error should mention %q: %v", want, err)
		}
	}
	if got := stub.Lines(); len(got) != 0 {
		testingT.Fatalf("no commands should run on a non-UEFI host, got %q", got)
	}
}

func TestMountEfiVarsCreatesMountpointBeforeMount(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	ctx, stub := testContext(testingT, cfg, classicSeeds())

	if err := installer.MountEfiVars(ctx); err != nil {
		testingT.Fatal(err)
	}
	if got := stub.Lines(); !strings.Contains(strings.Join(got, "\n"), efiMountLine) {
		testingT.Fatalf("expected efivarfs mount, got %q", got)
	}
	mp := filepath.Join(ctx.Root, "sys", "firmware", "efi", "efivars")
	if _, err := os.Stat(mp); err != nil {
		testingT.Fatalf("efivars mountpoint was not created: %v", err)
	}
}

func TestMountEfiVarsAlreadyMountedIsNoop(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	ctx, stub := testContext(testingT, cfg, classicSeeds())
	ctx.IsMountpoint = func(path string) bool { return path == "/sys/firmware/efi/efivars" }

	if err := installer.MountEfiVars(ctx); err != nil {
		testingT.Fatal(err)
	}
	if got := stub.Lines(); len(got) != 0 {
		testingT.Fatalf("already-mounted efivars should skip every command, got %q", got)
	}
}

func TestCheckHostBootMode(testingT *testing.T) {
	efiCfg := classicCfg("/dev/sdX", false, false)
	efiLayoutCtx, _ := testContext(testingT, efiCfg, classicSeeds())

	biosCfg := classicCfg("/dev/sdX", false, false)
	biosCfg.Disk.BootType = "bios"
	biosLayoutCtx, _ := testContext(testingT, biosCfg,
		map[string]string{"gpt": uGpt, "part_bios": uEfi, "part_root": uRoot})

	if err := installer.CheckHostBootMode(efiLayoutCtx); err != nil {
		testingT.Fatalf("EFI layout on an EFI host should pass, got %v", err)
	}
	if err := installer.CheckHostBootMode(biosLayoutCtx); err != nil {
		testingT.Fatalf("BIOS layout should always pass, got %v", err)
	}

	nonEFIHost(efiLayoutCtx)
	err := installer.CheckHostBootMode(efiLayoutCtx)
	if err == nil {
		testingT.Fatal("expected mismatch error for EFI layout on a non-UEFI host")
	}
	for _, want := range []string{"EFI", "UEFI", "disk.boot_type"} {
		if !strings.Contains(err.Error(), want) {
			testingT.Fatalf("error should mention %q: %v", want, err)
		}
	}
	if err := installer.CheckHostBootMode(biosLayoutCtx); err != nil {
		testingT.Fatalf("BIOS layout should pass even on a non-UEFI host, got %v", err)
	}
}

func TestCheckFilesystemSupport(testingT *testing.T) {
	withVfat := func(ctx *installer.Context) {
		ctx.Filesystems = func(string) bool { return true }
	}
	withoutVfat := func(ctx *installer.Context) {
		ctx.Filesystems = func(string) bool { return false }
	}

	efiCfg := classicCfg("/dev/sdX", false, false)
	efiCtx, _ := testContext(testingT, efiCfg, classicSeeds())
	withVfat(efiCtx)
	if err := installer.CheckFilesystemSupport(efiCtx); err != nil {
		testingT.Fatalf("EFI layout with vfat should pass, got %v", err)
	}

	biosCfg := classicCfg("/dev/sdX", false, false)
	biosCfg.Disk.BootType = "bios"
	biosCtx, _ := testContext(testingT, biosCfg,
		map[string]string{"gpt": uGpt, "part_bios": uEfi, "part_root": uRoot})
	withVfat(biosCtx)
	if err := installer.CheckFilesystemSupport(biosCtx); err != nil {
		testingT.Fatalf("BIOS layout with vfat should pass, got %v", err)
	}

	withoutVfat(efiCtx)
	if err := installer.CheckFilesystemSupport(efiCtx); err == nil {
		testingT.Fatal("EFI layout without vfat should fail before partitioning")
	} else {
		for _, want := range []string{"vfat", "/boot/efi", "rebuild"} {
			if !strings.Contains(err.Error(), want) {
				testingT.Fatalf("error should mention %q: %v", want, err)
			}
		}
	}

	withoutVfat(biosCtx)
	if err := installer.CheckFilesystemSupport(biosCtx); err == nil {
		testingT.Fatal("BIOS layout without vfat should fail before partitioning")
	} else {
		for _, want := range []string{"vfat", "/boot/bios", "retrying"} {
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(want)) {
				testingT.Fatalf("error should mention %q: %v", want, err)
			}
		}
	}
}
