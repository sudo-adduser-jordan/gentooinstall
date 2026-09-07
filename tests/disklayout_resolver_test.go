// Device ID resolution (disklayout.Resolver, Canonicalize, SplitIDList).
package tests

import (
	"testing"

	"gentooinstall/internal/disklayout"
)

func TestResolverCanonicalizeFallback(t *testing.T) {
	dev := "/dev/nonexistent-gentooinstall-xyz-123"
	if got := disklayout.Canonicalize(dev); got != dev {
		t.Fatalf("Canonicalize(%q) = %q, want passthrough", dev, got)
	}
}

func TestResolverDeviceByPtUuidCached(t *testing.T) {
	r := &disklayout.Resolver{}
	r.SetCachedLsblk("NAME=\"/dev/sdz\" PTUUID=\"abcd-1234\" PARTUUID=\"\"\n" +
		"NAME=\"/dev/sdz1\" PTUUID=\"abcd-1234\" PARTUUID=\"11111111-2222-3333-4444-555555555555\"\n")
	got, err := r.DeviceByPtUuid("ABCD-1234")
	if err != nil {
		t.Fatalf("DeviceByPtUuid: %v", err)
	}
	if got != "/dev/sdz" {
		t.Fatalf("DeviceByPtUuid = %q, want /dev/sdz", got)
	}
	if _, err := r.DeviceByPtUuid("deadbeef-0000"); err == nil {
		t.Fatal("expected error for unknown PTUUID")
	}
}

func TestResolverCachedEnvRoundTrip(t *testing.T) {
	r := &disklayout.Resolver{}
	r.SetCachedLsblk("  \n") // blank must not seed the cache
	if v := r.CachedEnvValue(); v != "" {
		t.Fatalf("blank cache seeded %q", v)
	}
	r.SetCachedLsblk("NAME=\"/dev/sda\" PTUUID=\"aa\" PARTUUID=\"\"")
	if v := r.CachedEnvValue(); v == "" {
		t.Fatal("expected cached value")
	}
	other := &disklayout.Resolver{}
	other.SetCachedLsblk(r.CachedEnvValue())
	got, err := other.DeviceByPtUuid("aa")
	if err != nil || got != "/dev/sda" {
		t.Fatalf("passthrough cache resolve = %q, %v", got, err)
	}
}

func TestResolverResolveLuksDevice(t *testing.T) {
	cfg := classicCfg("/dev/sdX", true, false)
	layout, err := disklayout.BuildFromConfig(cfg, t.TempDir())
	if err != nil {
		t.Fatalf("BuildFromConfig: %v", err)
	}
	r := &disklayout.Resolver{Layout: layout}
	got, err := r.ResolveDevice("part_luks_root")
	if err != nil {
		t.Fatalf("ResolveDevice luks: %v", err)
	}
	if got != "/dev/mapper/root" {
		t.Fatalf("ResolveDevice(part_luks_root) = %q, want /dev/mapper/root", got)
	}
	if _, err := r.ResolveDevice("no-such-id"); err == nil {
		t.Fatal("expected error for unknown id")
	}
}

func TestSplitIDList(t *testing.T) {
	got := disklayout.SplitIDList("a;b;;c")
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("SplitIDList = %q", got)
	}
	if len(disklayout.SplitIDList("")) != 0 {
		t.Fatal("empty input must yield no ids")
	}
}

func TestResolverResolveExistingDevice(t *testing.T) {
	b := disklayout.NewBuilder(t.TempDir())
	if err := b.RegisterExisting("rootdisk", "/dev/sdz"); err != nil {
		t.Fatalf("RegisterExisting: %v", err)
	}
	layout := b.Finish()
	r := &disklayout.Resolver{Layout: layout}
	got, err := r.ResolveDevice("rootdisk")
	if err != nil {
		t.Fatalf("ResolveDevice: %v", err)
	}
	if got != "/dev/sdz" {
		t.Fatalf("ResolveDevice(rootdisk) = %q, want /dev/sdz", got)
	}
}
