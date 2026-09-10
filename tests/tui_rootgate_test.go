// Root-required gate of the TUI (lib/tui model.rootOK).
package tests

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"gentooinstall/lib/config"
	"gentooinstall/lib/tui"
)

func TestTuiRootGate(testingT *testing.T) {
	appModel := tui.New(config.Default(true), "/tmp/test-gentoo.toml")
	mm, _ := appModel.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	appModel = mm.(*tui.Model)
	appModel.SetRoot(false)

	out := appModel.View()
	for _, want := range []string{"must be run as root", "Press q to quit"} {
		if !strings.Contains(out, want) {
			testingT.Fatalf("root-required page missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "💾 Disk") {
		testingT.Fatalf("tab UI rendered despite non-root:\n%s", out)
	}

	// Any other key is swallowed; the page stays up.
	mm, _ = appModel.Update(keyEnter())
	if model := mm.(*tui.Model); !strings.Contains(model.View(), "must be run as root") {
		testingT.Fatalf("enter must leave the root-required page up:\n%s", model.View())
	}

	// q quits.
	_, cmd := appModel.Update(keyRunes('q'))
	if cmd == nil {
		testingT.Fatal("q on the root-required page must quit (non-nil command)")
	}
}

func TestTuiRootGateNotTriggeredAsRoot(testingT *testing.T) {
	// Default rootOK is true (tests, demo): the UI renders normally.
	appModel := tui.New(config.Default(true), "/tmp/test-gentoo.toml")
	mm, _ := appModel.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	out := mm.(*tui.Model).View()
	if strings.Contains(out, "must be run as root") {
		testingT.Fatalf("root-gate triggered while rootOK:\n%s", out)
	}
	if !strings.Contains(out, "💾 Disk") {
		testingT.Fatalf("default model should render the tab UI:\n%s", out)
	}
}
