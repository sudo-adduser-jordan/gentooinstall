// System information basics and embedded assets.
package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gentooinstall/assets"
	"gentooinstall/lib/sysinfo"
)

func TestCanonicalizePassthrough(testingT *testing.T) {
	if got := sysinfo.CanonicalizeDevice("/dev/nonexistent-xyz"); got != "/dev/nonexistent-xyz" {
		testingT.Fatal(got)
	}
}

func TestDefaultKeymapFallback(testingT *testing.T) {
	if keymap := sysinfo.DefaultKeymap([]string{"us", "de"}); keymap != "us" {
		testingT.Fatal(keymap)
	}
}

func TestEmbeddedLocales(testingT *testing.T) {
	locs := assets.SupportedLocales()
	if len(locs) < 400 {
		testingT.Fatalf("expected many locales, got %d", len(locs))
	}
	found := false
	for _, locale := range locs {
		if strings.HasPrefix(locale, "en_US.UTF-8") {
			found = true
		}
	}
	if !found {
		testingT.Fatal("en_US.UTF-8 missing")
	}
}

func TestEmbeddedAssetsNonEmpty(testingT *testing.T) {
	if !strings.Contains(assets.Fstab, "fstab") {
		testingT.Fatal("fstab asset wrong")
	}
	if !strings.Contains(assets.SSHDConfig, "PermitRootLogin") {
		testingT.Fatal("sshd_config asset wrong")
	}
}

func TestFallbackKeymapsPresent(testingT *testing.T) {
	if len(sysinfo.FallbackKeymaps) == 0 {
		testingT.Fatal("empty fallback keymaps")
	}
}

func TestEFIAndBootType(testingT *testing.T) {
	// EFI detection must resolve to a boolean (either result is fine).
	_ = sysinfo.HasEFI()
}

func TestSupportsFilesystemPath(testingT *testing.T) {
	dir := testingT.TempDir()
	withVfat := filepath.Join(dir, "filesystems")
	if err := os.WriteFile(withVfat, []byte("nodev\tsysfs\nnodev\tbpf\n\tvfat\n"), 0o644); err != nil {
		testingT.Fatal(err)
	}
	if !sysinfo.SupportsFilesystemPath(withVfat, "vfat") {
		testingT.Fatal("vfat should be detected")
	}
	if sysinfo.SupportsFilesystemPath(withVfat, "zfs") {
		testingT.Fatal("zfs should not be detected")
	}
	without := filepath.Join(dir, "nofat")
	if err := os.WriteFile(without, []byte("nodev\tsysfs\n\text4\n"), 0o644); err != nil {
		testingT.Fatal(err)
	}
	if sysinfo.SupportsFilesystemPath(without, "vfat") {
		testingT.Fatal("vfat must be missing")
	}
	if sysinfo.SupportsFilesystemPath(filepath.Join(dir, "absent"), "vfat") {
		testingT.Fatal("unreadable table must report unsupported")
	}
}
