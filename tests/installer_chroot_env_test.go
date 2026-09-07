package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gentooinstall/internal/installer"
)

func requireLsblk(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("lsblk"); err != nil {
		t.Skip("lsblk not found; PrepareChrootEnv needs it for CacheLsblkOutput")
	}
}

// fakeMountedChroot simulates a chroot whose virtual filesystems are already
// mounted: the mount commands are stubbed, but the entries the verification
// step inspects (proc/self, dev) exist on disk.
func fakeMountedChroot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "proc", "self"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "dev"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPrepareChrootEnvMountSequence(t *testing.T) {
	requireLsblk(t)
	cfg := classicCfg("/dev/sdX", false, false)
	c, stub := testContext(t, cfg, nil)
	dir := fakeMountedChroot(t)

	if err := installer.PrepareChrootEnv(c, dir); err != nil {
		t.Fatalf("PrepareChrootEnv: %v", err)
	}
	j := filepath.Join
	assertCmds(t, stub,
		"mount -t proc /proc "+j(dir, "proc"),
		"mount --rbind /run "+j(dir, "run"),
		"mount --make-rslave "+j(dir, "run"),
		"mount --rbind /tmp "+j(dir, "tmp"),
		"mount --make-rslave "+j(dir, "tmp"),
		"mount --rbind /sys "+j(dir, "sys"),
		"mount --make-rslave "+j(dir, "sys"),
		"mount --rbind /dev "+j(dir, "dev"),
		"mount --make-rslave "+j(dir, "dev"),
		"mount -t devpts devpts -o gid=5,mode=620 "+j(dir, "dev", "pts"),
	)

	if _, err := os.Stat(filepath.Join(dir, "etc", "resolv.conf")); err != nil {
		t.Fatalf("resolv.conf not copied: %v", err)
	}
	for name, want := range map[string]string{
		"fd":     "/proc/self/fd",
		"stdin":  "/proc/self/fd/0",
		"stdout": "/proc/self/fd/1",
		"stderr": "/proc/self/fd/2",
	} {
		got, err := os.Readlink(filepath.Join(dir, "dev", name))
		if err != nil {
			t.Fatalf("dev/%s missing: %v", name, err)
		}
		if got != want {
			t.Fatalf("dev/%s -> %q, want %q", name, got, want)
		}
	}
}

