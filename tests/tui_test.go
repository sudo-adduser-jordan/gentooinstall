package tests

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"gentooinstall/lib/config"
	"gentooinstall/lib/tui"
)

func keyRunes(ch ...rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: ch} }
func keyEnter() tea.KeyMsg           { return tea.KeyMsg{Type: tea.KeyEnter} }
func keyDown() tea.KeyMsg            { return tea.KeyMsg{Type: tea.KeyDown} }
func keyUp() tea.KeyMsg              { return tea.KeyMsg{Type: tea.KeyUp} }
func keyLeft() tea.KeyMsg            { return tea.KeyMsg{Type: tea.KeyLeft} }

func rowDownN(appModel *tui.Model, count int) *tui.Model {
	for idx := 0; idx < count; idx++ {
		mm, _ := appModel.Update(keyDown())
		appModel = mm.(*tui.Model)
	}
	return appModel
}

func TestTuiTabSwitchAndEdit(testingT *testing.T) {
	cfg := config.Default(true)
	appModel := tui.New(cfg, "/tmp/test-gentoo.toml")

	mm, _ := appModel.Update(keyRunes('2'))
	model, ok := mm.(*tui.Model)
	if !ok {
		testingT.Fatal("model type")
	}
	if model.ActiveTab() != 1 {
		testingT.Fatalf("active tab = %d, want 1", model.ActiveTab())
	}

	if model.Dirty() {
		testingT.Fatal("must not be dirty before edits")
	}

	mm, _ = model.Update(keyEnter())
	model = mm.(*tui.Model)
	for range model.Config().System.Hostname {
		mm, _ = model.Update(tea.KeyMsg{Type: tea.KeyBackspace})
		model = mm.(*tui.Model)
	}
	for _, ch := range "testhost" {
		mm, _ = model.Update(keyRunes(ch))
		model = mm.(*tui.Model)
	}
	mm, _ = model.Update(keyEnter())
	model = mm.(*tui.Model)

	if !model.Dirty() {
		testingT.Fatal("editing must mark config dirty")
	}
	if got := model.Config().System.Hostname; got != "testhost" {
		testingT.Fatalf("hostname = %q, want testhost", got)
	}
}

func TestTuiToggleAndHelp(testingT *testing.T) {
	cfg := config.Default(true)
	appModel := tui.New(cfg, "/tmp/test-gentoo.toml")

	mm, _ := appModel.Update(keyRunes('5'))
	model := mm.(*tui.Model)
	before := model.Config().Packages.EnableSSHD
	mm, _ = model.Update(keyEnter())
	model = mm.(*tui.Model)
	if model.Config().Packages.EnableSSHD == before {
		testingT.Fatal("toggle had no effect")
	}

	mm, _ = model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model = mm.(*tui.Model)
	mm, _ = model.Update(keyRunes('?'))
	model = mm.(*tui.Model)
	mm, _ = model.Update(keyEnter())
	_ = mm
}

func TestTuiProfileSelect(testingT *testing.T) {
	cfg := config.Default(true)
	appModel := tui.New(cfg, "/tmp/test-gentoo.toml")

	mm, _ := appModel.Update(keyRunes('4'))
	model := mm.(*tui.Model)
	mm, _ = model.Update(keyDown())
	model = mm.(*tui.Model)
	mm, _ = model.Update(keyEnter())
	model = mm.(*tui.Model)

	profile := "default/linux/amd64/23.0/desktop/gnome"
	for _, ch := range profile {
		mm, _ = model.Update(keyRunes(ch))
		model = mm.(*tui.Model)
	}
	mm, _ = model.Update(keyEnter())
	model = mm.(*tui.Model)

	if model.Config().Gentoo.Profile != profile {
		testingT.Fatalf("profile = %q, want %q", model.Config().Gentoo.Profile, profile)
	}
	if !model.Dirty() {
		testingT.Fatal("selecting a profile must mark config dirty")
	}
}

