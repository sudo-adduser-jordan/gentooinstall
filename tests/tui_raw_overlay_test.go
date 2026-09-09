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

func openRawOverlay(t *testing.T, m *tui.Model) *tui.Model {
	t.Helper()
	mm, _ := m.Update(keyRunes('l'))
	model := mm.(*tui.Model)
	// Force a render so the dedicated viewport sizes and tails.
	_ = model.View()
	return model
}

func feedLines(t *testing.T, m *tui.Model, n int, prefix string) *tui.Model {
	t.Helper()
	var mm tea.Model = m
	var err error
	for i := 0; i < n; i++ {
		mm, _ = mm.(*tui.Model).Update(tui.InstallLineMsg{Line: fmt.Sprintf("%s %d", prefix, i)})
	}
	_ = err
	return mm.(*tui.Model)
}

func TestRawOverlayFollowsTail(t *testing.T) {
	m, _ := newInstallModel(t)
	mm, _ := m.Update(tui.InstallStartMsg{})
	model := mm.(*tui.Model)
	model = feedLines(t, model, 40, "rawline")
	model = openRawOverlay(t, model)

	view := model.View()
	if !strings.Contains(view, "Raw output") {
		t.Fatal("raw overlay title missing")
	}
	if !strings.Contains(view, "rawline 39") {
		t.Fatalf("latest line must be visible while following, got:\n%s", view)
	}
	if strings.Contains(view, "scrolled") {
		t.Fatal("follow hint must not show scrolled state while at bottom")
	}
}

func TestRawOverlayScrollPausesAndResumes(t *testing.T) {
	m, _ := newInstallModel(t)
	mm, _ := m.Update(tui.InstallStartMsg{})
	model := mm.(*tui.Model)
	model = feedLines(t, model, 40, "scrollline")
	model = openRawOverlay(t, model)

	// Scroll up: pauses follow and shows the resume hint.
	mm, _ = model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model = mm.(*tui.Model)
	view := model.View()
	if !strings.Contains(view, "scrolled") {
		t.Fatalf("scrolling up must pause follow and show resume hint, got:\n%s", view)
	}

	// New output while scrolled up must not yank to the bottom.
	mm, _ = model.Update(tui.InstallLineMsg{Line: "scrollline NEW"})
	model = mm.(*tui.Model)
	view = model.View()
	if !strings.Contains(view, "scrolled") {
		t.Fatal("new output must not resume follow while scrolled up")
	}
	if strings.Contains(view, "scrollline NEW") {
		t.Fatal("newest line must stay hidden while scrolled up")
	}

	// End resumes the tail.
	mm, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnd})
	model = mm.(*tui.Model)
	view = model.View()
	if strings.Contains(view, "scrolled") {
		t.Fatal("End must resume follow")
	}
	if !strings.Contains(view, "scrollline NEW") {
		t.Fatal("newest line must be visible after End resumes tail")
	}
}

func TestRawOverlayKeepsPositionWithoutNewLines(t *testing.T) {
	m, _ := newInstallModel(t)
	mm, _ := m.Update(tui.InstallStartMsg{})
	model := mm.(*tui.Model)
	model = feedLines(t, model, 40, "stableline")
	model = openRawOverlay(t, model)

	mm, _ = model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model = mm.(*tui.Model)
	before := model.View()
	after := model.View()
	if before != after {
		t.Fatal("re-render without new lines must preserve scroll position")
	}
	if !strings.Contains(after, "scrolled") {
		t.Fatal("scroll position lost on re-render")
	}
}

func TestRawOverlayFallbackColors(t *testing.T) {
	m, _ := newInstallModel(t)
	mm, _ := m.Update(tui.InstallStartMsg{})
	model := mm.(*tui.Model)
	mm, _ = model.Update(tui.InstallLineMsg{Line: "$ emerge -v dev-vcs/git"})
	model = mm.(*tui.Model)
	mm, _ = model.Update(tui.InstallLineMsg{Line: "[!] something failed badly"})
	model = mm.(*tui.Model)
	model = openRawOverlay(t, model)

	view := model.View()
	if !strings.Contains(view, "\x1b[") {
		t.Fatal("plain lines must get fallback ANSI colors in the raw window")
	}
	if !strings.Contains(view, "dev-vcs/git") {
		t.Fatal("colorized content must keep the original text")
	}
}

func TestRawOverlayPreservesUpstreamAnsi(t *testing.T) {
	m, _ := newInstallModel(t)
	mm, _ := m.Update(tui.InstallStartMsg{})
	model := mm.(*tui.Model)
	colored := "\x1b[1;32m$ emerge --verbose sys-kernel/gentoo-kernel\x1b[m"
	mm, _ = model.Update(tui.InstallLineMsg{Line: colored})
	model = mm.(*tui.Model)
	model = openRawOverlay(t, model)

	view := model.View()
	if !strings.Contains(view, "\x1b[1;32m") {
		t.Fatal("upstream ANSI must pass through untouched")
	}
}