// TestPrepareChrootEnvCreatesFdSymlinks is the regression test for the live
// ISO failure: devtmpfs-only /dev without udev has no fd symlinks, and the
// bare --rbind would propagate that gap. The stubbed rbind binds nothing,
// so the symlinks must come from PrepareChrootEnv itself.
func TestPrepareChrootEnvCreatesFdSymlinks(t *testing.T) {
	requireLsblk(t)
	cfg := classicCfg("/dev/sdX", false, false)
	c, _ := testContext(t, cfg, nil)
	dir := fakeMountedChroot(t)

	if _, err := os.Lstat(filepath.Join(dir, "dev", "fd")); err == nil {
		t.Fatal("precondition: dev/fd must not exist before prepare")
	}
	if err := installer.PrepareChrootEnv(c, dir); err != nil {
		t.Fatalf("PrepareChrootEnv: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "dev", "fd")); err != nil {
		t.Fatalf("dev/fd not created: %v", err)
	}
}

func TestPrepareChrootEnvFailsFastWithoutProc(t *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	c, stub := testContext(t, cfg, nil)
	dir := t.TempDir() // no proc/self: mounts cannot have succeeded

	err := installer.PrepareChrootEnv(c, dir)
	if err == nil {
		t.Fatal("expected error for chroot without /proc")
	}
	if !strings.Contains(err.Error(), "/proc") {
		t.Fatalf("error should mention /proc, got: %v", err)
	}
	if len(stub.Calls()) == 0 {
		t.Fatal("mounts should have been attempted before verification failed")
	}
	// The fd symlinks are still created so a retry after mounting /proc
	// finds a complete /dev.
	if _, err := os.Lstat(filepath.Join(dir, "dev", "fd")); err != nil {
		t.Fatalf("dev/fd should exist even on verification failure: %v", err)
	}
}

func TestPrepareChrootEnvSkipsMounted(t *testing.T) {
	requireLsblk(t)
	cfg := classicCfg("/dev/sdX", false, false)
	c, stub := testContext(t, cfg, nil)
	c.IsMountpoint = func(string) bool { return true }
	dir := fakeMountedChroot(t)
	if err := installer.EnsureDevSymlinks(dir); err != nil {
		t.Fatal(err)
	}

	if err := installer.PrepareChrootEnv(c, dir); err != nil {
		t.Fatalf("PrepareChrootEnv: %v", err)
	}
	if len(stub.Calls()) != 0 {
		t.Fatalf("mounted paths must record no commands, got %v", stub.Lines())
	}
}

func TestEnsureDevSymlinks(t *testing.T) {
	dir := t.TempDir()
	if err := installer.EnsureDevSymlinks(dir); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"fd": "/proc/self/fd", "stdin": "/proc/self/fd/0",
		"stdout": "/proc/self/fd/1", "stderr": "/proc/self/fd/2",
	} {
		got, err := os.Readlink(filepath.Join(dir, "dev", name))
		if err != nil || got != want {
			t.Fatalf("dev/%s -> %q, %v; want %q", name, got, err, want)
		}
	}
	// Idempotent and never clobbers existing entries.
	keep := filepath.Join(dir, "dev", "stdout")
	if err := os.Remove(keep); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keep, []byte("real"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := installer.EnsureDevSymlinks(dir); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(keep); string(data) != "real" {
		t.Fatal("existing dev entry was clobbered")
	}
}

func TestCheckChrootEnv(t *testing.T) {
	if _, err := os.Stat("/proc/self"); err != nil {
		t.Skip("host /proc/self missing; cannot validate fd resolution")
	}
	dir := t.TempDir()
	if err := installer.CheckChrootEnv(dir); err == nil ||
		!strings.Contains(err.Error(), "/proc") {
		t.Fatalf("empty dir should fail on /proc, got: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "proc", "self"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := installer.CheckChrootEnv(dir); err == nil ||
		!strings.Contains(err.Error(), "/dev/fd") {
		t.Fatalf("missing dev/fd should fail, got: %v", err)
	}
	if err := installer.EnsureDevSymlinks(dir); err != nil {
		t.Fatal(err)
	}
	if err := installer.CheckChrootEnv(dir); err != nil {
		t.Fatalf("complete env should pass: %v", err)
	}
}

func TestMountSourceNegative(t *testing.T) {
	if got := installer.MountSource("/definitely/not/a/mountpoint-gentooinstall-xyz"); got != "" {
		t.Fatalf("MountSource = %q, want empty", got)
	}
}

func TestMountByIDRefusesStaleMount(t *testing.T) {
	if installer.MountSource("/proc") == "" {
		t.Skip("/proc not mounted; cannot test stale-mount refusal")
	}
	cfg := classicCfg("/dev/sdX", false, false)
	c, _ := testContext(t, cfg, nil)
	c.IsMountpoint = func(string) bool { return true }
	// /proc is really mounted (from "proc"), which cannot be the layout's
	// root device: reuse must be refused, not silently accepted.
	err := installer.MountByID(c, c.Layout.RootID, "/proc")
	if err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("expected stale-mount error, got: %v", err)
	}
}

func TestUnmountChroot(t *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	c, stub := testContext(t, cfg, nil)
	dir := filepath.Join(t.TempDir(), "root")

	c.IsMountpoint = func(string) bool { return false }
	if err := installer.UnmountChroot(c, dir); err != nil {
		t.Fatalf("UnmountChroot empty: %v", err)
	}
	if len(stub.Calls()) != 0 {
		t.Fatalf("nothing mounted, want no commands, got %v", stub.Lines())
	}

	c.IsMountpoint = func(string) bool { return true }
	if err := installer.UnmountChroot(c, dir); err != nil {
		t.Fatalf("UnmountChroot: %v", err)
	}
	j := filepath.Join
	assertCmds(t, stub,
		"umount -l "+j(dir, "dev", "pts"),
		"umount -l "+j(dir, "dev"),
		"umount -l "+j(dir, "sys"),
		"umount -l "+j(dir, "tmp"),
		"umount -l "+j(dir, "run"),
		"umount -l "+j(dir, "proc"),
		"umount -l "+dir,
	)
}
