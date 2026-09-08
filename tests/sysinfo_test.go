// System information basics and embedded assets.
package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gentooinstall/assets"
	"gentooinstall/internal/sysinfo"
)

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
	// EFI detection must resolve to a boolean (either result is fine).
	_ = sysinfo.HasEFI()
}

func TestSupportsFilesystemPath(t *testing.T) {
	dir := t.TempDir()
	withVfat := filepath.Join(dir, "filesystems")
	if err := os.WriteFile(withVfat, []byte("nodev\tsysfs\nnodev\tbpf\n\tvfat\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !sysinfo.SupportsFilesystemPath(withVfat, "vfat") {
		t.Fatal("vfat should be detected")
	}
	if sysinfo.SupportsFilesystemPath(withVfat, "zfs") {
		t.Fatal("zfs should not be detected")
	}
	without := filepath.Join(dir, "nofat")
	if err := os.WriteFile(without, []byte("nodev\tsysfs\n\text4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if sysinfo.SupportsFilesystemPath(without, "vfat") {
		t.Fatal("vfat must be missing")
	}
	if sysinfo.SupportsFilesystemPath(filepath.Join(dir, "absent"), "vfat") {
		t.Fatal("unreadable table must report unsupported")
	}
}
