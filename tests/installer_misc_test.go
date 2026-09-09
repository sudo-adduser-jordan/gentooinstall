// Smaller installer units (programs, paths, mountpoints, runner).
package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gentooinstall/lib/installer"
)

func containsAll(hay []string, needles ...string) bool {
	set := map[string]bool{}
	for _, hayItem := range hay {
		set[hayItem] = true
	}
	for _, needle := range needles {
		if !set[needle] {
			return false
		}
	}
	return true
}

func TestWantedProgramsComposition(testingT *testing.T) {
	base := classicCfg("/dev/sdX", false, false)
	ctx, _ := testContext(testingT, base, nil)
	req, _ := installer.WantedPrograms(ctx)
	if !containsAll(req, "gpg", "sgdisk", "lsblk", "partprobe", "hwclock", "ntpd") {
		testingT.Fatalf("base required = %v", req)
	}
	for _, bad := range []string{"cryptsetup", "mdadm", "zfs", "btrfs"} {
		for _, prog := range req {
			if prog == bad {
				testingT.Fatalf("base required unexpectedly contains %q: %v", bad, req)
			}
		}
	}

	luks := classicCfg("/dev/sdX", true, false)
	lc, _ := testContext(testingT, luks, nil)
	req, _ = installer.WantedPrograms(lc)
	if !containsAll(req, "cryptsetup") {
		testingT.Fatalf("luks required = %v", req)
	}
}

func TestMustExist(testingT *testing.T) {
	dir := testingT.TempDir()
	ok := filepath.Join(dir, "present")
	if err := os.WriteFile(ok, []byte("x"), 0o644); err != nil {
		testingT.Fatal(err)
	}
	if err := installer.MustExist(ok, "file"); err != nil {
		testingT.Fatalf("MustExist present: %v", err)
	}
	if err := installer.MustExist(filepath.Join(dir, "missing"), "file"); err == nil {
		testingT.Fatal("expected error for missing path")
	}
}

func TestBinConfigInBind(testingT *testing.T) {
	if !strings.HasSuffix(installer.BinInBind(), "gentooinstall-self") {
		testingT.Fatalf("BinInBind = %q", installer.BinInBind())
	}
	if !strings.HasSuffix(installer.ConfigInBind(), "config.toml") {
		testingT.Fatalf("ConfigInBind = %q", installer.ConfigInBind())
	}
}

func TestIsMountpointNegative(testingT *testing.T) {
	if installer.IsMountpoint("/definitely/not/a/mountpoint-gentooinstall-xyz") {
		testingT.Fatal("bogus path reported as mountpoint")
	}
}

func TestRunnerHasProgramStub(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	ctx, _ := testContext(testingT, cfg, nil)
	ctx.Runner.LookPath = func(name string) bool { return name == "sgdisk" }
	if !ctx.Runner.HasProgram("sgdisk") {
		testingT.Fatal("stubbed sgdisk must be present")
	}
	if ctx.Runner.HasProgram("no-such-program-xyz") {
		testingT.Fatal("stubbed missing program must be absent")
	}
}

func TestMountByIDSkipAndMount(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	ctx, stub := testContext(testingT, cfg, nil)

	ctx.IsMountpoint = func(string) bool { return true }
	if err := installer.MountByID(ctx, ctx.Layout.RootID, "/mnt/skip"); err != nil {
		testingT.Fatalf("skip mounted: %v", err)
	}
	if len(stub.Calls()) != 0 {
		testingT.Fatalf("mounted path must record no commands, got %v", stub.Lines())
	}

	ctx.IsMountpoint = func(string) bool { return false }
	if err := installer.MountByID(ctx, ctx.Layout.RootID, "/mnt/target"); err != nil {
		testingT.Fatalf("MountByID: %v", err)
	}
	lines := stub.Lines()
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "mount ") {
		testingT.Fatalf("expected one mount call, got %v", lines)
	}
	if !strings.Contains(lines[0], "/mnt/target") {
		testingT.Fatalf("mount target missing: %v", lines)
	}
}
