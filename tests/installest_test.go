// Install size/count estimates and related TUI tabs.
package tests

import (
	"strconv"
	"strings"
	"testing"

	"gentooinstall/lib/config"
	"gentooinstall/lib/tui"
)

// estGiB parses "~1.6 GiB" into 1.6 for ordering assertions.
func estGiB(testingT *testing.T, sizeStr string) float64 {
	testingT.Helper()
	sizeStr = strings.TrimPrefix(strings.TrimSpace(sizeStr), "~")
	sizeStr = strings.Fields(sizeStr)[0]
	size, err := strconv.ParseFloat(sizeStr, 64)
	if err != nil {
		testingT.Fatalf("bad size string %q: %v", sizeStr, err)
	}
	return size
}

func TestEstimateInstallSize(testingT *testing.T) {
	base := config.Default(true) // systemd minimal, bin kernel, git sync
	s0 := estGiB(testingT, base.EstimateInstallSize())

	desktop := config.Default(true)
	desktop.Gentoo.Stage3Variant = "desktop-systemd"
	if size := estGiB(testingT, desktop.EstimateInstallSize()); size <= s0 {
		testingT.Fatalf("desktop size %v should exceed minimal %v", size, s0)
	}

	gitHistory := config.Default(true)
	gitHistory.Gentoo.PortageGitFullHistory = true
	if size := estGiB(testingT, gitHistory.EstimateInstallSize()); size <= s0 {
		testingT.Fatalf("git full history should add size, got %v", size)
	}

	source := config.Default(true)
	source.Packages.KernelType = "source"
	if size := estGiB(testingT, source.EstimateInstallSize()); size <= s0 {
		testingT.Fatalf("source kernel should exceed bin kernel, got %v", size)
	}

	extras := config.Default(true)
	extras.Packages.Additional = []string{"a/b", "c/d", "e/f"}
	extras.Packages.CustomPackages = []string{"x/y"}
	if size := estGiB(testingT, extras.EstimateInstallSize()); size <= s0 {
		testingT.Fatalf("selected packages should add size, got %v", size)
	}
}

func TestEstimatePackageCount(testingT *testing.T) {
	base := config.Default(true)
	c0 := base.EstimatePackageCount()

	desktop := config.Default(true)
	desktop.Gentoo.Stage3Variant = "desktop-systemd"
	if count := desktop.EstimatePackageCount(); count <= c0 {
		testingT.Fatalf("desktop count %d should exceed minimal %d", count, c0)
	}

	// Each modifier is asserted as its own isolated delta so the test does not
	// depend on incidental coupling between defaults (e.g. EnableSSHD).
	profile := config.Default(true)
	profile.Gentoo.Profile = "default/linux/amd64/23.0/desktop/gnome"
	profile.Packages.Additional = nil
	profile.Packages.CustomPackages = nil
	if diff := profile.EstimatePackageCount() - c0; diff != len(config.LookupProfile(profile.Gentoo.Profile).Packages) {
		testingT.Fatalf("profile-only delta = %d, want %d", diff, len(config.LookupProfile(profile.Gentoo.Profile).Packages))
	}

	additional := config.Default(true)
	additional.Packages.Additional = []string{"a/b", "c/d"}
	if diff := additional.EstimatePackageCount() - c0; diff != 2 {
		testingT.Fatalf("additional-only delta = %d, want 2", diff)
	}

	custom := config.Default(true)
	custom.Packages.CustomPackages = []string{"x/y"}
	if diff := custom.EstimatePackageCount() - c0; diff != 1 {
		testingT.Fatalf("custom-only delta = %d, want 1", diff)
	}
}

func TestUseFlagsRoundTrip(testingT *testing.T) {
	dir := testingT.TempDir()
	path := dir + "/gentoo.toml"

	cfg := config.Default(true)
	cfg.Packages.UseFlags = []string{
		"dev-libs/openssl -asm",
		"sys-apps/systemd -cryptsetup",
	}
	if err := cfg.Save(path); err != nil {
		testingT.Fatal(err)
	}
	got, err := config.Load(path)
	if err != nil {
		testingT.Fatal(err)
	}
	if len(got.Packages.UseFlags) != 2 ||
		got.Packages.UseFlags[0] != "dev-libs/openssl -asm" {
		testingT.Fatalf("use_flags round trip mismatch: %v", got.Packages.UseFlags)
	}
}

func TestTuiInstallTabShowsStats(testingT *testing.T) {
	cfg := config.Default(true)
	cfg.Gentoo.Profile = "default/linux/amd64/23.0/desktop/gnome"
	appModel := tui.New(cfg, "/tmp/test-gentoo.toml")

	mm, _ := appModel.Update(keyRunes('6')) // Install tab
	model := mm.(*tui.Model)
	view := model.View()
	for _, want := range []string{"Total packages", "Install size", "Profile", "Kernel", "Init"} {
		if !strings.Contains(view, want) {
			testingT.Fatalf("Install tab should show %q, got:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Estimated install") {
		testingT.Fatalf("Install tab must not show the section header, got:\n%s", view)
	}
}

func TestTuiPackagesTabHasUseFlags(testingT *testing.T) {
	cfg := config.Default(true)
	appModel := tui.New(cfg, "/tmp/test-gentoo.toml")

	mm, _ := appModel.Update(keyRunes('5')) // Packages tab
	model := mm.(*tui.Model)
	if view := model.View(); !strings.Contains(view, "USE flags (package.use)") {
		testingT.Fatalf("Packages tab should offer the USE flags field, got:\n%s", view)
	}
}

func TestTuiKernelDeblobSubOption(testingT *testing.T) {
	bin := config.Default(true)
	appModel := tui.New(bin, "/tmp/test-gentoo.toml")
	mm, _ := appModel.Update(keyRunes('5')) // Packages tab
	model := mm.(*tui.Model)
	if view := model.View(); strings.Contains(view, "Deblob kernel") {
		testingT.Fatalf("deblob sub-option must be hidden for a bin kernel, got:\n%s", view)
	}

	source := config.Default(true)
	source.Packages.KernelType = "source"
	appModel = tui.New(source, "/tmp/test-gentoo.toml")
	mm, _ = appModel.Update(keyRunes('5'))
	model = mm.(*tui.Model)
	if view := model.View(); !strings.Contains(view, "Deblob kernel") {
		testingT.Fatalf("deblob sub-option should appear for a source kernel, got:\n%s", view)
	}

	// Kernel type is visible pos 2; deblob is pos 3. Toggle it on.
	for step := 0; step < 3; step++ {
		mm, _ = model.Update(keyDown())
		model = mm.(*tui.Model)
	}
	mm, _ = model.Update(keyEnter())
	model = mm.(*tui.Model)
	if !model.Config().Packages.KernelDeblob {
		testingT.Fatal("toggling the deblob row must set KernelDeblob")
	}
	if !model.Dirty() {
		testingT.Fatal("toggling deblob must mark config dirty")
	}
}
