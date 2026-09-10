// Package tests hosts all gentooinstall test suites, kept separate from the
// production packages so they exercise only the exported API surface.
package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gentooinstall/lib/config"
	"gentooinstall/lib/pkglists"
)

func classicCfg(dev string, luks, btrfs bool) *config.Config {
	cfg := config.Default(true)
	cfg.Disk.Scheme = config.SchemeClassic
	cfg.Disk.Device = dev
	cfg.Disk.UseLuks = luks
	if btrfs {
		cfg.Disk.RootFS = "btrfs"
	}
	return cfg
}

func TestRoundTrip(testingT *testing.T) {
	dir := testingT.TempDir()
	path := filepath.Join(dir, "default.toml")

	cfg := config.Default(true)
	cfg.System.Hostname = "myhost"
	cfg.System.Locales = []string{"en_US.UTF-8 UTF-8", "de_DE.UTF-8 UTF-8"}
	cfg.Disk.Scheme = config.SchemeZFSCentric
	cfg.Disk.Devices = []string{"/dev/sda", "/dev/sdb"}
	cfg.Gentoo.Stage3Variant = "openrc"
	cfg.Gentoo.Profile = "default/linux/amd64/23.0/desktop/gnome"
	cfg.Packages.Additional = []string{"apps-one/shellcheck", "net-misc/curl"}
	cfg.Packages.CustomPackages = []string{"app-editors/helix", "media-sound/foo"}
	cfg.Packages.EnablingRepos = []string{"guru", "kde"}
	cfg.Packages.KernelType = "source"
	cfg.Packages.KernelDeblob = true
	cfg.Packages.InstallFirmware = false
	cfg.Packages.FirmwareSections = []string{"i915", "intel", "amdgpu"}
	cfg.MakeConf.Options = []string{"jobs", "ccache"}
	cfg.MakeConf.Extra = "CFLAGS=\"-O3 -pipe\""

	if err := cfg.Save(path); err != nil {
		testingT.Fatal(err)
	}
	got, err := config.Load(path)
	if err != nil {
		testingT.Fatal(err)
	}
	if got.System.Hostname != "myhost" ||
		len(got.System.Locales) != 2 ||
		got.Disk.Scheme != config.SchemeZFSCentric ||
		len(got.Disk.Devices) != 2 ||
		got.Gentoo.Stage3Variant != "openrc" ||
		got.Gentoo.Profile != "default/linux/amd64/23.0/desktop/gnome" {
		testingT.Fatalf("round trip mismatch: %+v", got)
	}
	if len(got.Packages.Additional) != 2 ||
		len(got.Packages.CustomPackages) != 2 ||
		len(got.Packages.EnablingRepos) != 2 ||
		got.Packages.KernelType != "source" ||
		!got.Packages.KernelDeblob ||
		got.Packages.InstallFirmware ||
		len(got.Packages.FirmwareSections) != 3 {
		testingT.Fatalf("packages round trip mismatch: %+v", got.Packages)
	}
	if len(got.MakeConf.Options) != 2 ||
		got.MakeConf.Options[0] != "jobs" ||
		got.MakeConf.Options[1] != "ccache" ||
		got.MakeConf.Extra != "CFLAGS=\"-O3 -pipe\"" {
		testingT.Fatalf("makeconf round trip mismatch: %+v", got.MakeConf)
	}
	if !got.Disk.UseSwap || !got.Disk.UseLuks || got.Disk.SwapSize != "8GiB" {
		testingT.Fatalf("defaults lost: %+v", got.Disk)
	}
}

func TestSaveCreatesParentDirs(testingT *testing.T) {
	path := filepath.Join(testingT.TempDir(), "nested", "dir", "custom.toml")
	cfg := config.Default(true)
	cfg.System.Hostname = "livehost"
	if err := cfg.Save(path); err != nil {
		testingT.Fatalf("Save into missing dirs failed: %v", err)
	}
	re, err := config.Load(path)
	if err != nil {
		testingT.Fatalf("Load after save failed: %v", err)
	}
	if re.System.Hostname != "livehost" {
		testingT.Fatalf("round trip mismatch: %+v", re.System)
	}
}

