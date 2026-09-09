// Chroot environment preparation and verification (PrepareChrootEnv,
// EnsureDevSymlinks, CheckChrootEnv, MountByID, UnmountChroot).
package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gentooinstall/lib/installer"
)

func requireLsblk(testingT *testing.T) {
	testingT.Helper()
	if _, err := exec.LookPath("lsblk"); err != nil {
		testingT.Skip("lsblk not found; PrepareChrootEnv needs it for CacheLsblkOutput")
	}
}

// fakeMountedChroot simulates a chroot whose virtual filesystems are already
// mounted: the mount commands are stubbed, but the entries the verification
// step inspects (proc/self, dev) exist on disk.
func fakeMountedChroot(testingT *testing.T) string {
	testingT.Helper()
	dir := testingT.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "proc", "self"), 0o755); err != nil {
		testingT.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "dev"), 0o755); err != nil {
		testingT.Fatal(err)
	}
	return dir
}

func TestPrepareChrootEnvMountSequence(testingT *testing.T) {
	requireLsblk(testingT)
	cfg := classicCfg("/dev/sdX", false, false)
	ctx, stub := testContext(testingT, cfg, nil)
	dir := fakeMountedChroot(testingT)

	if err := installer.PrepareChrootEnv(ctx, dir); err != nil {
		testingT.Fatalf("PrepareChrootEnv: %v", err)
	}
	join := filepath.Join
	assertCmds(testingT, stub,
		"mount -t proc /proc "+join(dir, "proc"),
		"mount --rbind /run "+join(dir, "run"),
		"mount --make-rslave "+join(dir, "run"),
		"mount --rbind /tmp "+join(dir, "tmp"),
		"mount --make-rslave "+join(dir, "tmp"),
		"mount --rbind /sys "+join(dir, "sys"),
		"mount --make-rslave "+join(dir, "sys"),
		"mount --rbind /dev "+join(dir, "dev"),
		"mount --make-rslave "+join(dir, "dev"),
		"mount -t devpts devpts -o gid=5,mode=620 "+join(dir, "dev", "pts"),
	)

	if _, err := os.Stat(filepath.Join(dir, "etc", "resolv.conf")); err != nil {
		testingT.Fatalf("resolv.conf not copied: %v", err)
	}
	for name, want := range map[string]string{
		"fd":     "/proc/self/fd",
		"stdin":  "/proc/self/fd/0",
		"stdout": "/proc/self/fd/1",
		"stderr": "/proc/self/fd/2",
	} {
		got, err := os.Readlink(filepath.Join(dir, "dev", name))
		if err != nil {
			testingT.Fatalf("dev/%s missing: %v", name, err)
		}
		if got != want {
			testingT.Fatalf("dev/%s -> %q, want %q", name, got, want)
		}
	}
}

