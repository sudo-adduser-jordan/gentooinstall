// Package tests hosts all gentooinstall test suites, kept separate from the
// production packages so they exercise only the exported API surface.
package tests

import (
	"fmt"
	"path/filepath"
	"testing"

	"gentooinstall/internal/config"
	"gentooinstall/internal/pkglists"
)

func classicCfg(dev string, luks, btrfs bool) *config.Config {
	c := config.Default(true)
	c.Disk.Scheme = config.SchemeClassic
	c.Disk.Device = dev
	c.Disk.UseLuks = luks
	if btrfs {
		c.Disk.RootFS = "btrfs"
	}
	return c
}

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "default.toml")

	c := config.Default(true)
	c.System.Hostname = "myhost"
	c.System.Locales = []string{"en_US.UTF-8 UTF-8", "de_DE.UTF-8 UTF-8"}
	c.Disk.Scheme = config.SchemeZFSCentric
	c.Disk.Devices = []string{"/dev/sda", "/dev/sdb"}
	c.Gentoo.Stage3Variant = "openrc"
	c.Gentoo.Profile = "default/linux/amd64/23.0/desktop/gnome"
	c.Packages.Additional = []string{"apps-one/shellcheck", "net-misc/curl"}
	c.Packages.CustomPackages = []string{"app-editors/helix", "media-sound/foo"}
	c.Packages.EnablingRepos = []string{"guru", "kde"}
	c.Packages.KernelType = "source"
	c.Packages.KernelDeblob = true
	c.MakeConf.Options = []string{"jobs", "ccache"}
	c.MakeConf.Extra = "CFLAGS=\"-O3 -pipe\""

	if err := c.Save(p); err != nil {
		t.Fatal(err)
	}
	got, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.System.Hostname != "myhost" ||
		len(got.System.Locales) != 2 ||
		got.Disk.Scheme != config.SchemeZFSCentric ||
		len(got.Disk.Devices) != 2 ||
		got.Gentoo.Stage3Variant != "openrc" ||
		got.Gentoo.Profile != "default/linux/amd64/23.0/desktop/gnome" {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	if len(got.Packages.Additional) != 2 ||
		len(got.Packages.CustomPackages) != 2 ||
		len(got.Packages.EnablingRepos) != 2 ||
		got.Packages.KernelType != "source" ||
		!got.Packages.KernelDeblob {
		t.Fatalf("packages round trip mismatch: %+v", got.Packages)
	}
	if len(got.MakeConf.Options) != 2 ||
		got.MakeConf.Options[0] != "jobs" ||
		got.MakeConf.Options[1] != "ccache" ||
		got.MakeConf.Extra != "CFLAGS=\"-O3 -pipe\"" {
		t.Fatalf("makeconf round trip mismatch: %+v", got.MakeConf)
	}
	if !got.Disk.UseSwap || !got.Disk.UseLuks || got.Disk.SwapSize != "8GiB" {
		t.Fatalf("defaults lost: %+v", got.Disk)
	}
}

func TestSaveCreatesParentDirs(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nested", "dir", "custom.toml")
	c := config.Default(true)
	c.System.Hostname = "livehost"
	if err := c.Save(p); err != nil {
		t.Fatalf("Save into missing dirs failed: %v", err)
	}
	re, err := config.Load(p)
	if err != nil {
		t.Fatalf("Load after save failed: %v", err)
	}
	if re.System.Hostname != "livehost" {
		t.Fatalf("round trip mismatch: %+v", re.System)
	}
}

func TestLoadOrDefault(t *testing.T) {
	p := filepath.Join(t.TempDir(), "missing.toml")
	c, existed, err := config.LoadOrDefault(p, true)
	if err != nil || existed {
		t.Fatalf("err=%v existed=%v", err, existed)
	}
	if c.System.Hostname != "gentoo" || c.Disk.BootType != "efi" {
		t.Fatalf("bad defaults: %+v %+v", c.System, c.Disk)
	}
}