func TestLoadOrDefault(testingT *testing.T) {
	path := filepath.Join(testingT.TempDir(), "missing.toml")
	cfg, existed, err := config.LoadOrDefault(path, true)
	if err != nil || existed {
		testingT.Fatalf("err=%v existed=%v", err, existed)
	}
	if cfg.System.Hostname != "gentoo" || cfg.Disk.BootType != "efi" {
		testingT.Fatalf("bad defaults: %+v %+v", cfg.System, cfg.Disk)
	}
}

func TestResolveSavePath(testingT *testing.T) {
	cases := map[string]string{
		"builds/default.toml":             "builds/custom.toml",
		"builds/custom.toml":              "builds/custom.toml",
		"builds/openrc.toml":              "builds/custom.toml",
		"builds/musl.toml":                "builds/custom.toml",
		"builds/desktop-systemd.toml":     "builds/custom.toml",
		"/root/builds/default.toml":       "/root/builds/custom.toml",
		"/root/builds/btrfs-efi.toml":     "/root/builds/custom.toml",
		"/etc/gentooinstall/default.toml": "/etc/gentooinstall/custom.toml",
	}
	for in, want := range cases {
		if got := config.ResolveSavePath(in); got != want {
			testingT.Fatalf("ResolveSavePath(%q) = %q, want %q", in, got, want)
		}
	}
	// Paths outside a builds/ directory pass through unchanged, custom.toml
	// included, so user-owned files are never rewritten or moved.
	for _, keep := range []string{
		"/tmp/something.toml",
		"/etc/gentooinstall/openrc.toml",
		"/etc/gentooinstall/custom.toml",
	} {
		if got := config.ResolveSavePath(keep); got != keep {
			testingT.Fatalf("ResolveSavePath(%q) = %q, want unchanged", keep, got)
		}
	}
}

func TestSaveReportsUnwritableParent(testingT *testing.T) {
	dir := testingT.TempDir()
	// A regular file in the way of the parent path forces MkdirAll to fail
	// regardless of the running user, and the error must name the directory.
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		testingT.Fatal(err)
	}
	cfg := config.Default(true)
	err := cfg.Save(filepath.Join(blocker, "sub", "custom.toml"))
	if err == nil {
		testingT.Fatal("Save must fail when the parent directory cannot be created")
	}
	if !strings.Contains(err.Error(), blocker) {
		testingT.Fatalf("Save error should name the directory, got %v", err)
	}
}

func TestBuildTemplates(testingT *testing.T) {
	// Every shipped template must parse, validate, and round-trip losslessly.
	// Scan builds/*.toml so newly added templates are covered automatically
	// (custom.toml is user-generated and excluded).
	matches, err := filepath.Glob(filepath.Join("..", "builds", "*.toml"))
	if err != nil {
		testingT.Fatal(err)
	}
	names := make([]string, 0, len(matches))
	for _, match := range matches {
		name := filepath.Base(match)
		if name == "custom.toml" {
			continue
		}
		names = append(names, name)
	}
	if len(names) < 4 {
		testingT.Fatalf("expected several shipped templates, found %d", len(names))
	}
	dir := testingT.TempDir()
	for _, name := range names {
		path := filepath.Join("..", "builds", name)
		cfg, err := config.Load(path)
		if err != nil {
			testingT.Fatalf("%s: %v", name, err)
		}
		if cfg.System.Hostname == "" || cfg.Gentoo.Arch == "" {
			testingT.Fatalf("%s: template appears empty", name)
		}
		if errs := cfg.Validate(); len(errs) != 0 {
			testingT.Fatalf("%s: does not validate: %v", name, errs)
		}
		tmp := filepath.Join(dir, name)
		if err := cfg.Save(tmp); err != nil {
			testingT.Fatalf("%s: save: %v", name, err)
		}
		re, err := config.Load(tmp)
		if err != nil {
			testingT.Fatalf("%s: reparse: %v", name, err)
		}
		if fmt.Sprint(re) != fmt.Sprint(cfg) {
			testingT.Fatalf("%s: round trip changed config", name)
		}
	}
}

