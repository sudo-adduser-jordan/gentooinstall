// Minimum terminal size gate of the TUI.
package tests

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"gentooinstall/internal/config"
	"gentooinstall/internal/tui"
)

func runewidthWidth(s string) int { return lipgloss.Width(s) }

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
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model, ok := mm.(*tui.Model)
	if !ok {
		t.Fatal("model type")
	}
	out := model.View()
	if !strings.HasPrefix(out, "╭") {
		t.Fatalf("default render does not open the window frame: %q", out)
	}
	if !strings.Contains(out, "💾 Disk") {
		t.Fatalf("default render missing tab bar: %q", out)
	}
}

// TestTuiNarrowSerialFits locks the qemu -nographic layout: at 80x24 (and
// with unknown size, which falls back to 80x24) every rendered line fits
// the terminal and the active tab stays visible instead of clipped off.
func TestTuiNarrowSerialFits(t *testing.T) {
	for _, size := range []tea.WindowSizeMsg{
		{Width: 80, Height: 24},
		{Width: 0, Height: 0},
	} {
		m := tui.New(config.Default(true), "/tmp/test-gentoo.toml")
		var model *tui.Model
		if size.Width != 0 {
			mm, _ := m.Update(size)
			var ok bool
			model, ok = mm.(*tui.Model)
			if !ok {
				t.Fatal("model type")
			}
		} else {
			model = m
		}
		// The compact strip fits all six tabs (numbered labels keep the
		// 1-6 mapping visible); no tab may be replaced by an ellipsis.
		out := model.View()
		for _, want := range []string{"1 Disk", "6 Install"} {
			if !strings.Contains(out, want) {
				t.Fatalf("size %+v compact tab strip missing %q:\n%s", size, want, out)
			}
		}
		// Rules and rows span the pane instead of leaving the window blank.
		ruled := false
		for _, ln := range strings.Split(out, "\n") {
			if strings.Contains(ln, "Partitioning") && strings.Contains(ln, "─") {
				if w := runewidthWidth(ln); w < 68 {
					t.Fatalf("size %+v rule does not span the window (width %d): %q",
						size, w, ln)
				}
				ruled = true
			}
		}
		if !ruled {
			t.Fatalf("size %+v: no spanning Partitioning rule found", size)
		}
		// Walk every tab; each must fit the terminal.
		for tab := 0; tab < 6; tab++ {
			mm, _ := model.Update(keyRunes(rune('1' + tab)))
			model, _ = mm.(*tui.Model)
			out := model.View()
			if strings.Contains(out, "Terminal too small") {
				t.Fatalf("size %+v wrongly gated as too small", size)
			}
			limit := size.Width
			if limit == 0 {
				limit = 80
			}
			for i, ln := range strings.Split(out, "\n") {
				if w := runewidthWidth(ln); w > limit {
					t.Fatalf("size %+v tab %d line %d width %d exceeds %d: %q",
						size, tab+1, i, w, limit, ln)
				}
			}
		}
	}
}
