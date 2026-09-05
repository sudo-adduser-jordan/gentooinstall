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
