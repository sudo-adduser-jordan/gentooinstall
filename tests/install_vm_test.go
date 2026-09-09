// Opt-in QEMU end-to-end test that executes a full installation for every
// shipped build template inside the VM (needs GENTOOINSTALL_E2E=1 and
// GENTOOINSTALL_E2E_INSTALL=1; run via `make vm-install`). Each template is
// staged as builds/custom.toml on the ISO, a `gentooinstall.install=...`
// kernel flag makes the live init boot straight into a headless
// `gentooinstall install`, and the test asserts the `gentooinstall install:
// success` marker on the serial console. This is slow (stage3 download +
// chroot + kernel per file), hence opt-in and local-only.
package tests

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gentooinstall/lib/config"
)

// installWatchdog bounds one full VM install. A full install includes the
// stage3 download, portage sync and the chroot work, which on a 2-vCPU guest
// can take tens of minutes on a slow mirror.
const installWatchdog = 60 * time.Minute

// vmInstallCase describes the VM fixture a build template needs.
type vmInstallCase struct {
	name    string // builds/<name>
	disks   int    // raw disk images to attach (/dev/sda, /dev/sdb, ...)
	prepare bool   // pre-partition + format the first disk (existing-efi)
}

var vmInstallCases = []vmInstallCase{
	{name: "default.toml"},
	{name: "bios.toml"},
	{name: "openrc.toml"},
	{name: "musl.toml"},
	{name: "desktop-systemd.toml"},
	{name: "btrfs-efi.toml", disks: 2},
	{name: "existing-efi.toml", prepare: true},
}

// TestInstallInVM runs a full install in QEMU for each shipped build template.
func TestInstallInVM(testingT *testing.T) {
	if os.Getenv("GENTOOINSTALL_E2E") == "" {
		testingT.Skip("set GENTOOINSTALL_E2E=1 to run QEMU e2e tests")
	}
	if os.Getenv("GENTOOINSTALL_E2E_INSTALL") == "" {
		testingT.Skip("set GENTOOINSTALL_E2E_INSTALL=1 to run VM install tests")
	}
	if testing.Short() {
		testingT.Skip("skipping QEMU install e2e in short mode")
	}
	for _, tc := range []string{"qemu-system-x86_64", "qemu-img", "sgdisk"} {
		if _, err := exec.LookPath(tc); err != nil {
			testingT.Skipf("%s not found; skipping VM install tests", tc)
		}
	}
	root := repoRoot(testingT)
	for _, tc := range vmInstallCases {
		testingT.Run(tc.name, func(testingT *testing.T) {
			runInstallInVM(testingT, root, tc)
		})
	}
}

// runInstallInVM performs one full install inside QEMU for a build template.
func runInstallInVM(testingT *testing.T, root string, tc vmInstallCase) {
	testingT.Helper()
	dir := testingT.TempDir()

	// Stage the template with VM-appropriate device paths, then build an ISO
	// whose default grub entry boots straight into a headless install of it
	// (release.sh GENTOOINSTALL_INSTALL_CFG hook).
	staged := filepath.Join(dir, "custom.toml")
	cfg := stageConfig(testingT, root, tc.name, staged)
	fw := cfg.Disk.BootType
	if fw != "efi" && fw != "bios" {
		testingT.Fatalf("%s: unexpected boot_type %q", tc.name, fw)
	}

	iso := filepath.Join(dir, "gentooinstall-live-amd64.iso")
	buildInstallISO(testingT, root, iso, staged)

	numDisks := tc.disks
	if numDisks == 0 {
		numDisks = 1
	}
	disks := make([]string, numDisks)
	for idx := range disks {
		disks[idx] = filepath.Join(dir, fmt.Sprintf("disk%d.img", idx))
		imgPath := disks[idx]
		if out, err := exec.Command("qemu-img", "create", "-f", "raw", imgPath, "20G").CombinedOutput(); err != nil {
			testingT.Fatalf("qemu-img create failed: %v\n%s", err, out)
		}
	}
	if tc.prepare {
		prePrepareExisting(testingT, disks[0])
	}

	serial := bootInstallVM(testingT, iso, fw, disks, diskBuses(cfg))
	testingT.Logf("QEMU serial output:\n%s", serial)

	if !strings.Contains(serial, "gentooinstall install: success") {
		testingT.Fatalf("headless install did not finish successfully; full output above")
	}
}