func TestValidate(testingT *testing.T) {
	cfg := config.Default(false)
	cfg.System.Timezone = "Europe/Berlin"
	if errs := cfg.Validate(); len(errs) != 0 {
		testingT.Fatalf("default should validate: %v", errs)
	}

	bad := config.Default(false)
	bad.System.Hostname = "in valid"
	bad.Disk.Scheme = "nope"
	errs := bad.Validate()
	if len(errs) < 2 {
		testingT.Fatalf("expected errors, got %v", errs)
	}
}

func TestStage3BaseNames(testingT *testing.T) {
	cfg := config.Default(false)
	cfg.Gentoo.Arch = "amd64"
	cfg.Gentoo.Stage3Variant = "systemd"
	if got := cfg.Stage3BaseNameFinal(); got != "stage3-amd64-systemd" {
		testingT.Fatal(got)
	}
	cfg.Gentoo.Stage3Variant = "x32-openrc"
	if got := cfg.Stage3BaseNameFinal(); got != "stage3-x32-openrc" {
		testingT.Fatal(got)
	}
	cfg.Gentoo.Stage3Variant = "systemd"
	cfg.Gentoo.Arch = "x86"
	cfg.Gentoo.Subarch = "i686"
	if got := cfg.Stage3BaseNameFinal(); got != "stage3-i686-systemd" {
		testingT.Fatal(got)
	}
}

func TestSystemdDetection(testingT *testing.T) {
	cfg := config.Default(false)
	cfg.Gentoo.Stage3Variant = "openrc"
	if cfg.UsesSystemd() || cfg.UsesMusl() {
		testingT.Fatal("openrc must not be systemd/musl")
	}
	cfg.Gentoo.Stage3Variant = "musl"
	if cfg.UsesSystemd() || !cfg.UsesMusl() {
		testingT.Fatal("musl detection broken")
	}
}

func TestProfileValidation(testingT *testing.T) {
	cfg := config.Default(false)
	cfg.System.Timezone = "Europe/Berlin"
	cfg.System.Hostname = "host"
	// Default stage3 variant is "systemd", so use a systemd profile.
	cfg.Gentoo.Profile = "default/linux/amd64/23.0/desktop/gnome/systemd"
	if errs := cfg.Validate(); len(errs) != 0 {
		testingT.Fatalf("valid profile should not error: %v", errs)
	}
	cfg.Gentoo.Profile = "default/linux/amd64/23.0/does-not-exist"
	if errs := cfg.Validate(); len(errs) != 1 {
		testingT.Fatalf("expected 1 error for unknown profile, got %v", errs)
	}
}

func TestProfileVariantInitMismatch(testingT *testing.T) {
	newCfg := func(variant, profile string) *config.Config {
		cfg := config.Default(false)
		cfg.System.Timezone = "Europe/Berlin"
		cfg.System.Hostname = "host"
		cfg.Gentoo.Stage3Variant = variant
		cfg.Gentoo.Profile = profile
		return cfg
	}

	// systemd stage3 + OpenRC profile => mismatch.
	cfg := newCfg("systemd", "default/linux/amd64/23.0/desktop")
	if errs := cfg.Validate(); len(errs) != 1 {
		testingT.Fatalf("expected 1 init-mismatch error, got %v", errs)
	}

	// OpenRC stage3 + systemd profile => mismatch.
	cfg = newCfg("openrc", "default/linux/amd64/23.0/systemd")
	if errs := cfg.Validate(); len(errs) != 1 {
		testingT.Fatalf("expected 1 init-mismatch error, got %v", errs)
	}

	// systemd stage3 + systemd profile => clean.
	cfg = newCfg("systemd", "default/linux/amd64/23.0/desktop/gnome/systemd")
	if errs := cfg.Validate(); len(errs) != 0 {
		testingT.Fatalf("matching init/profiles should not error: %v", errs)
	}

	// desktop-openrc stage3 + desktop (OpenRC) profile => clean.
	cfg = newCfg("desktop-openrc", "default/linux/amd64/23.0/desktop")
	if errs := cfg.Validate(); len(errs) != 0 {
		testingT.Fatalf("matching desktop/OpenRC should not error: %v", errs)
	}

	// no profile selected => no mismatch check.
	cfg = newCfg("systemd", "")
	if errs := cfg.Validate(); len(errs) != 0 {
		testingT.Fatalf("no profile should not trigger mismatch: %v", errs)
	}
}

