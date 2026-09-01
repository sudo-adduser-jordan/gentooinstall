package tests

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"gentooinstall/internal/config"
	"gentooinstall/internal/tui"
)

func TestTuiTooSmallGate(t *testing.T) {
	m := tui.New(config.Default(true), "/tmp/test-gentoo.toml")
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 50, Height: 15})
	model, ok := mm.(*tui.Model)
	if !ok {
		t.Fatal("model type")
	}
	out := model.View()
	if !strings.Contains(out, "Terminal too small") {
		t.Fatalf("too-small notice missing from render: %q", out)
	}
	if strings.Contains(out, "💾 Disk") {
		t.Fatalf("tab UI rendered despite too-small terminal: %q", out)
	}
}

func TestTuiDefaultWindowRenders(t *testing.T) {
	m := tui.New(config.Default(true), "/tmp/test-gentoo.toml")
	out := m.View()
	if !strings.HasPrefix(out, "╭") {
		t.Fatalf("default render does not open the window frame: %q", out)
	}
	if !strings.Contains(out, "💾 Disk") {
		t.Fatalf("default render missing tab bar: %q", out)
	}
}

// TestTuiTabBarBadgeFlushRight guards against the LUKS-badge spacer
// leaving the badge stranded mid-screen on wide terminals (as recorded by
// VHS at Set Width 2000 -> ~247 columns). The tab bar spans the full right
// pane: tabs hug the left and the badge is pinned to the right window edge.
func TestTuiTabBarBadgeFlushRight(t *testing.T) {
	m := tui.New(config.Default(true), "/tmp/test-gentoo.toml")
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 240, Height: 45})
	model, ok := mm.(*tui.Model)
	if !ok {
		t.Fatal("model type")
	}
	for _, line := range strings.Split(model.View(), "\n") {
		if !strings.Contains(line, "🚀 Install") {
			continue
		}
		rs := []rune(line)
		lastTab := -1
		for i := range rs {
			if strings.HasPrefix(string(rs[i:]), "🚀 Install") {
				lastTab = i + len("🚀 Install")
			}
		}
		badge := strings.Index(line, "🔒")
		if lastTab < 0 || badge < 0 {
			t.Fatalf("tab/badge markers not found: %q", line)
		}
		// Tabs must hug the left edge of the right pane (not be pushed
		// right by a spacer); the badge must reach the far right.
		if lastTab > 150 {
			t.Fatalf("last tab pushed too far right: col %d, line %q", lastTab, line)
		}
		if len(rs)-badge > 3 {
			t.Fatalf("badge not flush to right edge: badge col %d of %d, line %q", badge, len(rs), line)
		}
		return
	}
	t.Fatalf("no install tab line found in wide render")
}
