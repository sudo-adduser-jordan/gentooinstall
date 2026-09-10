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

func TestMissingPrograms(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	ctx, _ := testContext(testingT, cfg, nil)
	// Everything present except ntpd and sgdisk.
	ctx.Runner.LookPath = func(name string) bool { return name != "ntpd" && name != "sgdisk" }

	missing := installer.MissingPrograms(ctx)
	if len(missing) != 2 || missing[0] != "ntpd" || missing[1] != "sgdisk" {
		testingT.Fatalf("MissingPrograms = %v, want [ntpd sgdisk]", missing)
	}
	err := installer.CheckPrograms(ctx)
	if err == nil || !strings.Contains(err.Error(), "missing required programs: ntpd sgdisk") {
		testingT.Fatalf("CheckPrograms error = %v, want missing ntpd sgdisk", err)
	}
}

func TestMissingProgramsNone(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	ctx, _ := testContext(testingT, cfg, nil)
	ctx.Runner.LookPath = func(string) bool { return true }
	if missing := installer.MissingPrograms(ctx); len(missing) != 0 {
		testingT.Fatalf("all programs present, got missing %v", missing)
	}
}

func TestProgramPackagesAndCmdline(testingT *testing.T) {
	pkgs := installer.ProgramPackages([]string{"ntpd", "sgdisk", "lsblk", "gpg", "bogus-tool"})
	want := []string{"ntp", "sgdisk", "util-linux", "gnupg", "bogus-tool"}
	if strings.Join(pkgs, " ") != strings.Join(want, " ") {
		testingT.Fatalf("ProgramPackages = %v, want %v", pkgs, want)
	}
	if cmd := installer.InstallProgramsCmdline([]string{"ntpd", "sgdisk"}); cmd != "apk add --no-cache ntp sgdisk" {
		testingT.Fatalf("InstallProgramsCmdline = %q", cmd)
	}
}

func TestInstallMissingProgramsInstalls(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	ctx, stub := testContext(testingT, cfg, nil)
	// ntpd/sgdisk are missing until the apk install has been recorded.
	ctx.Runner.LookPath = func(name string) bool {
		switch name {
		case "apk":
			return true
		case "ntpd", "sgdisk":
			for _, line := range stub.Lines() {
				if strings.HasPrefix(line, "apk add --no-cache") {
					return true
				}
			}
			return false
		default:
			return true
		}
	}

	if err := installer.InstallMissingPrograms(ctx); err != nil {
		testingT.Fatal(err)
	}
	assertCmds(testingT, stub, "apk add --no-cache ntp sgdisk")
	if missing := installer.MissingPrograms(ctx); len(missing) != 0 {
		testingT.Fatalf("still missing after install: %v", missing)
	}
}

func TestInstallMissingProgramsNoApk(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	ctx, stub := testContext(testingT, cfg, nil)
	ctx.Runner.LookPath = func(name string) bool { return name != "ntpd" && name != "sgdisk" && name != "apk" }

	err := installer.InstallMissingPrograms(ctx)
	if err == nil || !strings.Contains(err.Error(), "no apk") {
		testingT.Fatalf("expected no-apk error, got %v", err)
	}
	if len(stub.Lines()) != 0 {
		testingT.Fatalf("no commands must run without apk, got %v", stub.Lines())
	}
}

func TestInstallMissingProgramsStillMissing(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	ctx, stub := testContext(testingT, cfg, nil)
	// apk succeeds but ntpd/sgdisk stay absent: the follow-up check fails.
	ctx.Runner.LookPath = func(name string) bool {
		switch name {
		case "apk", "gpg", "hwclock", "lsblk", "partprobe":
			return true
		default:
			return false
		}
	}

	err := installer.InstallMissingPrograms(ctx)
	if err == nil || !strings.Contains(err.Error(), "still missing required programs") {
		testingT.Fatalf("expected still-missing error, got %v", err)
	}
	assertCmds(testingT, stub, "apk add --no-cache ntp sgdisk")
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
