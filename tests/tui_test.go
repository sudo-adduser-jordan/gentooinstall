package tests

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"gentooinstall/internal/config"
	"gentooinstall/internal/tui"
)

func keyRunes(r ...rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: r} }
func keyEnter() tea.KeyMsg          { return tea.KeyMsg{Type: tea.KeyEnter} }
func keyDown() tea.KeyMsg           { return tea.KeyMsg{Type: tea.KeyDown} }
func keyUp() tea.KeyMsg             { return tea.KeyMsg{Type: tea.KeyUp} }
func keyLeft() tea.KeyMsg           { return tea.KeyMsg{Type: tea.KeyLeft} }

// rowDownN advances the cursor n rows down, returning the updated model.
func rowDownN(m *tui.Model, n int) *tui.Model {
	for i := 0; i < n; i++ {
		mm, _ := m.Update(keyDown())
		m = mm.(*tui.Model)
	}
	return m
}

func TestTuiTabSwitchAndEdit(t *testing.T) {
	cfg := config.Default(true)
	m := tui.New(cfg, "/tmp/test-gentoo.toml")

	// Switch to tab 2 (System).
	mm, _ := m.Update(keyRunes('2'))
	model, ok := mm.(*tui.Model)
	if !ok {
		t.Fatal("model type")
	}
	if model.ActiveTab() != 1 {
		t.Fatalf("active tab = %d, want 1", model.ActiveTab())
	}

	if model.Dirty() {
		t.Fatal("must not be dirty before edits")
	}

	// First row on System tab is Hostname; open the editor.
	mm, _ = model.Update(keyEnter())
	model = mm.(*tui.Model)
	// Type a hostname and confirm. The editor opens pre-filled, so clear it.
	for range model.Config().System.Hostname {
		mm, _ = model.Update(tea.KeyMsg{Type: tea.KeyBackspace})
		model = mm.(*tui.Model)
	}
	for _, r := range "testhost" {
		mm, _ = model.Update(keyRunes(r))
		model = mm.(*tui.Model)
	}
	mm, _ = model.Update(keyEnter())
	model = mm.(*tui.Model)

	if !model.Dirty() {
		t.Fatal("editing must mark config dirty")
	}
	if got := model.Config().System.Hostname; got != "testhost" {
		t.Fatalf("hostname = %q, want testhost", got)
	}
}

func TestTuiToggleAndHelp(t *testing.T) {
	cfg := config.Default(true)
	m := tui.New(cfg, "/tmp/test-gentoo.toml")

	// Tab 5 (Packages): first row toggles Enable sshd.
	mm, _ := m.Update(keyRunes('5'))
	model := mm.(*tui.Model)
	before := model.Config().Packages.EnableSSHD
	mm, _ = model.Update(keyEnter()) // toggle in place
	model = mm.(*tui.Model)
	if model.Config().Packages.EnableSSHD == before {
		t.Fatal("toggle had no effect")
	}

	// Help overlay opens and closes with any key.
	mm, _ = model.Update(tea.KeyMsg{Type: tea.KeyUp}) // move to next row
	model = mm.(*tui.Model)
	mm, _ = model.Update(keyRunes('?'))
	model = mm.(*tui.Model)
	mm, _ = model.Update(keyEnter()) // dismiss
	_ = mm
}

func TestTuiProfileSelect(t *testing.T) {
	cfg := config.Default(true)
	m := tui.New(cfg, "/tmp/test-gentoo.toml")

	// Tab 4 (Gentoo). The Profile field is the second row.
	mm, _ := m.Update(keyRunes('4'))
	model := mm.(*tui.Model)
	mm, _ = model.Update(keyDown()) // move to Profile
	model = mm.(*tui.Model)
	mm, _ = model.Update(keyEnter()) // open the picker
	model = mm.(*tui.Model)

	profile := "default/linux/amd64/23.0/desktop/gnome"
	// Type the profile in the filter, then Enter to select.
	for _, r := range profile {
		mm, _ = model.Update(keyRunes(r))
		model = mm.(*tui.Model)
	}
	mm, _ = model.Update(keyEnter())
	model = mm.(*tui.Model)

	if model.Config().Gentoo.Profile != profile {
		t.Fatalf("profile = %q, want %q", model.Config().Gentoo.Profile, profile)
	}
	if !model.Dirty() {
		t.Fatal("selecting a profile must mark config dirty")
	}
}

