// Headless tests for the raw output window: tail-follow, scroll pause and
// fallback colors. Exercises only exported identifiers.
package tests

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"gentooinstall/lib/tui"
)

func openRawOverlay(testingT *testing.T, appModel *tui.Model) *tui.Model {
	testingT.Helper()
	mm, _ := appModel.Update(keyRunes('l'))
	model := mm.(*tui.Model)
	// Force a render so the dedicated viewport sizes and tails.
	_ = model.View()
	return model
}

func feedLines(testingT *testing.T, appModel *tui.Model, count int, prefix string) *tui.Model {
	testingT.Helper()
	var mm tea.Model = appModel
	var err error
	for idx := 0; idx < count; idx++ {
		mm, _ = mm.(*tui.Model).Update(tui.InstallLineMsg{Line: fmt.Sprintf("%s %d", prefix, idx)})
	}
	_ = err
	return mm.(*tui.Model)
}

func TestRawOverlayFollowsTail(testingT *testing.T) {
	appModel, _ := newInstallModel(testingT)
	mm, _ := appModel.Update(tui.InstallStartMsg{})
	model := mm.(*tui.Model)
	model = feedLines(testingT, model, 40, "rawline")
	model = openRawOverlay(testingT, model)

	view := model.View()
	if !strings.Contains(view, "Raw output") {
		testingT.Fatal("raw overlay title missing")
	}
	if !strings.Contains(view, "rawline 39") {
		testingT.Fatalf("latest line must be visible while following, got:\n%s", view)
	}
	if strings.Contains(view, "scrolled") {
		testingT.Fatal("follow hint must not show scrolled state while at bottom")
	}
}

func TestRawOverlayScrollPausesAndResumes(testingT *testing.T) {
	appModel, _ := newInstallModel(testingT)
	mm, _ := appModel.Update(tui.InstallStartMsg{})
	model := mm.(*tui.Model)
	model = feedLines(testingT, model, 40, "scrollline")
	model = openRawOverlay(testingT, model)

	// Scroll up: pauses follow and shows the resume hint.
	mm, _ = model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model = mm.(*tui.Model)
	view := model.View()
	if !strings.Contains(view, "scrolled") {
		testingT.Fatalf("scrolling up must pause follow and show resume hint, got:\n%s", view)
	}

	// New output while scrolled up must not yank to the bottom.
	mm, _ = model.Update(tui.InstallLineMsg{Line: "scrollline NEW"})
	model = mm.(*tui.Model)
	view = model.View()
	if !strings.Contains(view, "scrolled") {
		testingT.Fatal("new output must not resume follow while scrolled up")
	}
	if strings.Contains(view, "scrollline NEW") {
		testingT.Fatal("newest line must stay hidden while scrolled up")
	}

	// End resumes the tail.
	mm, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnd})
	model = mm.(*tui.Model)
	view = model.View()
	if strings.Contains(view, "scrolled") {
		testingT.Fatal("End must resume follow")
	}
	if !strings.Contains(view, "scrollline NEW") {
		testingT.Fatal("newest line must be visible after End resumes tail")
	}
}

func TestRawOverlayKeepsPositionWithoutNewLines(testingT *testing.T) {
	appModel, _ := newInstallModel(testingT)
	mm, _ := appModel.Update(tui.InstallStartMsg{})
	model := mm.(*tui.Model)
	model = feedLines(testingT, model, 40, "stableline")
	model = openRawOverlay(testingT, model)

	mm, _ = model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model = mm.(*tui.Model)
	before := model.View()
	after := model.View()
	if before != after {
		testingT.Fatal("re-render without new lines must preserve scroll position")
	}
	if !strings.Contains(after, "scrolled") {
		testingT.Fatal("scroll position lost on re-render")
	}
}

func TestRawOverlayFallbackColors(testingT *testing.T) {
	appModel, _ := newInstallModel(testingT)
	mm, _ := appModel.Update(tui.InstallStartMsg{})
	model := mm.(*tui.Model)
	mm, _ = model.Update(tui.InstallLineMsg{Line: "$ emerge -v dev-vcs/git"})
	model = mm.(*tui.Model)
	mm, _ = model.Update(tui.InstallLineMsg{Line: "[!] something failed badly"})
	model = mm.(*tui.Model)
	model = openRawOverlay(testingT, model)

	view := model.View()
	if !strings.Contains(view, "\x1b[") {
		testingT.Fatal("plain lines must get fallback ANSI colors in the raw window")
	}
	if !strings.Contains(view, "dev-vcs/git") {
		testingT.Fatal("colorized content must keep the original text")
	}
}

func TestRawOverlayPreservesUpstreamAnsi(testingT *testing.T) {
	appModel, _ := newInstallModel(testingT)
	mm, _ := appModel.Update(tui.InstallStartMsg{})
	model := mm.(*tui.Model)
	colored := "\x1b[1;32m$ emerge --verbose sys-kernel/gentoo-kernel\x1b[m"
	mm, _ = model.Update(tui.InstallLineMsg{Line: colored})
	model = mm.(*tui.Model)
	model = openRawOverlay(testingT, model)

	view := model.View()
	if !strings.Contains(view, "\x1b[1;32m") {
		testingT.Fatal("upstream ANSI must pass through untouched")
	}
}