func TestResolveSavePath(t *testing.T) {
	if got, want := config.ResolveSavePath("builds/default.toml"), "builds/custom.toml"; got != want {
		t.Fatalf("ResolveSavePath(default) = %q, want %q", got, want)
	}
	if got, want := config.ResolveSavePath("builds/custom.toml"), "builds/custom.toml"; got != want {
		t.Fatalf("ResolveSavePath(custom) = %q, want %q", got, want)
	}
	p := "/etc/gentooinstall/default.toml"
	if got, want := config.ResolveSavePath(p), "/etc/gentooinstall/custom.toml"; got != want {
		t.Fatalf("ResolveSavePath(absolute default) = %q, want %q", got, want)
	}
	other := "/tmp/something.toml"
	if got := config.ResolveSavePath(other); got != other {
		t.Fatalf("ResolveSavePath(other) = %q, want %q", got, other)
	}
}

func TestBuildTemplates(t *testing.T) {
	// Every shipped template must parse, validate, and round-trip losslessly.
	// Scan builds/*.toml so newly added templates are covered automatically
	// (custom.toml is user-generated and excluded).
	matches, err := filepath.Glob(filepath.Join("..", "builds", "*.toml"))
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		name := filepath.Base(m)
		if name == "custom.toml" {
			continue
		}
		names = append(names, name)
	}
	if len(names) < 4 {
		t.Fatalf("expected several shipped templates, found %d", len(names))
	}
	dir := t.TempDir()
	for _, name := range names {
		p := filepath.Join("..", "builds", name)
		c, err := config.Load(p)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if c.System.Hostname == "" || c.Gentoo.Arch == "" {
			t.Fatalf("%s: template appears empty", name)
		}
		if errs := c.Validate(); len(errs) != 0 {
			t.Fatalf("%s: does not validate: %v", name, errs)
		}
		tmp := filepath.Join(dir, name)
		if err := c.Save(tmp); err != nil {
			t.Fatalf("%s: save: %v", name, err)
		}
		re, err := config.Load(tmp)
		if err != nil {
			t.Fatalf("%s: reparse: %v", name, err)
		}
		if fmt.Sprint(re) != fmt.Sprint(c) {
			t.Fatalf("%s: round trip changed config", name)
		}
	}
}

func TestValidate(t *testing.T) {
	c := config.Default(false)
	c.System.Timezone = "Europe/Berlin"
	if errs := c.Validate(); len(errs) != 0 {
		t.Fatalf("default should validate: %v", errs)
	}

	bad := config.Default(false)
	bad.System.Hostname = "in valid"
	bad.Disk.Scheme = "nope"
	errs := bad.Validate()
	if len(errs) < 2 {
		t.Fatalf("expected errors, got %v", errs)
	}
}

func TestStage3BaseNames(t *testing.T) {
	c := config.Default(false)
	c.Gentoo.Arch = "amd64"
	c.Gentoo.Stage3Variant = "systemd"
	if got := c.Stage3BaseNameFinal(); got != "stage3-amd64-systemd" {
		t.Fatal(got)
	}
	c.Gentoo.Stage3Variant = "x32-openrc"
	if got := c.Stage3BaseNameFinal(); got != "stage3-x32-openrc" {
		t.Fatal(got)
	}
	c.Gentoo.Stage3Variant = "systemd"
	c.Gentoo.Arch = "x86"
	c.Gentoo.Subarch = "i686"
	if got := c.Stage3BaseNameFinal(); got != "stage3-i686-systemd" {
		t.Fatal(got)
	}
}

func TestSystemdDetection(t *testing.T) {
	c := config.Default(false)
	c.Gentoo.Stage3Variant = "openrc"
	if c.UsesSystemd() || c.UsesMusl() {
		t.Fatal("openrc must not be systemd/musl")
	}
	c.Gentoo.Stage3Variant = "musl"
	if c.UsesSystemd() || !c.UsesMusl() {
		t.Fatal("musl detection broken")
	}
}

func TestProfileValidation(t *testing.T) {
	c := config.Default(false)
	c.System.Timezone = "Europe/Berlin"
	c.System.Hostname = "host"
	// Default stage3 variant is "systemd", so use a systemd profile.
	c.Gentoo.Profile = "default/linux/amd64/23.0/desktop/gnome/systemd"
	if errs := c.Validate(); len(errs) != 0 {
		t.Fatalf("valid profile should not error: %v", errs)
	}
	c.Gentoo.Profile = "default/linux/amd64/23.0/does-not-exist"
	if errs := c.Validate(); len(errs) != 1 {
		t.Fatalf("expected 1 error for unknown profile, got %v", errs)
	}
}