func TestTuiReadOnlyProfileRows(t *testing.T) {
	cfg := config.Default(true)
	cfg.Gentoo.Profile = "default/linux/amd64/23.0/desktop/gnome"
	m := tui.New(cfg, "/tmp/test-gentoo.toml")

	// Tab 5 (Packages).
	mm, _ := m.Update(keyRunes('5'))
	model := mm.(*tui.Model)

	view := model.View()
	// The 'Selected profile' row shows the friendly description, not the path.
	if !strings.Contains(view, "GNOME desktop (OpenRC)") {
		t.Fatalf("Packages view should show friendly profile name, got:\n%s", view)
	}
	// 'Installed by profile' shows a package count instead of the concat list.
	if !strings.Contains(view, "packages") {
		t.Fatalf("Packages view should show an installed-by-profile count, got:\n%s", view)
	}
	if strings.Contains(view, "x11-base/xorg-server") {
		t.Fatalf("Packages view should not expose the concat package list, got:\n%s", view)
	}

	// Navigate down to the 'Installed by profile' row (visible pos 11)
	// and open the package-list modal.
	model = rowDownN(model, 11)
	mm, _ = model.Update(keyEnter())
	model = mm.(*tui.Model)

	modal := model.View()
	if !strings.Contains(modal, "gnome-base/gnome") {
		t.Fatalf("modal should list profile packages (incl. gnome-base/gnome), got:\n%s", modal)
	}

	// The modal must not change the configuration.
	if model.Config().Gentoo.Profile != cfg.Gentoo.Profile {
		t.Fatal("opening the package modal must not modify config")
	}
}

func TestTuiSelectedProfileOpensPicker(t *testing.T) {
	cfg := config.Default(true)
	m := tui.New(cfg, "/tmp/test-gentoo.toml")

	// Tab 5 (Packages).
	mm, _ := m.Update(keyRunes('5'))
	model := mm.(*tui.Model)

	// Navigate down to the 'Selected profile' row (visible pos 10,
	// after the two make.conf rows and the USE flags row were added) and open the picker.
	model = rowDownN(model, 10)
	mm, _ = model.Update(keyEnter())
	model = mm.(*tui.Model)

	profile := "default/linux/amd64/23.0/desktop/kde"
	for _, r := range profile {
		mm, _ = model.Update(keyRunes(r))
		model = mm.(*tui.Model)
	}
	mm, _ = model.Update(keyEnter())
	model = mm.(*tui.Model)

	if model.Config().Gentoo.Profile != profile {
		t.Fatalf("profile = %q, want %q", model.Config().Gentoo.Profile, profile)
	}
	if !model.Dirty() {
		t.Fatal("selecting via the Packages-tab row must mark config dirty")
	}
}

func TestTuiProfileModalShowsFriendlyLabel(t *testing.T) {
	cfg := config.Default(true)
	m := tui.New(cfg, "/tmp/test-gentoo.toml")

	// Tab 4 (Gentoo). The Profile field is the second row.
	mm, _ := m.Update(keyRunes('4'))
	model := mm.(*tui.Model)
	mm, _ = model.Update(keyDown()) // move to Profile
	model = mm.(*tui.Model)
	mm, _ = model.Update(keyEnter()) // open the picker
	model = mm.(*tui.Model)

	// The picker rows should show the friendly description, not the full path.
	view := model.View()
	if !strings.Contains(view, "desktop/systemd") && !strings.Contains(view, "Desktop") {
		t.Fatalf("profile picker should list profiles, got:\n%s", view)
	}
	if !strings.Contains(view, "GNOME desktop") {
		t.Fatalf("profile picker should show friendly label, got:\n%s", view)
	}
}

func TestTuiReposAndPackagesPickers(t *testing.T) {
	cfg := config.Default(true)
	m := tui.New(cfg, "/tmp/test-gentoo.toml")

	// Tab 5 (Packages).
	mm, _ := m.Update(keyRunes('5'))
	model := mm.(*tui.Model)

	// Navigate to 'Enable repositories/overlays' (visible pos 4) and open it.
	model = rowDownN(model, 4)
	mm, _ = model.Update(keyEnter())
	model = mm.(*tui.Model)

	// Filter for guru and toggle it on.
	for _, r := range "guru" {
		mm, _ = model.Update(keyRunes(r))
		model = mm.(*tui.Model)
	}
	mm, _ = model.Update(tea.KeyMsg{Type: tea.KeySpace})
	model = mm.(*tui.Model)
	mm, _ = model.Update(keyEnter()) // apply
	model = mm.(*tui.Model)

	if len(model.Config().Packages.EnablingRepos) != 1 ||
		model.Config().Packages.EnablingRepos[0] != "guru" {
		t.Fatalf("enabling repos = %v, want [guru]", model.Config().Packages.EnablingRepos)
	}

	// Navigate to 'Additional packages' (visible pos 5) and open the picker.
	mm, _ = model.Update(keyDown())
	model = mm.(*tui.Model)
	mm, _ = model.Update(keyEnter())
	model = mm.(*tui.Model)

	view := model.View()
	// Keep the gentoo package check on the unfiltered, scrollable list.
	if !strings.Contains(view, "app-editors/vim") {
		t.Fatalf("additional-packages picker should list gentoo package vim, got:\n%s", view)
	}

	// Filter for the guru-only package to confirm enabled-overlay atoms are
	// offered (it may be scrolled out of the visible window otherwise).
	for _, r := range "fastfetch" {
		mm, _ = model.Update(keyRunes(r))
		model = mm.(*tui.Model)
	}
	filtered := model.View()
	if !strings.Contains(filtered, "app-misc/fastfetch") {
		t.Fatalf("filtered picker should list guru package fastfetch, got:\n%s", filtered)
	}
}

