package tests

import (
	"strconv"
	"strings"
	"testing"

	"gentooinstall/internal/config"
	"gentooinstall/internal/tui"
)

// estGiB parses "~1.6 GiB" into 1.6 for ordering assertions.
func estGiB(t *testing.T, s string) float64 {
	t.Helper()
	s = strings.TrimPrefix(strings.TrimSpace(s), "~")
	s = strings.Fields(s)[0]
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatalf("bad size string %q: %v", s, err)
	}
	return f
}

func TestEstimateInstallSize(t *testing.T) {
	base := config.Default(true) // systemd minimal, bin kernel, git sync
	s0 := estGiB(t, base.EstimateInstallSize())

	desktop := config.Default(true)
	desktop.Gentoo.Stage3Variant = "desktop-systemd"
	if s := estGiB(t, desktop.EstimateInstallSize()); s <= s0 {
		t.Fatalf("desktop size %v should exceed minimal %v", s, s0)
	}

	gitHistory := config.Default(true)
	gitHistory.Gentoo.PortageGitFullHistory = true
	if s := estGiB(t, gitHistory.EstimateInstallSize()); s <= s0 {
		t.Fatalf("git full history should add size, got %v", s)
	}

	source := config.Default(true)
	source.Packages.KernelType = "source"
	if s := estGiB(t, source.EstimateInstallSize()); s <= s0 {
		t.Fatalf("source kernel should exceed bin kernel, got %v", s)
	}

	extras := config.Default(true)
	extras.Packages.Additional = []string{"a/b", "c/d", "e/f"}
	extras.Packages.CustomPackages = []string{"x/y"}
	if s := estGiB(t, extras.EstimateInstallSize()); s <= s0 {
		t.Fatalf("selected packages should add size, got %v", s)
	}
}

func TestEstimatePackageCount(t *testing.T) {
	base := config.Default(true)
	c0 := base.EstimatePackageCount()

	desktop := config.Default(true)
	desktop.Gentoo.Stage3Variant = "desktop-systemd"
	if c := desktop.EstimatePackageCount(); c <= c0 {
		t.Fatalf("desktop count %d should exceed minimal %d", c, c0)
	}

	// Each modifier is asserted as its own isolated delta so the test does not
	// depend on incidental coupling between defaults (e.g. EnableSSHD).
	profile := config.Default(true)
	profile.Gentoo.Profile = "default/linux/amd64/23.0/desktop/gnome"
	profile.Packages.Additional = nil
	profile.Packages.CustomPackages = nil
	if diff := profile.EstimatePackageCount() - c0; diff != len(config.LookupProfile(profile.Gentoo.Profile).Packages) {
		t.Fatalf("profile-only delta = %d, want %d", diff, len(config.LookupProfile(profile.Gentoo.Profile).Packages))
	}

	additional := config.Default(true)
	additional.Packages.Additional = []string{"a/b", "c/d"}
	if diff := additional.EstimatePackageCount() - c0; diff != 2 {
		t.Fatalf("additional-only delta = %d, want 2", diff)
	}

	custom := config.Default(true)
	custom.Packages.CustomPackages = []string{"x/y"}
	if diff := custom.EstimatePackageCount() - c0; diff != 1 {
		t.Fatalf("custom-only delta = %d, want 1", diff)
	}
}

func TestUseFlagsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := dir + "/gentoo.toml"

	c := config.Default(true)
	c.Packages.UseFlags = []string{
		"dev-libs/openssl -asm",
		"sys-apps/systemd -cryptsetup",
	}
	if err := c.Save(p); err != nil {
		t.Fatal(err)
	}
	got, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Packages.UseFlags) != 2 ||
		got.Packages.UseFlags[0] != "dev-libs/openssl -asm" {
		t.Fatalf("use_flags round trip mismatch: %v", got.Packages.UseFlags)
	}
}

func TestTuiInstallTabShowsStats(t *testing.T) {
	cfg := config.Default(true)
	cfg.Gentoo.Profile = "default/linux/amd64/23.0/desktop/gnome"
	m := tui.New(cfg, "/tmp/test-gentoo.toml")

	mm, _ := m.Update(keyRunes('6')) // Install tab
	model := mm.(*tui.Model)
	view := model.View()
	for _, want := range []string{"Total packages", "Install size", "Profile", "Kernel", "Init"} {
		if !strings.Contains(view, want) {
			t.Fatalf("Install tab should show %q, got:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Estimated install") {
		t.Fatalf("Install tab must not show the section header, got:\n%s", view)
	}
}

func TestTuiPackagesTabHasUseFlags(t *testing.T) {
	cfg := config.Default(true)
	m := tui.New(cfg, "/tmp/test-gentoo.toml")

	mm, _ := m.Update(keyRunes('5')) // Packages tab
	model := mm.(*tui.Model)
	if v := model.View(); !strings.Contains(v, "USE flags (package.use)") {
		t.Fatalf("Packages tab should offer the USE flags field, got:\n%s", v)
	}
}

func TestTuiKernelDeblobSubOption(t *testing.T) {
	bin := config.Default(true)
	m := tui.New(bin, "/tmp/test-gentoo.toml")
	mm, _ := m.Update(keyRunes('5')) // Packages tab
	model := mm.(*tui.Model)
	if v := model.View(); strings.Contains(v, "Deblob kernel") {
		t.Fatalf("deblob sub-option must be hidden for a bin kernel, got:\n%s", v)
	}

	source := config.Default(true)
	source.Packages.KernelType = "source"
	m = tui.New(source, "/tmp/test-gentoo.toml")
	mm, _ = m.Update(keyRunes('5'))
	model = mm.(*tui.Model)
	if v := model.View(); !strings.Contains(v, "Deblob kernel") {
		t.Fatalf("deblob sub-option should appear for a source kernel, got:\n%s", v)
	}

	// Kernel type is visible pos 2; deblob is pos 3. Toggle it on.
	for i := 0; i < 3; i++ {
		mm, _ = model.Update(keyDown())
		model = mm.(*tui.Model)
	}
	mm, _ = model.Update(keyEnter())
	model = mm.(*tui.Model)
	if !model.Config().Packages.KernelDeblob {
		t.Fatal("toggling the deblob row must set KernelDeblob")
	}
	if !model.Dirty() {
		t.Fatal("toggling deblob must mark config dirty")
	}
}