func TestProfileVariantInitMismatch(t *testing.T) {
	newCfg := func(variant, profile string) *config.Config {
		c := config.Default(false)
		c.System.Timezone = "Europe/Berlin"
		c.System.Hostname = "host"
		c.Gentoo.Stage3Variant = variant
		c.Gentoo.Profile = profile
		return c
	}

	// systemd stage3 + OpenRC profile => mismatch.
	c := newCfg("systemd", "default/linux/amd64/23.0/desktop")
	if errs := c.Validate(); len(errs) != 1 {
		t.Fatalf("expected 1 init-mismatch error, got %v", errs)
	}

	// OpenRC stage3 + systemd profile => mismatch.
	c = newCfg("openrc", "default/linux/amd64/23.0/systemd")
	if errs := c.Validate(); len(errs) != 1 {
		t.Fatalf("expected 1 init-mismatch error, got %v", errs)
	}

	// systemd stage3 + systemd profile => clean.
	c = newCfg("systemd", "default/linux/amd64/23.0/desktop/gnome/systemd")
	if errs := c.Validate(); len(errs) != 0 {
		t.Fatalf("matching init/profiles should not error: %v", errs)
	}

	// desktop-openrc stage3 + desktop (OpenRC) profile => clean.
	c = newCfg("desktop-openrc", "default/linux/amd64/23.0/desktop")
	if errs := c.Validate(); len(errs) != 0 {
		t.Fatalf("matching desktop/OpenRC should not error: %v", errs)
	}

	// no profile selected => no mismatch check.
	c = newCfg("systemd", "")
	if errs := c.Validate(); len(errs) != 0 {
		t.Fatalf("no profile should not trigger mismatch: %v", errs)
	}
}

func TestProfileVariantInitSkipExotic(t *testing.T) {
	// Exotic variants have no catalog profile counterpart, so any profile is
	// allowed without a mismatch error.
	c := config.Default(false)
	c.System.Timezone = "Europe/Berlin"
	c.System.Hostname = "host"
	c.Gentoo.Stage3Variant = "musl"
	c.Gentoo.Profile = "default/linux/amd64/23.0/desktop/systemd"
	if errs := c.Validate(); len(errs) != 0 {
		t.Fatalf("exotic variant should skip mismatch check: %v", errs)
	}
}

func TestAdvisories(t *testing.T) {
	c := config.Default(false)
	c.System.Timezone = "Europe/Berlin"
	c.System.Hostname = "host"

	// No profile selected -> no advisories.
	if warns := c.Advisories(); len(warns) != 0 {
		t.Fatalf("expected no advisories, got %v", warns)
	}

	// Desktop stage3 + non-desktop profile => advisory, but not a blocking error.
	c.Gentoo.Stage3Variant = "desktop-systemd"
	c.Gentoo.Profile = "default/linux/amd64/23.0/systemd"
	if warns := c.Advisories(); len(warns) != 1 {
		t.Fatalf("expected 1 advisory, got %v", warns)
	}
	if errs := c.Validate(); len(errs) != 0 {
		t.Fatalf("advisory must not block: %v", errs)
	}

	// Non-desktop stage3 + desktop profile => advisory.
	c.Gentoo.Stage3Variant = "systemd"
	c.Gentoo.Profile = "default/linux/amd64/23.0/desktop/gnome"
	if warns := c.Advisories(); len(warns) != 1 {
		t.Fatalf("expected 1 advisory, got %v", warns)
	}

	// Aligned => no advisory.
	c.Gentoo.Stage3Variant = "desktop-systemd"
	c.Gentoo.Profile = "default/linux/amd64/23.0/desktop/gnome/systemd"
	if warns := c.Advisories(); len(warns) != 0 {
		t.Fatalf("expected no advisories for aligned config, got %v", warns)
	}
}

func TestProfileHelpers(t *testing.T) {
	if got := config.ProfileDesc("default/linux/amd64/23.0/desktop/gnome/systemd"); got != "GNOME desktop (systemd)" {
		t.Fatalf("ProfileDesc = %q", got)
	}
	if got := config.ProfileDesc("bogus"); got != "" {
		t.Fatalf("ProfileDesc unknown = %q", got)
	}
	if !config.ProfileUsesSystemd("default/linux/amd64/23.0/systemd") {
		t.Fatal("systemd profile must be detected")
	}
	if config.ProfileUsesSystemd("default/linux/amd64/23.0/desktop") {
		t.Fatal("OpenRC profile must not be detected as systemd")
	}
}

