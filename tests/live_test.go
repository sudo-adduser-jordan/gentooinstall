// Live-ISO environment helpers (mount table, modules, interfaces).
package tests

import (
	"sort"
	"strings"
	"testing"

	"gentooinstall/lib/live"
)

func TestLiveMountTable(testingT *testing.T) {
	got := live.MountTable()
	if len(got) != 4 {
		testingT.Fatalf("MountTable len = %d, want 4", len(got))
	}
	wantTargets := []string{"/proc", "/sys", "/dev", "/dev/pts"}
	for idx, want := range wantTargets {
		if got[idx].Target != want {
			testingT.Fatalf("MountTable[%d].Target = %q, want %q", idx, got[idx].Target, want)
		}
	}
	if got[0].FSType != "proc" || got[1].FSType != "sysfs" || got[2].FSType != "devtmpfs" {
		testingT.Fatalf("unexpected fstypes: %+v", got)
	}
}

func TestLiveNeedModules(testingT *testing.T) {
	need := map[string]bool{}
	for _, mod := range live.NeedModules {
		need[mod] = true
	}
	// NICs so DHCP works under QEMU (e1000) and feature support for
	// shipped templates; kept in sync with scripts/release.sh MODULES.
	for _, want := range []string{"e1000", "virtio_net", "dm_crypt", "btrfs", "md_mod", "nvme", "fat", "vfat"} {
		if !need[want] {
			testingT.Fatalf("NeedModules missing %q: %v", want, live.NeedModules)
		}
	}
	if len(live.NeedModules) != len(need) {
		testingT.Fatal("NeedModules contains duplicates")
	}
}

func TestLiveInterfacesSafe(testingT *testing.T) {
	ifs := live.Interfaces()
	if !sort.StringsAreSorted(ifs) {
		testingT.Fatalf("Interfaces not sorted: %v", ifs)
	}
	for _, name := range ifs {
		if name == "" || name == "lo" || strings.HasPrefix(name, "veth") {
			testingT.Fatalf("Interfaces leaked pseudo-device %q: %v", name, ifs)
		}
	}
}

func TestLiveNetworkReadyDefault(testingT *testing.T) {
	// Callable without panic; on linux the init override reports DHCP
	// success (false until tryDHCP succeeds), elsewhere always true.
	_ = live.NetworkReady()
}

func TestLiveModuleDir(testingT *testing.T) {
	if live.ModuleDir != "/lib/modules/bundle" {
		testingT.Fatalf("ModuleDir = %q", live.ModuleDir)
	}
}