func TestTuiMakeConfOptionsPicker(t *testing.T) {
	cfg := config.Default(true)
	m := tui.New(cfg, "/tmp/test-gentoo.toml")

	mm, _ := m.Update(keyRunes('5')) // Packages tab
	model := mm.(*tui.Model)

	// Navigate to 'make.conf options' (visible selectable pos 8).
	model = rowDownN(model, 8)
	mm, _ = model.Update(keyEnter())
	model = mm.(*tui.Model)

	// The picker lists the catalog. Toggle the first option ('jobs').
	if v := model.View(); !strings.Contains(v, "Parallel build jobs") {
		t.Fatalf("make.conf picker should list job option, got:\n%s", v)
	}
	mm, _ = model.Update(tea.KeyMsg{Type: tea.KeySpace})
	model = mm.(*tui.Model)
	mm, _ = model.Update(keyEnter())
	model = mm.(*tui.Model)

	if len(model.Config().MakeConf.Options) != 1 ||
		model.Config().MakeConf.Options[0] != "jobs" {
		t.Fatalf("makeconf options = %v, want [jobs]", model.Config().MakeConf.Options)
	}
}

func TestTuiMakeConfViewer(t *testing.T) {
	cfg := config.Default(true)
	cfg.MakeConf.Options = []string{"jobs", "ccache"}
	cfg.MakeConf.Extra = "CFLAGS=\"-O3 -pipe\""
	m := tui.New(cfg, "/tmp/test-gentoo.toml")

	mm, _ := m.Update(keyRunes('5')) // Packages tab
	model := mm.(*tui.Model)

	// Navigate to 'edit make.conf' (visible selectable pos 9).
	model = rowDownN(model, 9)
	mm, _ = model.Update(keyEnter())
	model = mm.(*tui.Model)

	// The viewer shows the effective make.conf content (scrollable, no edit).
	view := model.View()
	if !strings.Contains(view, "make.conf") {
		t.Fatalf("make.conf viewer should render the file, got:\n%s", view)
	}
	if !strings.Contains(view, "CCACHE") && !strings.Contains(view, "ccache") {
		t.Fatalf("make.conf viewer should list selected ccache option, got:\n%s", view)
	}
	if !strings.Contains(view, "CFLAGS") {
		t.Fatalf("make.conf viewer should render the extra block, got:\n%s", view)
	}

	// Closing leaves the config unchanged (view-only).
	mm, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = mm.(*tui.Model)
	if len(model.Config().MakeConf.Options) != 2 {
		t.Fatal("viewer must not modify config")
	}
}

func TestTuiQEMUDeviceFallback(t *testing.T) {
	// Force the "no block devices discovered" case so the picker has to
	// fall back to the QEMU default paths.
	orig := tui.BlockDevices
	tui.BlockDevices = func() []string { return nil }
	t.Cleanup(func() { tui.BlockDevices = orig })

	cfg := config.Default(true) // classic scheme; shows the "└ Device" row
	m := tui.New(cfg, "/tmp/test-gentoo.toml")
	model := m

	// Disk tab is active by default. Rows: Partitioning(section),
	// Partitioning scheme, ├ Boot type, └ Device.
	model = rowDownN(model, 3)
	mm, _ := model.Update(keyEnter()) // open the device picker
	model = mm.(*tui.Model)

	view := model.View()
	if !strings.Contains(view, "/dev/sda") {
		t.Fatalf("device picker should offer the QEMU default /dev/sda, got:\n%s", view)
	}
	if !strings.Contains(view, "/dev/vda") {
		t.Fatalf("device picker should offer the QEMU virtio /dev/vda, got:\n%s", view)
	}

	// The first fallback is /dev/sda; Enter confirms even though the node
	// does not exist in this forced-empty scenario.
	mm, _ = model.Update(keyEnter())
	model = mm.(*tui.Model)

	if got := model.Config().Disk.Device; got != "/dev/sda" {
		t.Fatalf("Disk.Device = %q, want /dev/sda", got)
	}
}
