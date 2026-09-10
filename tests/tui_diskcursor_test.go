// Disk-tab cursor highlight regression tests. The Disk tab opens with a
// "Partitioning" section separator as its first field; without the clamp fix
// the cursor rested on the separator, so no row was highlighted until the
// first cursor move.
package tests

import (
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"gentooinstall/lib/config"
	"gentooinstall/lib/tui"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

// cursorMarkerLine returns the first non-empty rendered line carrying the
// cursor marker, with escapes stripped.
func cursorMarkerLine(view string) string {
	for _, line := range strings.Split(view, "\n") {
		if clean := stripANSI(line); strings.Contains(clean, "▌") {
			return clean
		}
	}
	return ""
}

func TestTuiDiskTabCursorStartsOnFirstOption(testingT *testing.T) {
	cfg := config.Default(true) // classic scheme
	appModel := tui.New(cfg, "/tmp/test-gentoo.toml")
	model := appModel

	// The Disk tab is active by default. The cursor must sit on the first
	// selectable row ("Partitioning scheme"), not on the leading separator.
	if marker := cursorMarkerLine(model.View()); !strings.Contains(marker, "Partitioning scheme") {
		testingT.Fatalf("cursor marker should highlight the first option, marker = %q:\n%s",
			marker, stripANSI(model.View()))
	}
}

func TestTuiDiskTabCursorClampedAfterTabSwitch(testingT *testing.T) {
	cfg := config.Default(true)
	appModel := tui.New(cfg, "/tmp/test-gentoo.toml")
	model := appModel

	// Cycle away and back via number keys; the cursor must be re-clamped.
	for _, ch := range []rune{'5', '1'} {
		mm, _ := model.Update(keyRunes(ch))
		model = mm.(*tui.Model)
	}
	if marker := cursorMarkerLine(model.View()); !strings.Contains(marker, "Partitioning scheme") {
		testingT.Fatalf("after tab switch the cursor must highlight the first option, marker = %q:\n%s",
			marker, stripANSI(model.View()))
	}

	// Cycle away and back via tab. The cursor must stay on a selectable row
	// (never stuck on the leading separator), not necessarily row 0.
	model = rowDownN(model, 1) // move cursor off the first row
	for step := 0; step < 6; step++ {
		mm, _ := model.Update(tea.KeyMsg{Type: tea.KeyTab})
		model = mm.(*tui.Model)
	}
	marker := cursorMarkerLine(model.View())
	if marker == "" {
		testingT.Fatalf("after tab cycle the cursor marker must be visible:\n%s",
			stripANSI(model.View()))
	}
	if strings.Contains(marker, "──") {
		testingT.Fatalf("after tab cycle the cursor must not rest on a separator, marker = %q", marker)
	}
}
