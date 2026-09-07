// Smaller installer units (programs, paths, mountpoints, runner).
package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gentooinstall/internal/installer"
)

func containsAll(hay []string, needles ...string) bool {
	set := map[string]bool{}
	for _, h := range hay {
		set[h] = true
	}
	for _, n := range needles {
		if !set[n] {
			return false
		}
	}
	return true
}

func TestWantedProgramsComposition(t *testing.T) {
	base := classicCfg("/dev/sdX", false, false)
	c, _ := testContext(t, base, nil)
	req, _ := installer.WantedPrograms(c)
	if !containsAll(req, "gpg", "sgdisk", "lsblk", "partprobe", "hwclock", "ntpd") {
		t.Fatalf("base required = %v", req)
	}
	for _, bad := range []string{"cryptsetup", "mdadm", "zfs", "btrfs"} {
		for _, p := range req {
			if p == bad {
				t.Fatalf("base required unexpectedly contains %q: %v", bad, req)
			}
		}
	}

	luks := classicCfg("/dev/sdX", true, false)
	lc, _ := testContext(t, luks, nil)
	req, _ = installer.WantedPrograms(lc)
	if !containsAll(req, "cryptsetup") {
		t.Fatalf("luks required = %v", req)
	}
}

func TestMustExist(t *testing.T) {
	dir := t.TempDir()
	ok := filepath.Join(dir, "present")
	if err := os.WriteFile(ok, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := installer.MustExist(ok, "file"); err != nil {
		t.Fatalf("MustExist present: %v", err)
	}
	if err := installer.MustExist(filepath.Join(dir, "missing"), "file"); err == nil {
		t.Fatal("expected error for missing path")
	}
}

func TestBinConfigInBind(t *testing.T) {
	if !strings.HasSuffix(installer.BinInBind(), "gentooinstall-self") {
		t.Fatalf("BinInBind = %q", installer.BinInBind())
	}
	if !strings.HasSuffix(installer.ConfigInBind(), "config.toml") {
		t.Fatalf("ConfigInBind = %q", installer.ConfigInBind())
	}
}

func TestIsMountpointNegative(t *testing.T) {
	if installer.IsMountpoint("/definitely/not/a/mountpoint-gentooinstall-xyz") {
		t.Fatal("bogus path reported as mountpoint")
	}
}

func TestRunnerHasProgramStub(t *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	c, _ := testContext(t, cfg, nil)
	c.R.LookPath = func(s string) bool { return s == "sgdisk" }
	if !c.R.HasProgram("sgdisk") {
		t.Fatal("stubbed sgdisk must be present")
	}
	if c.R.HasProgram("no-such-program-xyz") {
		t.Fatal("stubbed missing program must be absent")
	}
}

func TestMountByIDSkipAndMount(t *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	c, stub := testContext(t, cfg, nil)

	c.IsMountpoint = func(string) bool { return true }
	if err := installer.MountByID(c, c.Layout.RootID, "/mnt/skip"); err != nil {
		t.Fatalf("skip mounted: %v", err)
	}
	if len(stub.Calls()) != 0 {
		t.Fatalf("mounted path must record no commands, got %v", stub.Lines())
	}

	c.IsMountpoint = func(string) bool { return false }
	if err := installer.MountByID(c, c.Layout.RootID, "/mnt/target"); err != nil {
		t.Fatalf("MountByID: %v", err)
	}
	lines := stub.Lines()
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "mount ") {
		t.Fatalf("expected one mount call, got %v", lines)
	}
	if !strings.Contains(lines[0], "/mnt/target") {
		t.Fatalf("mount target missing: %v", lines)
	}
}
