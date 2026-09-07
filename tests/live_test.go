// Live-ISO environment helpers (mount table, modules, interfaces).
package tests

import (
	"sort"
	"strings"
	"testing"

	"gentooinstall/internal/live"
)

func TestLiveMountTable(t *testing.T) {
	got := live.MountTable()
	if len(got) != 4 {
		t.Fatalf("MountTable len = %d, want 4", len(got))
	}
	wantTargets := []string{"/proc", "/sys", "/dev", "/dev/pts"}
	for i, want := range wantTargets {
		if got[i].Target != want {
			t.Fatalf("MountTable[%d].Target = %q, want %q", i, got[i].Target, want)
		}
	}
	if got[0].FSType != "proc" || got[1].FSType != "sysfs" || got[2].FSType != "devtmpfs" {
		t.Fatalf("unexpected fstypes: %+v", got)
	}
}

func TestLiveNeedModules(t *testing.T) {
	need := map[string]bool{}
	for _, m := range live.NeedModules {
		need[m] = true
	}
	// NICs so DHCP works under QEMU (e1000) and feature support for
	// shipped templates; kept in sync with scripts/release.sh MODULES.
	for _, want := range []string{"e1000", "virtio_net", "dm_crypt", "btrfs", "md_mod", "nvme", "fat", "vfat"} {
		if !need[want] {
			t.Fatalf("NeedModules missing %q: %v", want, live.NeedModules)
		}
	}
	if len(live.NeedModules) != len(need) {
		t.Fatal("NeedModules contains duplicates")
	}
}

func TestLiveInterfacesSafe(t *testing.T) {
	ifs := live.Interfaces()
	if !sort.StringsAreSorted(ifs) {
		t.Fatalf("Interfaces not sorted: %v", ifs)
	}
	for _, name := range ifs {
		if name == "" || name == "lo" || strings.HasPrefix(name, "veth") {
			t.Fatalf("Interfaces leaked pseudo-device %q: %v", name, ifs)
		}
	}
}

func TestLiveNetworkReadyDefault(t *testing.T) {
	// Callable without panic; on linux the init override reports DHCP
	// success (false until tryDHCP succeeds), elsewhere always true.
	_ = live.NetworkReady()
}

func TestLiveModuleDir(t *testing.T) {
	if live.ModuleDir != "/lib/modules/bundle" {
		t.Fatalf("ModuleDir = %q", live.ModuleDir)
	}
}