// stageConfig loads builds/<name>, rewrites the placeholder device paths for
// the VM fixtures (/dev/sdX -> /dev/sda, /dev/sdY -> /dev/sdb, sdX1..3 ->
// sda1..3) and saves the result to out. The staged config is what tells the
// test which firmware the VM must boot with.
func stageConfig(testingT *testing.T, root, name, out string) *config.Config {
	testingT.Helper()
	cfg, err := config.Load(filepath.Join(root, "builds", name))
	if err != nil {
		testingT.Fatalf("%s: %v", name, err)
	}
	fix := func(str string) string {
		replacer := strings.NewReplacer("/dev/sdX1", "/dev/sda1",
			"/dev/sdX2", "/dev/sda2", "/dev/sdX3", "/dev/sda3",
			"/dev/sdX", "/dev/sda", "/dev/sdY", "/dev/sdb")
		return replacer.Replace(str)
	}
	cfg.Disk.Device = fix(cfg.Disk.Device)
	cfg.Disk.BootDevice = fix(cfg.Disk.BootDevice)
	cfg.Disk.SwapDevice = fix(cfg.Disk.SwapDevice)
	for idx := range cfg.Disk.Devices {
		cfg.Disk.Devices[idx] = fix(cfg.Disk.Devices[idx])
	}
	if err := cfg.Save(out); err != nil {
		testingT.Fatalf("%s: stage save: %v", name, err)
	}
	return cfg
}

// diskBuses returns the QEMU disk bus to attach each disk image on, derived
// from the configured device paths in attach order (multi-device schemes use
// Disk.Devices, everything else Disk.Device, with partition suffixes stripped
// to reach the whole disk). A configured /dev/vd* device needs virtio-blk to
// show up at that path; /dev/sd* maps to the IDE bus QEMU presents by default.
func diskBuses(cfg *config.Config) []string {
	names := cfg.Disk.Devices
	if len(names) == 0 {
		names = []string{cfg.Disk.Device}
	}
	buses := make([]string, len(names))
	for idx, dev := range names {
		if strings.HasPrefix(strings.TrimRight(dev, "0123456789"), "/dev/vd") {
			buses[idx] = "virtio"
		} else {
			buses[idx] = "ide"
		}
	}
	return buses
}

// TestDiskBuses pins the bus mapping used by runInstallInVM so a build that
// targets /dev/vd* gets a virtio-blk disk and /dev/sd* an IDE one.
func TestDiskBuses(testingT *testing.T) {
	cases := []struct {
		name  string
		cfg   *config.Config
		wants []string
	}{
		{name: "single sda", cfg: &config.Config{Disk: config.Disk{Device: "/dev/sda"}}, wants: []string{"ide"}},
		{name: "single vda", cfg: &config.Config{Disk: config.Disk{Device: "/dev/vda"}}, wants: []string{"virtio"}},
		{name: "partition device", cfg: &config.Config{Disk: config.Disk{Device: "/dev/sdX3"}}, wants: []string{"ide"}},
		{
			name:  "two disks",
			cfg:   &config.Config{Disk: config.Disk{Devices: []string{"/dev/sda", "/dev/sdb"}}},
			wants: []string{"ide", "ide"},
		},
		{
			name:  "non-virtio device",
			cfg:   &config.Config{Disk: config.Disk{Device: "/dev/nvme0n1"}},
			wants: []string{"ide"},
		},
	}
	for _, tc := range cases {
		testingT.Run(tc.name, func(testingT *testing.T) {
			got := diskBuses(tc.cfg)
			if len(got) != len(tc.wants) {
				testingT.Fatalf("diskBuses() = %v, want %v", got, tc.wants)
			}
			for idx := range got {
				if got[idx] != tc.wants[idx] {
					testingT.Fatalf("diskBuses() = %v, want %v", got, tc.wants)
				}
			}
		})
	}
}