func TestTuiReadOnlyProfileRows(testingT *testing.T) {
	cfg := config.Default(true)
	cfg.Gentoo.Profile = "default/linux/amd64/23.0/desktop/gnome"
	appModel := tui.New(cfg, "/tmp/test-gentoo.toml")
	mm, _ := appModel.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	appModel = mm.(*tui.Model)

	mm2, _ := appModel.Update(keyRunes('5'))
	model := mm2.(*tui.Model)

	view := model.View()
	if !strings.Contains(view, "GNOME desktop (OpenRC)") {
		testingT.Fatalf("Packages view should show friendly profile name, got:\n%s", view)
	}
	if !strings.Contains(view, "packages") {
		testingT.Fatalf("Packages view should show an installed-by-profile count, got:\n%s", view)
	}
	if strings.Contains(view, "x11-base/xorg-server") {
		testingT.Fatalf("Packages view should not expose the concat package list, got:\n%s", view)
	}

	model = rowDownN(model, 13)
	mm, _ = model.Update(keyEnter())
	model = mm.(*tui.Model)

	modal := model.View()
	if !strings.Contains(modal, "gnome-base/gnome") {
		testingT.Fatalf("modal should list profile packages (incl. gnome-base/gnome), got:\n%s", modal)
	}

	if model.Config().Gentoo.Profile != cfg.Gentoo.Profile {
		testingT.Fatal("opening the package modal must not modify config")
	}
}

func TestTuiSelectedProfileOpensPicker(testingT *testing.T) {
	cfg := config.Default(true)
	appModel := tui.New(cfg, "/tmp/test-gentoo.toml")

	mm, _ := appModel.Update(keyRunes('5'))
	model := mm.(*tui.Model)

	model = rowDownN(model, 12)
	mm, _ = model.Update(keyEnter())
	model = mm.(*tui.Model)

	profile := "default/linux/amd64/23.0/desktop/kde"
	for _, ch := range profile {
		mm, _ = model.Update(keyRunes(ch))
		model = mm.(*tui.Model)
	}
	mm, _ = model.Update(keyEnter())
	model = mm.(*tui.Model)

	if model.Config().Gentoo.Profile != profile {
		testingT.Fatalf("profile = %q, want %q", model.Config().Gentoo.Profile, profile)
	}
	if !model.Dirty() {
		testingT.Fatal("selecting via the Packages-tab row must mark config dirty")
	}
}

func TestTuiProfileModalShowsFriendlyLabel(testingT *testing.T) {
	cfg := config.Default(true)
	appModel := tui.New(cfg, "/tmp/test-gentoo.toml")

	mm, _ := appModel.Update(keyRunes('4'))
	model := mm.(*tui.Model)
	mm, _ = model.Update(keyDown())
	model = mm.(*tui.Model)
	mm, _ = model.Update(keyEnter())
	model = mm.(*tui.Model)

	view := model.View()
	if !strings.Contains(view, "desktop/systemd") && !strings.Contains(view, "Desktop") {
		testingT.Fatalf("profile picker should list profiles, got:\n%s", view)
	}
	if !strings.Contains(view, "GNOME desktop") {
		testingT.Fatalf("profile picker should show friendly label, got:\n%s", view)
	}
}

func TestTuiReposAndPackagesPickers(testingT *testing.T) {
	cfg := config.Default(true)
	appModel := tui.New(cfg, "/tmp/test-gentoo.toml")

	mm, _ := appModel.Update(keyRunes('5'))
	model := mm.(*tui.Model)

	model = rowDownN(model, 6)
	mm, _ = model.Update(keyEnter())
	model = mm.(*tui.Model)

	for _, ch := range "guru" {
		mm, _ = model.Update(keyRunes(ch))
		model = mm.(*tui.Model)
	}
	mm, _ = model.Update(tea.KeyMsg{Type: tea.KeySpace})
	model = mm.(*tui.Model)
	mm, _ = model.Update(keyEnter())
	model = mm.(*tui.Model)

	if len(model.Config().Packages.EnablingRepos) != 1 ||
		model.Config().Packages.EnablingRepos[0] != "guru" {
		testingT.Fatalf("enabling repos = %v, want [guru]", model.Config().Packages.EnablingRepos)
	}

	mm, _ = model.Update(keyDown())
	model = mm.(*tui.Model)
	mm, _ = model.Update(keyEnter())
	model = mm.(*tui.Model)

	view := model.View()
	if !strings.Contains(view, "app-editors/vim") {
		testingT.Fatalf("additional-packages picker should list gentoo package vim, got:\n%s", view)
	}

	for _, ch := range "fastfetch" {
		mm, _ = model.Update(keyRunes(ch))
		model = mm.(*tui.Model)
	}
	filtered := model.View()
	if !strings.Contains(filtered, "app-misc/fastfetch") {
		testingT.Fatalf("filtered picker should list guru package fastfetch, got:\n%s", filtered)
	}
}

