package tests

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestISOBoots is a QEMU end-to-end test for the live ISO built by
// scripts/release.sh. It compiles the init binary, bundles an initramfs and
// the host kernel, runs grub-mkrescue, then boots the result under
// qemu-system-x86_64 and asserts the gentooinstall installer starts (its PID-1
// startup banner appears) on the serial console.
//
// This test is intentionally opt-in: it requires QEMU (and, to build the ISO,
// a kernel plus grub-mkrescue/xorriso), so it skips by default. Run it via
// `make vm-test`.
func TestISOBoots(t *testing.T) {
	if os.Getenv("GENTOOINSTALL_E2E") == "" {
		t.Skip("set GENTOOINSTALL_E2E=1 to run QEMU e2e test")
	}
	if testing.Short() {
		t.Skip("skipping QEMU e2e in short mode")
	}
	if _, err := exec.LookPath("qemu-system-x86_64"); err != nil {
		t.Skip("qemu-system-x86_64 not found; skipping live-ISO boot test")
	}
	if _, err := exec.LookPath("qemu-img"); err != nil {
		t.Skip("qemu-img not found; skipping live-ISO boot test")
	}

	root := repoRoot(t)
	iso := filepath.Join(t.TempDir(), "gentooinstall-live-amd64.iso")

	buildISO(t, root, iso)
	assertISOName(t, iso)

	serial := bootISO(t, iso)
	t.Logf("QEMU serial output:\n%s", serial)

	if !strings.Contains(serial, "gentooinstall init: PID 1") {
		t.Fatalf("gentooinstall init did not start on the serial console; full output above")
	}
	if !strings.Contains(serial, "live: block device /dev/sda") {
		t.Fatalf("attached disk was not detected as /dev/sda; full output above")
	}
}

// repoRoot walks up from the test working dir to the repository root (the
// directory containing .git).
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not locate repository root from %s", dir)
		}
		dir = parent
	}
}

// buildISO runs scripts/release.sh to produce the ISO at out.
func buildISO(t *testing.T, root, out string) {
	t.Helper()
	cmd := exec.Command(filepath.Join(root, "scripts", "release.sh"), out)
	cmd.Dir = root
	cmd.Env = os.Environ()
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		t.Fatalf("release.sh failed: %v\noutput:\n%s", err, buf.String())
	}
	st, err := os.Stat(out)
	if err != nil {
		t.Fatalf("release.sh did not produce %s: %v", out, err)
	}
	if st.Size() == 0 {
		t.Fatalf("release.sh produced an empty ISO at %s", out)
	}
	t.Logf("built ISO %s (%d bytes)", out, st.Size())
}

// assertISOName reads the ISO with xorriso (a grub-mkrescue dependency, so it
// is guaranteed present when the ISO can be built) and checks the expected
// boot files are present.
func assertISOName(t *testing.T, iso string) {
	t.Helper()
	cmd := exec.Command("xorriso", "-osirrox", "off", "-indev", iso, "-find", "/", "-type", "f")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("xorriso listing failed: %v\n%s", err, out)
	}
	got := string(out)
	for _, want := range []string{
		"/boot/initrd.img",
		"/boot/vmlinuz",
		"/boot/grub/grub.cfg",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("ISO is missing %q; listing:\n%s", want, got)
		}
	}
}

func bootISO(t *testing.T, iso string) string {
	t.Helper()
	// Attach a drive the same way the README exercise does: a plain qcow2 on
	// the default IDE interface (shows up as /dev/sda once the bundled IDE
	// driver loads). t.Cleanup kills the process if the test is interrupted,
	// so the VM never lingers. The flags below run with -no-reboot, and the
	// watchdog kills the VM if it does not settle, so no `timeout` wrapper is
	// needed.
	disk := filepath.Join(t.TempDir(), "gentoo-disk.img")
	if out, err := exec.Command("qemu-img", "create", "-f", "qcow2", disk, "2G").CombinedOutput(); err != nil {
		t.Fatalf("qemu-img create failed: %v\n%s", err, out)
	}
	cmd := exec.Command("qemu-system-x86_64",
		"-cdrom", iso,
		"-drive", "file="+disk+",format=qcow2",
		"-m", "512",
		"-nodefaults",
		"-nographic",
		"-serial", "stdio",
		"-no-reboot",
		"-display", "none",
	)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start qemu: %v", err)
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case <-time.After(90 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		return buf.String()
	case err := <-done:
		if err != nil && !errors.Is(err, os.ErrProcessDone) {
			// A non-zero exit is fine as long as we saw TUI output.
			t.Logf("qemu exited: %v", err)
		}
		return buf.String()
	}
}