// TestPrepareChrootEnvCreatesFdSymlinks is the regression test for the live
// ISO failure: devtmpfs-only /dev without udev has no fd symlinks, and the
// bare --rbind would propagate that gap. The stubbed rbind binds nothing,
// so the symlinks must come from PrepareChrootEnv itself.
func TestPrepareChrootEnvCreatesFdSymlinks(testingT *testing.T) {
	requireLsblk(testingT)
	cfg := classicCfg("/dev/sdX", false, false)
	ctx, _ := testContext(testingT, cfg, nil)
	dir := fakeMountedChroot(testingT)

	if _, err := os.Lstat(filepath.Join(dir, "dev", "fd")); err == nil {
		testingT.Fatal("precondition: dev/fd must not exist before prepare")
	}
	if err := installer.PrepareChrootEnv(ctx, dir); err != nil {
		testingT.Fatalf("PrepareChrootEnv: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "dev", "fd")); err != nil {
		testingT.Fatalf("dev/fd not created: %v", err)
	}
}

func TestPrepareChrootEnvFailsFastWithoutProc(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	ctx, stub := testContext(testingT, cfg, nil)
	dir := testingT.TempDir() // no proc/self: mounts cannot have succeeded

	err := installer.PrepareChrootEnv(ctx, dir)
	if err == nil {
		testingT.Fatal("expected error for chroot without /proc")
	}
	if !strings.Contains(err.Error(), "/proc") {
		testingT.Fatalf("error should mention /proc, got: %v", err)
	}
	if len(stub.Calls()) == 0 {
		testingT.Fatal("mounts should have been attempted before verification failed")
	}
	// The fd symlinks are still created so a retry after mounting /proc
	// finds a complete /dev.
	if _, err := os.Lstat(filepath.Join(dir, "dev", "fd")); err != nil {
		testingT.Fatalf("dev/fd should exist even on verification failure: %v", err)
	}
}

func TestPrepareChrootEnvSkipsMounted(testingT *testing.T) {
	requireLsblk(testingT)
	cfg := classicCfg("/dev/sdX", false, false)
	ctx, stub := testContext(testingT, cfg, nil)
	ctx.IsMountpoint = func(string) bool { return true }
	dir := fakeMountedChroot(testingT)
	if err := installer.EnsureDevSymlinks(dir); err != nil {
		testingT.Fatal(err)
	}

	if err := installer.PrepareChrootEnv(ctx, dir); err != nil {
		testingT.Fatalf("PrepareChrootEnv: %v", err)
	}
	if len(stub.Calls()) != 0 {
		testingT.Fatalf("mounted paths must record no commands, got %v", stub.Lines())
	}
}

func TestEnsureDevSymlinks(testingT *testing.T) {
	dir := testingT.TempDir()
	if err := installer.EnsureDevSymlinks(dir); err != nil {
		testingT.Fatal(err)
	}
	for name, want := range map[string]string{
		"fd": "/proc/self/fd", "stdin": "/proc/self/fd/0",
		"stdout": "/proc/self/fd/1", "stderr": "/proc/self/fd/2",
	} {
		got, err := os.Readlink(filepath.Join(dir, "dev", name))
		if err != nil || got != want {
			testingT.Fatalf("dev/%s -> %q, %v; want %q", name, got, err, want)
		}
	}
	// Idempotent and never clobbers existing entries.
	keep := filepath.Join(dir, "dev", "stdout")
	if err := os.Remove(keep); err != nil {
		testingT.Fatal(err)
	}
	if err := os.WriteFile(keep, []byte("real"), 0o644); err != nil {
		testingT.Fatal(err)
	}
	if err := installer.EnsureDevSymlinks(dir); err != nil {
		testingT.Fatal(err)
	}
	if data, _ := os.ReadFile(keep); string(data) != "real" {
		testingT.Fatal("existing dev entry was clobbered")
	}
}

func TestCheckChrootEnv(testingT *testing.T) {
	if _, err := os.Stat("/proc/self"); err != nil {
		testingT.Skip("host /proc/self missing; cannot validate fd resolution")
	}
	dir := testingT.TempDir()
	if err := installer.CheckChrootEnv(dir); err == nil ||
		!strings.Contains(err.Error(), "/proc") {
		testingT.Fatalf("empty dir should fail on /proc, got: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "proc", "self"), 0o755); err != nil {
		testingT.Fatal(err)
	}
	if err := installer.CheckChrootEnv(dir); err == nil ||
		!strings.Contains(err.Error(), "/dev/fd") {
		testingT.Fatalf("missing dev/fd should fail, got: %v", err)
	}
	if err := installer.EnsureDevSymlinks(dir); err != nil {
		testingT.Fatal(err)
	}
	if err := installer.CheckChrootEnv(dir); err != nil {
		testingT.Fatalf("complete env should pass: %v", err)
	}
}

func TestMountSourceNegative(testingT *testing.T) {
	if got := installer.MountSource("/definitely/not/a/mountpoint-gentooinstall-xyz"); got != "" {
		testingT.Fatalf("MountSource = %q, want empty", got)
	}
}

func TestMountByIDRefusesStaleMount(testingT *testing.T) {
	if installer.MountSource("/proc") == "" {
		testingT.Skip("/proc not mounted; cannot test stale-mount refusal")
	}
	cfg := classicCfg("/dev/sdX", false, false)
	ctx, _ := testContext(testingT, cfg, nil)
	ctx.IsMountpoint = func(string) bool { return true }
	// /proc is really mounted (from "proc"), which cannot be the layout's
	// root device: reuse must be refused, not silently accepted.
	err := installer.MountByID(ctx, ctx.Layout.RootID, "/proc")
	if err == nil || !strings.Contains(err.Error(), "stale") {
		testingT.Fatalf("expected stale-mount error, got: %v", err)
	}
}

func TestUnmountChroot(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	ctx, stub := testContext(testingT, cfg, nil)
	dir := filepath.Join(testingT.TempDir(), "root")

	ctx.IsMountpoint = func(string) bool { return false }
	if err := installer.UnmountChroot(ctx, dir); err != nil {
		testingT.Fatalf("UnmountChroot empty: %v", err)
	}
	if len(stub.Calls()) != 0 {
		testingT.Fatalf("nothing mounted, want no commands, got %v", stub.Lines())
	}

	ctx.IsMountpoint = func(string) bool { return true }
	if err := installer.UnmountChroot(ctx, dir); err != nil {
		testingT.Fatalf("UnmountChroot: %v", err)
	}
	join := filepath.Join
	assertCmds(testingT, stub,
		"umount -l "+join(dir, "dev", "pts"),
		"umount -l "+join(dir, "dev"),
		"umount -l "+join(dir, "sys"),
		"umount -l "+join(dir, "tmp"),
		"umount -l "+join(dir, "run"),
		"umount -l "+join(dir, "proc"),
		"umount -l "+dir,
	)
}