func TestProfilePackages(t *testing.T) {
	c := config.Default(true)
	if got := c.ProfilePackages(); got != nil {
		t.Fatalf("unset profile should have no packages, got %v", got)
	}

	c.Gentoo.Profile = "default/linux/amd64/23.0/desktop/gnome"
	got := c.ProfilePackages()
	want := "gnome-base/gnome"
	if !contains(got, want) {
		t.Fatalf("profile packages %v should include %q", got, want)
	}

	c.Gentoo.Profile = "default/linux/amd64/23.0/desktop/kde"
	got = c.ProfilePackages()
	if !contains(got, "kde-plasma/plasma-meta") {
		t.Fatalf("kde profile packages %v missing plasma-meta", got)
	}

	// Unknown profiles resolve to no packages (no panic).
	c.Gentoo.Profile = "bogus/profile"
	if got := c.ProfilePackages(); got != nil {
		t.Fatalf("unknown profile should have no packages, got %v", got)
	}
}

func TestRepoPackages(t *testing.T) {
	// The gentoo repo is always included, regardless of enabled overlays.
	base := config.RepoPackages(nil)
	if !contains(base, "app-editors/vim") || !contains(base, "sys-apps/htop") {
		t.Fatalf("gentoo repo packages missing entries: %v", base)
	}

	// Enabling guru adds its curated packages.
	withGuru := config.RepoPackages([]string{"guru"})
	if !contains(withGuru, "app-misc/fastfetch") {
		t.Fatalf("guru packages missing fastfetch: %v", withGuru)
	}
	if len(withGuru) <= len(base) {
		t.Fatalf("enabling guru should add packages: base=%d guru=%d", len(base), len(withGuru))
	}

	// Unknown overlays are ignored without panicking.
	withUnknown := config.RepoPackages([]string{"nope"})
	if len(withUnknown) != len(base) {
		t.Fatalf("unknown overlay should not change catalog: %v", withUnknown)
	}

	if o := config.LookupOverlay("guru"); o == nil || o.Name != "guru" {
		t.Fatalf("LookupOverlay(guru) = %v", o)
	}
	if o := config.LookupOverlay("missing"); o != nil {
		t.Fatalf("LookupOverlay(missing) = %v", o)
	}
}

func TestEmbeddedRepoLists(t *testing.T) {
	// Every known repo must ship an embedded static package list so the
	// picker is never empty. Verify parsing and sorting.
	names := []string{"gentoo", "guru", "kde", "cachyos", "librewolf"}
	for _, name := range names {
		if !pkglists.Has(name) {
			t.Fatalf("no embedded package list for repo %q", name)
		}
		atoms := pkglists.Atoms(name)
		if len(atoms) == 0 {
			t.Fatalf("repo %q embedded list is empty", name)
		}
		if !isSorted(atoms) {
			t.Fatalf("repo %q atoms not sorted: %v", name, atoms)
		}
	}

	// An unknown repo has no list and yields no atoms.
	if pkglists.Has("missing-repo") {
		t.Fatal("Has(missing-repo) should be false")
	}
	if got := pkglists.Atoms("missing-repo"); got != nil {
		t.Fatalf("Atoms(missing-repo) = %v, want nil", got)
	}
}

func isSorted(xs []string) bool {
	for i := 1; i < len(xs); i++ {
		if xs[i-1] > xs[i] {
			return false
		}
	}
	return true
}

func TestMakeConfCatalog(t *testing.T) {
	if len(config.MakeConfOptions) == 0 {
		t.Fatal("make.conf option catalog is empty")
	}
	o := config.LookupMakeConfOption("jobs")
	if o == nil || o.Key != "jobs" || o.Line == "" {
		t.Fatalf("LookupMakeConfOption(jobs) = %v", o)
	}
	if got := config.LookupMakeConfOption("missing"); got != nil {
		t.Fatalf("LookupMakeConfOption(missing) = %v", got)
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
