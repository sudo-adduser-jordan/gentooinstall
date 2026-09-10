// Host-prerequisite check on the TUI Install tab (lib/tui hostcheck.go).
package tests

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"gentooinstall/lib/config"
	"gentooinstall/lib/tui"
)

func TestTuiHostPrereqOk(testingT *testing.T) {
	appModel := tui.New(config.Default(true), "/tmp/test-gentoo.toml")
	appModel.SetPrereq(func() tui.Prereq { return tui.Prereq{RootOK: true} })
	mm, _ := appModel.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	appModel = mm.(*tui.Model)
	mm, _ = appModel.Update(keyRunes('6')) // Install tab
	model := mm.(*tui.Model)

	out := model.View()
	for _, want := range []string{"Host prerequisites", "running as root",
		"all required programs present"} {
		if !strings.Contains(out, want) {
			testingT.Fatalf("Install tab should show %q, got:\n%s", want, out)
		}
	}
}

func TestTuiHostPrereqMissingAndNotRoot(testingT *testing.T) {
	appModel := tui.New(config.Default(true), "/tmp/test-gentoo.toml")
	appModel.SetPrereq(func() tui.Prereq {
		return tui.Prereq{RootOK: false, MissingPrograms: []string{"ntpd", "sgdisk"}}
	})
	mm, _ := appModel.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	appModel = mm.(*tui.Model)
	mm, _ = appModel.Update(keyRunes('6')) // Install tab
	model := mm.(*tui.Model)

	out := model.View()
	for _, want := range []string{"not running as root", "ntpd", "sgdisk",
		"installer can ask to install"} {
		if !strings.Contains(out, want) {
			testingT.Fatalf("Install tab should flag %q, got:\n%s", want, out)
		}
	}
}

func TestTuiHostPrereqHiddenByDefault(testingT *testing.T) {
	appModel := tui.New(config.Default(true), "/tmp/test-gentoo.toml")
	mm, _ := appModel.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	appModel = mm.(*tui.Model)
	mm, _ = appModel.Update(keyRunes('6')) // Install tab
	model := mm.(*tui.Model)

	if out := model.View(); strings.Contains(out, "Host prerequisites") {
		testingT.Fatalf("no SetPrereq, but the section rendered:\n%s", out)
	}
}
