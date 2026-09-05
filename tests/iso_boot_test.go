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
	// The Alpine-based live rootfs must provide the engine's host tools.
	if !strings.Contains(serial, "live: tools:") || !strings.Contains(serial, "sgdisk") ||
		!strings.Contains(serial, "gpg") {
		t.Fatalf("live toolset missing or incomplete; full output above")
	}
}

// TestISOBootNetwork is a QEMU end-to-end test that boots the live ISO with a
// virtio-net NIC attached (via QEMU user networking) and asserts the live init
// brings up DHCP, writes /etc/resolv.conf, and successfully resolves + reaches
// the configured Gentoo mirror (the serial mirror self-check). This exercises
// the exact path a real install depends on — DHCP → DNS → outbound HTTPS → CA
// certs — which TestISOBoots deliberately skips because it boots without a NIC.
func TestISOBootNetwork(t *testing.T) {
	if os.Getenv("GENTOOINSTALL_E2E") == "" {
		t.Skip("set GENTOOINSTALL_E2E=1 to run QEMU e2e test")
	}
	if testing.Short() {
		t.Skip("skipping QEMU e2e in short mode")
	}
	if _, err := exec.LookPath("qemu-system-x86_64"); err != nil {
		t.Skip("qemu-system-x86_64 not found; skipping live-ISO network test")
	}
	if _, err := exec.LookPath("qemu-img"); err != nil {
		t.Skip("qemu-img not found; skipping live-ISO network test")
	}

	root := repoRoot(t)
	iso := filepath.Join(t.TempDir(), "gentooinstall-live-amd64.iso")

	buildISO(t, root, iso)
	assertISOName(t, iso)

	// Attach a user-network NIC that the bundled live initramfs can actually
	// drive. The live rootfs bundles the e1000 driver (confirmed by the
	// "live: loaded module e1000" banner), whereas virtio_net is not always
	// shipped as a standalone module by the host kernel used to build the ISO
	// (distro kernels often build it in, but the bundle step cannot then find a
	// .ko to ship). -nodefaults (used by bootISO for determinism) suppresses
	// QEMU's default NIC, so the NIC must be added explicitly.
	network := []string{
		"-netdev", "user,id=net0,dns=10.0.2.3",
		"-device", "e1000,netdev=net0",
	}
	serial := bootISO(t, iso, network...)
	t.Logf("QEMU serial output:\n%s", serial)

	if !strings.Contains(serial, "live: dhcp ") || !strings.Contains(serial, ": up") {
		t.Fatalf("live init did not report a successful DHCP bring-up; full output above")
	}
	// QEMU user networking hands out 10.0.2.3 as the DNS server; the udhcpc
	// bound script must have written it to /etc/resolv.conf.
	if !strings.Contains(serial, "nameserver 10.0.2.3") {
		t.Fatalf("/etc/resolv.conf was not populated with the QEMU nameserver; full output above")
	}
	// The PID-1 mirror self-check must resolve the mirror host (DNS) and reach it
	// over HTTPS (network + CA certs), reporting ok on the serial console.
	if !strings.Contains(serial, "live: mirror ") || !strings.Contains(serial, ": ok") {
		t.Fatalf("mirror self-check did not report ok over the network; full output above")
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

func bootISO(t *testing.T, iso string, extra ...string) string {
	t.Helper()
	// Attach a drive the same way the README exercise does: a plain qcow2 on
	// the default IDE interface (shows up as /dev/sda once the bundled IDE
	// driver loads). t.Cleanup kills the process if the test is interrupted,
	// so the VM never lingers. The flags below run with -no-reboot, and the
	// watchdog kills the VM if it does not settle, so no `timeout` wrapper is
	// needed. extra args (e.g. a user-network NIC) are appended verbatim.
	disk := filepath.Join(t.TempDir(), "gentoo-disk.img")
	if out, err := exec.Command("qemu-img", "create", "-f", "qcow2", disk, "2G").CombinedOutput(); err != nil {
		t.Fatalf("qemu-img create failed: %v\n%s", err, out)
	}
	args := []string{
		"-cdrom", iso,
		"-drive", "file=" + disk + ",format=qcow2",
		"-m", "512",
		"-nodefaults",
		"-nographic",
		"-serial", "stdio",
		"-no-reboot",
		"-display", "none",
	}
	args = append(args, extra...)
	cmd := exec.Command("qemu-system-x86_64", args...)
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