// prePrepareExisting partitions and formats the first (raw) disk for the
// existing-efi template: GPT with a 512M FAT32 ESP, an 8G swap and an ext4
// root filling the rest. sgdisk operates on the file directly; formatting
// needs the partitions visible through a loop device, so it requires root
// (skips cleanly otherwise).
func prePrepareExisting(testingT *testing.T, img string) {
	testingT.Helper()
	for _, tool := range []string{"sgdisk", "mkfs.vfat", "mkswap", "mkfs.ext4", "losetup"} {
		if _, err := exec.LookPath(tool); err != nil {
			testingT.Skipf("%s not found; cannot pre-format the existing-efi disk", tool)
		}
	}
	mustRun(testingT, "sgdisk", "--zap-all", img)
	mustRun(testingT, "sgdisk", "-n", "1:0:+512M", "-t", "1:ef00", "-c", "1:EFI", img)
	mustRun(testingT, "sgdisk", "-n", "2:0:+8G", "-t", "2:8200", img)
	mustRun(testingT, "sgdisk", "-n", "3:0:0", "-t", "3:8300", img)

	out, err := exec.Command("losetup", "--show", "-fP", img).CombinedOutput()
	if err != nil {
		testingT.Skipf("losetup failed (needs root?): %v\n%s", err, out)
	}
	loop := strings.TrimSpace(string(out))
	defer func() {
		_ = exec.Command("losetup", "-d", loop).Run()
	}()
	// Partition nodes appear asynchronously; wait briefly for loopNp1.
	part := loop + "p1"
	for attempt := 0; attempt < 50; attempt++ {
		if _, err := os.Stat(part); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	mustRun(testingT, "mkfs.vfat", "-F32", "-n", "EFI", loop+"p1")
	mustRun(testingT, "mkswap", loop+"p2")
	mustRun(testingT, "mkfs.ext4", "-F", loop+"p3")
}

// buildInstallISO builds the live ISO with GENTOOINSTALL_INSTALL_CFG set so
// the default grub entry boots straight into a headless install of cfg.
func buildInstallISO(testingT *testing.T, root, out, cfg string) {
	testingT.Helper()
	cmd := exec.Command(filepath.Join(root, "scripts", "release.sh"), out)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GENTOOINSTALL_INSTALL_CFG="+cfg)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		testingT.Fatalf("release.sh failed: %v\noutput:\n%s", err, buf.String())
	}
	assertISOName(testingT, out)
}

// bootInstallVM boots the ISO under QEMU with the firmware matching the staged
// config (OVMF for efi, SeaBIOS for bios), attaches the raw disk images on the
// bus each configured device expects, and captures the serial console until
// the guest powers off (or the watchdog fires). -no-reboot makes QEMU exit
// when the install init powers the guest down, which is how the test knows the
// install completed.
func bootInstallVM(testingT *testing.T, iso, fw string, disks, buses []string) string {
	testingT.Helper()
	if len(disks) != len(buses) {
		testingT.Fatalf("got %d disks but %d buses; diskBuses must match disk count", len(disks), len(buses))
	}
	args := []string{
		"-cdrom", iso,
		"-m", "2048",
		"-smp", "2",
		"-nodefaults",
		"-nographic",
		"-serial", "stdio",
		"-monitor", "none",
		"-no-reboot",
		"-display", "none",
	}
	if fw == "efi" {
		code, vars := ovmfFiles(testingT)
		args = append(args,
			"-drive", "if=pflash,format=raw,readonly=on,file="+code,
			"-drive", "if=pflash,format=raw,file="+vars)
	}
	for idx, disk := range disks {
		args = append(args, "-drive", "file="+disk+",format=raw,if="+buses[idx])
	}
	args = append(args,
		"-netdev", "user,id=net0,dns=10.0.2.3",
		"-device", "e1000,netdev=net0")

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
	case <-time.After(installWatchdog):
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

// ovmfFiles locates the OVMF firmware (code + a writable vars copy) for a UEFI
// boot, skipping the test when the host has none installed.
func ovmfFiles(testingT *testing.T) (code, vars string) {
	testingT.Helper()
	candidates := [][2]string{
		{"/usr/share/OVMF/x64/OVMF_CODE.4m.fd", "/usr/share/OVMF/x64/OVMF_VARS.4m.fd"},
		{"/usr/share/OVMF/x86_64/OVMF_CODE.4m.fd", "/usr/share/OVMF/x86_64/OVMF_VARS.4m.fd"},
		{"/usr/share/OVMF/OVMF_CODE.fd", "/usr/share/OVMF/OVMF_VARS.fd"},
		{"/usr/share/edk2/x64/OVMF_CODE.4m.fd", "/usr/share/edk2/x64/OVMF_VARS.4m.fd"},
		{"/usr/share/edk2-ovmf/x64/OVMF_CODE.fd", "/usr/share/edk2-ovmf/x64/OVMF_VARS.fd"},
		{"/usr/share/edk2-ovmf/OVMF_CODE.fd", "/usr/share/edk2-ovmf/OVMF_VARS.fd"},
	}
	for _, cand := range candidates {
		if _, err := os.Stat(cand[0]); err != nil {
			continue
		}
		if _, err := os.Stat(cand[1]); err != nil {
			continue
		}
		vars = filepath.Join(testingT.TempDir(), "OVMF_VARS.fd")
		data, err := os.ReadFile(cand[1])
		if err != nil {
			continue
		}
		if err := os.WriteFile(vars, data, 0o600); err != nil {
			continue
		}
		return cand[0], vars
	}
	testingT.Skip("no OVMF firmware found; skipping EFI install case")
	return "", ""
}

// mustRun runs a host command, failing the test on error with its output.
func mustRun(testingT *testing.T, name string, args ...string) {
	testingT.Helper()
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		testingT.Fatalf("%s %s failed: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}
