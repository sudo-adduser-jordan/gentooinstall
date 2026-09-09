// Device ID resolution (disklayout.Resolver, Canonicalize, SplitIDList).
package tests

import (
	"testing"

	"gentooinstall/lib/disklayout"
)

func TestResolverCanonicalizeFallback(testingT *testing.T) {
	dev := "/dev/nonexistent-gentooinstall-xyz-123"
	if got := disklayout.Canonicalize(dev); got != dev {
		testingT.Fatalf("Canonicalize(%q) = %q, want passthrough", dev, got)
	}
}

func TestResolverDeviceByPtUuidCached(testingT *testing.T) {
	resolver := &disklayout.Resolver{}
	resolver.SetCachedLsblk("NAME=\"/dev/sdz\" PTUUID=\"abcd-1234\" PARTUUID=\"\"\n" +
		"NAME=\"/dev/sdz1\" PTUUID=\"abcd-1234\" PARTUUID=\"11111111-2222-3333-4444-555555555555\"\n")
	got, err := resolver.DeviceByPtUuid("ABCD-1234")
	if err != nil {
		testingT.Fatalf("DeviceByPtUuid: %v", err)
	}
	if got != "/dev/sdz" {
		testingT.Fatalf("DeviceByPtUuid = %q, want /dev/sdz", got)
	}
	if _, err := resolver.DeviceByPtUuid("deadbeef-0000"); err == nil {
		testingT.Fatal("expected error for unknown PTUUID")
	}
}

func TestResolverCachedEnvRoundTrip(testingT *testing.T) {
	resolver := &disklayout.Resolver{}
	resolver.SetCachedLsblk("  \n") // blank must not seed the cache
	if cached := resolver.CachedEnvValue(); cached != "" {
		testingT.Fatalf("blank cache seeded %q", cached)
	}
	resolver.SetCachedLsblk("NAME=\"/dev/sda\" PTUUID=\"aa\" PARTUUID=\"\"")
	if cached := resolver.CachedEnvValue(); cached == "" {
		testingT.Fatal("expected cached value")
	}
	other := &disklayout.Resolver{}
	other.SetCachedLsblk(resolver.CachedEnvValue())
	got, err := other.DeviceByPtUuid("aa")
	if err != nil || got != "/dev/sda" {
		testingT.Fatalf("passthrough cache resolve = %q, %v", got, err)
	}
}

func TestResolverResolveLuksDevice(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", true, false)
	layout, err := disklayout.BuildFromConfig(cfg, testingT.TempDir())
	if err != nil {
		testingT.Fatalf("BuildFromConfig: %v", err)
	}
	resolver := &disklayout.Resolver{Layout: layout}
	got, err := resolver.ResolveDevice("part_luks_root")
	if err != nil {
		testingT.Fatalf("ResolveDevice luks: %v", err)
	}
	if got != "/dev/mapper/root" {
		testingT.Fatalf("ResolveDevice(part_luks_root) = %q, want /dev/mapper/root", got)
	}
	if _, err := resolver.ResolveDevice("no-such-id"); err == nil {
		testingT.Fatal("expected error for unknown id")
	}
}

func TestSplitIDList(testingT *testing.T) {
	got := disklayout.SplitIDList("a;b;;c")
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		testingT.Fatalf("SplitIDList = %q", got)
	}
	if len(disklayout.SplitIDList("")) != 0 {
		testingT.Fatal("empty input must yield no ids")
	}
}

func TestResolverResolveExistingDevice(testingT *testing.T) {
	builder := disklayout.NewBuilder(testingT.TempDir())
	if err := builder.RegisterExisting("rootdisk", "/dev/sdz"); err != nil {
		testingT.Fatalf("RegisterExisting: %v", err)
	}
	layout := builder.Finish()
	resolver := &disklayout.Resolver{Layout: layout}
	got, err := resolver.ResolveDevice("rootdisk")
	if err != nil {
		testingT.Fatalf("ResolveDevice: %v", err)
	}
	if got != "/dev/sdz" {
		testingT.Fatalf("ResolveDevice(rootdisk) = %q, want /dev/sdz", got)
	}
}
