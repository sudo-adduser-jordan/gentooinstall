// Live winsize detection: CSI reply parsing, clamping and the TUI poll.
package tests

import (
	"testing"

	"gentooinstall/lib/config"
	"gentooinstall/lib/live"
	"gentooinstall/lib/tui"
)

func TestParseCSITextAreaReply(t *testing.T) {
	cases := []struct {
		name string
		in   string
		cols int
		rows int
		ok   bool
	}{
		{"basic", "\x1b[8;24;80t", 80, 24, true},
		{"wide", "\x1b[8;50;200t", 200, 50, true},
		{"noise first", "boot...\x1b[8;30;120t", 120, 30, true},
		{"last wins", "\x1b[8;24;80t\x1b[8;40;160t", 160, 40, true},
		{"empty", "", 0, 0, false},
		{"truncated", "\x1b[8;24;", 0, 0, false},
		{"zero reply", "\x1b[8;0;0t", 0, 0, false},
		{"too small", "\x1b[8;5;10t", 0, 0, false},
		{"absurd", "\x1b[8;10;5000t", 0, 0, false},
		{"wrong final", "\x1b[8;24;80n", 0, 0, false},
	}
	for _, tc := range cases {
		cols, rows, ok := live.ParseCSITextAreaReply([]byte(tc.in))
		if ok != tc.ok || cols != tc.cols || rows != tc.rows {
			t.Errorf("%s: got (%d,%d,%v), want (%d,%d,%v)",
				tc.name, cols, rows, ok, tc.cols, tc.rows, tc.ok)
		}
	}
}

// TestGetWinsizeSane asserts the kernel query either reports a clamped
// usable size or reports unknown — never garbage a layout would consume.
func TestGetWinsizeSane(t *testing.T) {
	cols, rows, ok := live.GetWinsize()
	if !ok {
		return // no tty (pipes, CI): unknown is the correct answer
	}
	if cols < 20 || cols > 1000 || rows < 10 || rows > 500 {
		t.Fatalf("unclamped winsize (%d,%d)", cols, rows)
	}
}

// TestWinsizePollStops asserts the TUI size poll reschedules while the
// size is unsettled and stops after consecutive stable reads, so it can
// never churn repaints forever.
func TestWinsizePollStops(t *testing.T) {
	m := tui.New(config.Default(true), "/tmp/test-gentoo.toml")
	stopped := false
	for i := 0; i < 12; i++ {
		mm, cmd := m.Update(tui.WinsizeTickMsg{})
		m = mm.(*tui.Model)
		if cmd == nil {
			stopped = true
			break
		}
	}
	if !stopped {
		t.Fatal("winsize poll did not stop after stable reads")
	}
}
