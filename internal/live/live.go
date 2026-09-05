// Package live bootstraps the minimal userspace for the gentooinstall live
// ISO when the binary runs as PID 1 (kernel argument rdinit=/init): it mounts
// the virtual filesystems, sets sane environment defaults, loads storage
// drivers and brings up DHCP. Everything is best-effort so a failure here must
// never keep the TUI from starting: the headless e2e only learns about the
// boot from the serial banner otherwise.
package live

import (
	"os"
	"sort"
	"strings"
)

// NeedModules is the curated set of storage drivers the live init loads so
// disks appear under /dev even without udev/hotplug in the live rootfs. The
// modules themselves are bundled into the initramfs by scripts/release.sh
// beneath ModuleDir; entries that are built into the bundled kernel (or lack
// a matching module file on the build host) are skipped.
var NeedModules = []string{
	"virtio", "virtio_ring", "virtio_pci", "virtio_blk", "virtio_scsi",
	"scsi_mod", "sd_mod", "libata", "ahci", "ata_piix", "ata_generic",
	"nvme_keyring", "nvme_auth", "nvme_core", "nvme",
	"usbcore", "usb_storage", "uas", "xhci_pci", "xhci_hcd",
}

// ModuleDir is where release.sh stores the decompressed module files that
// loadModules reads; keep in sync with scripts/release.sh.
const ModuleDir = "/lib/modules/bundle"

// skipIfaces are pseudo-interfaces that should never receive a DHCP.
var skipIfaces = map[string]bool{
	"lo": true, "sit0": true, "tunl0": true, "ip6tnl0": true, "dummy0": true,
}

// Interfaces lists the physical network interfaces found below
// /sys/class/net, excluding loopback and tunnel pseudo-devices.
func Interfaces() []string {
	entries, err := os.ReadDir("/sys/class/net")
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if skipIfaces[name] || strings.HasPrefix(name, "veth") {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// MountSpec describes a single entry of the live mount table.
type MountSpec struct {
	Device string
	Target string
	FSType string
	Flags  uintptr
	Data   string
}

// mountSpecs is the ordered list of virtual filesystems the live init mounts.
var mountSpecs = []MountSpec{
	{Device: "proc", Target: "/proc", FSType: "proc"},
	{Device: "sysfs", Target: "/sys", FSType: "sysfs"},
	{Device: "devtmpfs", Target: "/dev", FSType: "devtmpfs"},
	{Device: "devpts", Target: "/dev/pts", FSType: "devpts"},
}

// MountTable returns the virtual filesystems the live init mounts at boot.
func MountTable() []MountSpec {
	return mountSpecs
}
