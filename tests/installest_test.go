// Install size/count estimates and related TUI tabs.
package tests

import (
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

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

func TestTuiFirmwareSubOption(testingT *testing.T) {
	// On the Packages tab the firmware toggle is always offered unless the
	// deblob path suppresses the whole firmware install.
	toggled := config.Default(true)
	toggled.Packages.InstallFirmware = false
	// Pre-seed one section so the picker does not run host auto-detection:
	// the assertion below counts exactly the rows we toggle.
	toggled.Packages.FirmwareSections = []string{"atmel"}
	appModel := tui.New(toggled, "/tmp/test-gentoo.toml")
	mm, _ := appModel.Update(keyRunes('5')) // Packages tab
	model := mm.(*tui.Model)
	if view := model.View(); !strings.Contains(view, "Install linux-firmware (non-free)") {
		testingT.Fatalf("firmware toggle should appear on the Packages tab, got:\n%s", view)
	}

	// The sections multi-pick is hidden while the firmware toggle is off.
	if view := model.View(); strings.Contains(view, "Firmware sections") {
		testingT.Fatalf("firmware sections must be hidden when install_firmware is off, got:\n%s", view)
	}

	// Turn the toggle back on (visible pos 3) and open the sections picker.
	for step := 0; step < 3; step++ {
		mm, _ = model.Update(keyDown())
		model = mm.(*tui.Model)
	}
	mm, _ = model.Update(keyEnter())
	model = mm.(*tui.Model)
	if !model.Config().Packages.InstallFirmware {
		testingT.Fatal("toggling the firmware row must set InstallFirmware")
	}

	// Sections picker is visible now (pos 4). Open it and filter for intel.
	mm, _ = model.Update(keyDown())
	model = mm.(*tui.Model)
	mm, _ = model.Update(keyEnter())
	model = mm.(*tui.Model)
	for _, ch := range "intel" {
		mm, _ = model.Update(keyRunes(ch))
		model = mm.(*tui.Model)
	}
	filtered := model.View()
	if !strings.Contains(filtered, "(Wi-Fi)") {
		testingT.Fatalf("wifi firmware entries should be hinted, got:\n%s", filtered)
	}

	// Space on the vendor category row selects all of its subdirectories.
	mm, _ = model.Update(tea.KeyMsg{Type: tea.KeySpace})
	model = mm.(*tui.Model)
	mm, _ = model.Update(keyEnter())
	model = mm.(*tui.Model)
	sections := model.Config().Packages.FirmwareSections
	want := []string{"atmel", "e100", "i915", "intel", "isci", "ixp4xx", "xe"}
	sort.Strings(sections)
	sort.Strings(want)
	if !slices.Equal(sections, want) {
		testingT.Fatalf("firmware_sections = %v, want %v", sections, want)
	}

	// Reopen and toggle a single directory on top of the category selection.
	mm, _ = model.Update(keyEnter())
	model = mm.(*tui.Model)
	for _, ch := range "amdgpu" {
		mm, _ = model.Update(keyRunes(ch))
		model = mm.(*tui.Model)
	}
	mm, _ = model.Update(tea.KeyMsg{Type: tea.KeySpace})
	model = mm.(*tui.Model)
	mm, _ = model.Update(keyEnter())
	model = mm.(*tui.Model)
	sections = model.Config().Packages.FirmwareSections
	if !slices.Contains(sections, "amdgpu") || len(sections) != 8 {
		testingT.Fatalf("picked single dir must mix with the category, got: %v", sections)
	}

	// Firmware section selection implies the toggle stays on and marks dirty.
	if model.Config().Packages.InstallFirmware != true {
		testingT.Fatal("picking firmware sections must leave install_firmware on")
	}
	if !model.Dirty() {
		testingT.Fatal("picking firmware sections must mark config dirty")
	}
}

func TestTuiFirmwareHiddenWithDeblob(testingT *testing.T) {
	cfg := config.Default(true)
	cfg.Packages.KernelType = "source"
	cfg.Packages.KernelDeblob = true
	appModel := tui.New(cfg, "/tmp/test-gentoo.toml")
	mm, _ := appModel.Update(keyRunes('5')) // Packages tab
	model := mm.(*tui.Model)
	view := model.View()
	if strings.Contains(view, "Install linux-firmware") || strings.Contains(view, "Firmware sections") {
		testingT.Fatalf("firmware fields must be hidden when deblob is on, got:\n%s", view)
	}
}