func TestProfileVariantInitSkipExotic(testingT *testing.T) {
	// Exotic variants have no catalog profile counterpart, so any profile is
	// allowed without a mismatch error.
	cfg := config.Default(false)
	cfg.System.Timezone = "Europe/Berlin"
	cfg.System.Hostname = "host"
	cfg.Gentoo.Stage3Variant = "musl"
	cfg.Gentoo.Profile = "default/linux/amd64/23.0/desktop/systemd"
	if errs := cfg.Validate(); len(errs) != 0 {
		testingT.Fatalf("exotic variant should skip mismatch check: %v", errs)
	}
}

func TestAdvisories(testingT *testing.T) {
	cfg := config.Default(false)
	cfg.System.Timezone = "Europe/Berlin"
	cfg.System.Hostname = "host"

	// No profile selected -> no advisories.
	if warns := cfg.Advisories(); len(warns) != 0 {
		testingT.Fatalf("expected no advisories, got %v", warns)
	}

	// Desktop stage3 + non-desktop profile => advisory, but not a blocking error.
	cfg.Gentoo.Stage3Variant = "desktop-systemd"
	cfg.Gentoo.Profile = "default/linux/amd64/23.0/systemd"
	if warns := cfg.Advisories(); len(warns) != 1 {
		testingT.Fatalf("expected 1 advisory, got %v", warns)
	}
	if errs := cfg.Validate(); len(errs) != 0 {
		testingT.Fatalf("advisory must not block: %v", errs)
	}

	// Non-desktop stage3 + desktop profile => advisory.
	cfg.Gentoo.Stage3Variant = "systemd"
	cfg.Gentoo.Profile = "default/linux/amd64/23.0/desktop/gnome"
	if warns := cfg.Advisories(); len(warns) != 1 {
		testingT.Fatalf("expected 1 advisory, got %v", warns)
	}

	// Aligned => no advisory.
	cfg.Gentoo.Stage3Variant = "desktop-systemd"
	cfg.Gentoo.Profile = "default/linux/amd64/23.0/desktop/gnome/systemd"
	if warns := cfg.Advisories(); len(warns) != 0 {
		testingT.Fatalf("expected no advisories for aligned config, got %v", warns)
	}
}

func TestProfileHelpers(testingT *testing.T) {
	if got := config.ProfileDesc("default/linux/amd64/23.0/desktop/gnome/systemd"); got != "GNOME desktop (systemd)" {
		testingT.Fatalf("ProfileDesc = %q", got)
	}
	if got := config.ProfileDesc("bogus"); got != "" {
		testingT.Fatalf("ProfileDesc unknown = %q", got)
	}
	if !config.ProfileUsesSystemd("default/linux/amd64/23.0/systemd") {
		testingT.Fatal("systemd profile must be detected")
	}
	if config.ProfileUsesSystemd("default/linux/amd64/23.0/desktop") {
		testingT.Fatal("OpenRC profile must not be detected as systemd")
	}
}

