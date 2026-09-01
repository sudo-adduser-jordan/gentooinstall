package tests

import (
	"strings"
	"testing"

	"gentooinstall/assets"
	"gentooinstall/internal/sysinfo"
)

func TestShortenDevice(t *testing.T) {
	if got := sysinfo.ShortenDevice("/dev/disk/by-id/ata-FOO"); got != "ata-FOO" {
		t.Fatal(got)
	}
	if got := sysinfo.ShortenDevice("/dev/sda"); got != "/dev/sda" {
		t.Fatal(got)
	}
}

func TestCanonicalizePassthrough(t *testing.T) {
	if got := sysinfo.CanonicalizeDevice("/dev/nonexistent-xyz"); got != "/dev/nonexistent-xyz" {
		t.Fatal(got)
	}
}

func TestDefaultKeymapFallback(t *testing.T) {
	if k := sysinfo.DefaultKeymap([]string{"us", "de"}); k != "us" {
		t.Fatal(k)
	}
}

func TestEmbeddedLocales(t *testing.T) {
	locs := assets.SupportedLocales()
	if len(locs) < 400 {
		t.Fatalf("expected many locales, got %d", len(locs))
	}
	found := false
	for _, l := range locs {
		if strings.HasPrefix(l, "en_US.UTF-8") {
			found = true
		}
	}
	if !found {
		t.Fatal("en_US.UTF-8 missing")
	}
}

func TestEmbeddedAssetsNonEmpty(t *testing.T) {
	if !strings.Contains(assets.Fstab, "fstab") {
		t.Fatal("fstab asset wrong")
	}
	if !strings.Contains(assets.SSHDConfig, "PermitRootLogin") {
		t.Fatal("sshd_config asset wrong")
	}
}

func TestFallbackKeymapsPresent(t *testing.T) {
	if len(sysinfo.FallbackKeymaps) == 0 {
		t.Fatal("empty fallback keymaps")
	}
}

func TestEFIAndBootType(t *testing.T) {
	// On the test machine either result is fine; just ensure consistency.
	bt := sysinfo.DefaultBootType()
	if bt != "efi" && bt != "bios" {
		t.Fatal(bt)
	}
	if (bt == "efi") != sysinfo.HasEFI() {
		t.Fatal("boot type inconsistent with EFI detection")
	}
}
