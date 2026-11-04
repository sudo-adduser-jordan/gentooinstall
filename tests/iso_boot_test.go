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

func TestISOBoots(testingT *testing.T) {
	if os.Getenv("GENTOOINSTALL_E2E") == "" {
		testingT.Skip("set GENTOOINSTALL_E2E=1 to run QEMU e2e test")
	}
	if testing.Short() {
		testingT.Skip("skipping QEMU e2e in short mode")
	}
	if _, err := exec.LookPath("qemu-system-x86_64"); err != nil {
		testingT.Skip("qemu-system-x86_64 not found; skipping live-ISO boot test")
	}
	if _, err := exec.LookPath("qemu-img"); err != nil {
		testingT.Skip("qemu-img not found; skipping live-ISO boot test")
	}

	root := repoRoot(testingT)
	iso := filepath.Join(testingT.TempDir(), "gentooinstall-live-amd64.iso")

	buildISO(testingT, root, iso)
	assertISOName(testingT, iso)

	serial := bootISO(testingT, iso)
	testingT.Logf("QEMU serial output:\n%s", serial)

	if !strings.Contains(serial, "gentooinstall init: PID 1") {
		testingT.Fatalf("gentooinstall init did not start on the serial console; full output above")
	}
	if !strings.Contains(serial, "live: tui starting") {
		testingT.Fatalf("boot did not reach the TUI on the serial console; full output above")
	}
}

func TestISOBootNetwork(testingT *testing.T) {
	if os.Getenv("GENTOOINSTALL_E2E") == "" {
		testingT.Skip("set GENTOOINSTALL_E2E=1 to run QEMU e2e test")
	}
	if os.Getenv("GENTOOINSTALL_E2E_NET") == "" {
		testingT.Skip("set GENTOOINSTALL_E2E_NET=1 to run the local-only network e2e test")
	}
	if testing.Short() {
		testingT.Skip("skipping QEMU e2e in short mode")
	}
	if _, err := exec.LookPath("qemu-system-x86_64"); err != nil {
		testingT.Skip("qemu-system-x86_64 not found; skipping live-ISO network test")
	}
	if _, err := exec.LookPath("qemu-img"); err != nil {
		testingT.Skip("qemu-img not found; skipping live-ISO network test")
	}

	root := repoRoot(testingT)
	iso := filepath.Join(testingT.TempDir(), "gentooinstall-live-amd64.iso")

	buildISO(testingT, root, iso)
	assertISOName(testingT, iso)

	network := []string{
		"-netdev", "user,id=net0,dns=10.0.2.3",
		"-device", "e1000,netdev=net0",
	}
	serial := bootISO(testingT, iso, network...)
	testingT.Logf("QEMU serial output:\n%s", serial)

	if !strings.Contains(serial, "live: tui starting") {
		testingT.Fatalf("boot did not reach the TUI on the serial console; full output above")
	}
	if strings.Contains(serial, "live: resolv.conf:") {
		testingT.Fatalf("background network log smeared the serial TUI; full output above")
	}
}

func repoRoot(testingT *testing.T) string {
	testingT.Helper()
	dir, err := os.Getwd()
	if err != nil {
		testingT.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			testingT.Fatalf("could not locate repository root from %s", dir)
		}
		dir = parent
	}
}

func buildISO(testingT *testing.T, root, out string) {
	testingT.Helper()
	taskBin, err := exec.LookPath("task")
	if err != nil {
		testingT.Skip("task not found; install go-task to build the ISO")
	}
	cmd := exec.Command(taskBin, "iso")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "ISO_OUTPUT="+out)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		testingT.Fatalf("task iso failed: %v\noutput:\n%s", err, buf.String())
	}
	st, err := os.Stat(out)
	if err != nil {
		testingT.Fatalf("task iso did not produce %s: %v", out, err)
	}
	if st.Size() == 0 {
		testingT.Fatalf("task iso produced an empty ISO at %s", out)
	}
	testingT.Logf("built ISO %s (%d bytes)", out, st.Size())
}

func assertISOName(testingT *testing.T, iso string) {
	testingT.Helper()
	cmd := exec.Command("xorriso", "-osirrox", "off", "-indev", iso, "-find", "/", "-type", "f")
	out, err := cmd.CombinedOutput()
	if err != nil {
		testingT.Fatalf("xorriso listing failed: %v\n%s", err, out)
	}
	got := string(out)
	for _, want := range []string{
		"/boot/initrd.img",
		"/boot/vmlinuz",
		"/boot/grub/grub.cfg",
	} {
		if !strings.Contains(got, want) {
			testingT.Fatalf("ISO is missing %q; listing:\n%s", want, got)
		}
	}
}

func bootISO(testingT *testing.T, iso string, extra ...string) string {
	testingT.Helper()
	disk := filepath.Join(testingT.TempDir(), "gentoo-disk.img")
	if out, err := exec.Command("qemu-img", "create", "-f", "qcow2", disk, "2G").CombinedOutput(); err != nil {
		testingT.Fatalf("qemu-img create failed: %v\n%s", err, out)
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
		testingT.Fatalf("failed to start qemu: %v", err)
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
			testingT.Logf("qemu exited: %v", err)
		}
		return buf.String()
	}
}
