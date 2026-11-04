package live

import (
	"os"
	"sort"
	"strings"
)

var NetworkReady = func() bool { return true }

var NeedModules = []string{
	"virtio", "virtio_ring", "virtio_pci", "virtio_blk", "virtio_scsi",
	"scsi_mod", "sd_mod", "libata", "ahci", "ata_piix", "ata_generic",
	"nvme_keyring", "nvme_auth", "nvme_core", "nvme",
	"usbcore", "usb_storage", "uas", "xhci_pci", "xhci_hcd",
	"virtio_net", "e1000", "e1000e", "r8169", "igb", "ixgbe", "tg3",
	"md_mod", "dm_mod", "dm_crypt", "btrfs",
	"fat", "nls_cp437", "nls_ascii", "vfat",
}

const ModuleDir = "/lib/modules/bundle"

var skipIfaces = map[string]bool{
	"lo": true, "sit0": true, "tunl0": true, "ip6tnl0": true, "dummy0": true,
}

func Interfaces() []string {
	entries, err := os.ReadDir("/sys/class/net")
	if err != nil {
		return nil
	}
	var out []string
	for _, entry := range entries {
		name := entry.Name()
		if skipIfaces[name] || strings.HasPrefix(name, "veth") {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

type MountSpec struct {
	Device string
	Target string
	FSType string
	Flags  uintptr
	Data   string
}

var mountSpecs = []MountSpec{
	{Device: "proc", Target: "/proc", FSType: "proc"},
	{Device: "sysfs", Target: "/sys", FSType: "sysfs"},
	{Device: "devtmpfs", Target: "/dev", FSType: "devtmpfs"},
	{Device: "devpts", Target: "/dev/pts", FSType: "devpts"},
}

func MountTable() []MountSpec {
	return mountSpecs
}