func TestTuiMakeConfOptionsPicker(testingT *testing.T) {
	cfg := config.Default(true)
	appModel := tui.New(cfg, "/tmp/test-gentoo.toml")

	mm, _ := appModel.Update(keyRunes('5'))
	model := mm.(*tui.Model)

	model = rowDownN(model, 10)
	mm, _ = model.Update(keyEnter())
	model = mm.(*tui.Model)

	if viewSnapshot := model.View(); !strings.Contains(viewSnapshot, "Parallel build jobs") {
		testingT.Fatalf("make.conf picker should list job option, got:\n%s", viewSnapshot)
	}
	mm, _ = model.Update(tea.KeyMsg{Type: tea.KeySpace})
	model = mm.(*tui.Model)
	mm, _ = model.Update(keyEnter())
	model = mm.(*tui.Model)

	if len(model.Config().MakeConf.Options) != 1 ||
		model.Config().MakeConf.Options[0] != "jobs" {
		testingT.Fatalf("makeconf options = %v, want [jobs]", model.Config().MakeConf.Options)
	}
}

func TestTuiMakeConfViewer(testingT *testing.T) {
	cfg := config.Default(true)
	cfg.MakeConf.Options = []string{"jobs", "ccache"}
	cfg.MakeConf.Extra = "CFLAGS=\"-O3 -pipe\""
	appModel := tui.New(cfg, "/tmp/test-gentoo.toml")

	mm, _ := appModel.Update(keyRunes('5'))
	model := mm.(*tui.Model)

	model = rowDownN(model, 11)
	mm, _ = model.Update(keyEnter())
	model = mm.(*tui.Model)

	view := model.View()
	if !strings.Contains(view, "make.conf") {
		testingT.Fatalf("make.conf viewer should render the file, got:\n%s", view)
	}
	if !strings.Contains(view, "CCACHE") && !strings.Contains(view, "ccache") {
		testingT.Fatalf("make.conf viewer should list selected ccache option, got:\n%s", view)
	}
	if !strings.Contains(view, "CFLAGS") {
		testingT.Fatalf("make.conf viewer should render the extra block, got:\n%s", view)
	}

	mm, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = mm.(*tui.Model)
	if len(model.Config().MakeConf.Options) != 2 {
		testingT.Fatal("viewer must not modify config")
	}
}

func TestTuiQEMUDeviceFallback(testingT *testing.T) {
	orig := tui.BlockDevices
	tui.BlockDevices = func() []string { return nil }
	testingT.Cleanup(func() { tui.BlockDevices = orig })

	cfg := config.Default(true)
	appModel := tui.New(cfg, "/tmp/test-gentoo.toml")
	model := appModel

	model = rowDownN(model, 2)
	mm, _ := model.Update(keyEnter())
	model = mm.(*tui.Model)

	view := model.View()
	if !strings.Contains(view, "/dev/sda") {
		testingT.Fatalf("device picker should offer the QEMU default /dev/sda, got:\n%s", view)
	}
	if !strings.Contains(view, "/dev/vda") {
		testingT.Fatalf("device picker should offer the QEMU virtio /dev/vda, got:\n%s", view)
	}

	mm, _ = model.Update(keyEnter())
	model = mm.(*tui.Model)

	if got := model.Config().Disk.Device; got != "/dev/sda" {
		testingT.Fatalf("Disk.Device = %q, want /dev/sda", got)
	}
}