func TestProfilePackages(testingT *testing.T) {
	cfg := config.Default(true)
	if got := cfg.ProfilePackages(); got != nil {
		testingT.Fatalf("unset profile should have no packages, got %v", got)
	}

	cfg.Gentoo.Profile = "default/linux/amd64/23.0/desktop/gnome"
	got := cfg.ProfilePackages()
	want := "gnome-base/gnome"
	if !contains(got, want) {
		testingT.Fatalf("profile packages %v should include %q", got, want)
	}

	cfg.Gentoo.Profile = "default/linux/amd64/23.0/desktop/kde"
	got = cfg.ProfilePackages()
	if !contains(got, "kde-plasma/plasma-meta") {
		testingT.Fatalf("kde profile packages %v missing plasma-meta", got)
	}

	// Unknown profiles resolve to no packages (no panic).
	cfg.Gentoo.Profile = "bogus/profile"
	if got := cfg.ProfilePackages(); got != nil {
		testingT.Fatalf("unknown profile should have no packages, got %v", got)
	}
}

func TestRepoPackages(testingT *testing.T) {
	// The gentoo repo is always included, regardless of enabled overlays.
	base := config.RepoPackages(nil)
	if !contains(base, "app-editors/vim") || !contains(base, "sys-apps/htop") {
		testingT.Fatalf("gentoo repo packages missing entries: %v", base)
	}

	// Enabling guru adds its curated packages.
	withGuru := config.RepoPackages([]string{"guru"})
	if !contains(withGuru, "app-misc/fastfetch") {
		testingT.Fatalf("guru packages missing fastfetch: %v", withGuru)
	}
	if len(withGuru) <= len(base) {
		testingT.Fatalf("enabling guru should add packages: base=%d guru=%d", len(base), len(withGuru))
	}

	// Unknown overlays are ignored without panicking.
	withUnknown := config.RepoPackages([]string{"nope"})
	if len(withUnknown) != len(base) {
		testingT.Fatalf("unknown overlay should not change catalog: %v", withUnknown)
	}

	if overlay := config.LookupOverlay("guru"); overlay == nil || overlay.Name != "guru" {
		testingT.Fatalf("LookupOverlay(guru) = %v", overlay)
	}
	if overlay := config.LookupOverlay("missing"); overlay != nil {
		testingT.Fatalf("LookupOverlay(missing) = %v", overlay)
	}
}

func TestEmbeddedRepoLists(testingT *testing.T) {
	// Every known repo must ship an embedded static package list so the
	// picker is never empty. Verify parsing and sorting.
	names := []string{"gentoo", "guru", "kde", "cachyos", "librewolf"}
	for _, name := range names {
		if !pkglists.Has(name) {
			testingT.Fatalf("no embedded package list for repo %q", name)
		}
		atoms := pkglists.Atoms(name)
		if len(atoms) == 0 {
			testingT.Fatalf("repo %q embedded list is empty", name)
		}
		if !isSorted(atoms) {
			testingT.Fatalf("repo %q atoms not sorted: %v", name, atoms)
		}
	}

	// An unknown repo has no list and yields no atoms.
	if pkglists.Has("missing-repo") {
		testingT.Fatal("Has(missing-repo) should be false")
	}
	if got := pkglists.Atoms("missing-repo"); got != nil {
		testingT.Fatalf("Atoms(missing-repo) = %v, want nil", got)
	}
}

func isSorted(xs []string) bool {
	for index := 1; index < len(xs); index++ {
		if xs[index-1] > xs[index] {
			return false
		}
	}
	return true
}

func TestMakeConfCatalog(testingT *testing.T) {
	if len(config.MakeConfOptions) == 0 {
		testingT.Fatal("make.conf option catalog is empty")
	}
	opt := config.LookupMakeConfOption("jobs")
	if opt == nil || opt.Key != "jobs" || opt.Line == "" {
		testingT.Fatalf("LookupMakeConfOption(jobs) = %v", opt)
	}
	if got := config.LookupMakeConfOption("missing"); got != nil {
		testingT.Fatalf("LookupMakeConfOption(missing) = %v", got)
	}
}

func contains(xs []string, str string) bool {
	for _, item := range xs {
		if item == str {
			return true
		}
	}
	return false
}
