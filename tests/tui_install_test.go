// Headless TUI install flow: phases, decisions and progress view.
package tests

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"gentooinstall/lib/config"
	"gentooinstall/lib/tui"
)

func newInstallModel(t *testing.T) (*tui.Model, *[]tui.InstallDecision) {
	t.Helper()
	cfg := config.Default(true)
	m := tui.New(cfg, "/tmp/test-gentoo.toml")
	// Wide terminal: install-card tests assume the fixed 24-row card and
	// 12-slot checklist window (narrow serial terminals shrink both).
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = mm.(*tui.Model)
	decisions := &[]tui.InstallDecision{}
	m.SetInstallFunc(func() error {
		if len(*decisions) > 0 && (*decisions)[len(*decisions)-1] == tui.DecideAbort {
			return errors.New("aborted")
		}
		return nil
	})
	return m, decisions
}

func TestTuiInstallStreamsAndFinishes(t *testing.T) {
	m, _ := newInstallModel(t)

	if m.InstallActive() {
		t.Fatal("install view must start inactive")
	}
	mm, _ := m.Update(tui.InstallStartMsg{})
	model := mm.(*tui.Model)
	if !model.InstallActive() || model.InstallState() != "running" {
		t.Fatalf("state = %s, want running", model.InstallState())
	}

	mm, _ = model.Update(tui.InstallLineMsg{Line: "\x1b[1;32m$ emerge --verbose sys-kernel/gentoo-kernel\x1b[m"})
	model = mm.(*tui.Model)
	lines := model.InstallLines()
	if len(lines) == 0 || !strings.HasSuffix(lines[len(lines)-1], "$ emerge --verbose sys-kernel/gentoo-kernel") {
		t.Fatalf("ansi not stripped: %q", lines)
	}

	view := model.View()
	if !strings.Contains(view, "emerge --verbose sys-kernel/gentoo-kernel") {
		t.Fatal("log line missing from install view")
	}
	if !strings.Contains(view, "running") {
		t.Fatal("status missing from install view")
	}

	mm, _ = model.Update(tui.InstallDoneMsg{Err: nil})
	model = mm.(*tui.Model)
	if model.InstallState() != "done" {
		t.Fatalf("state = %s, want done", model.InstallState())
	}
	// e returns to the tabs and resets so a fresh install can begin again.
	mm, _ = model.Update(keyRunes('e'))
	model = mm.(*tui.Model)
	if model.InstallActive() {
		t.Fatal("e must leave the install view")
	}
	if model.InstallState() != "idle" {
		t.Fatalf("state = %s, want idle after leaving a finished install", model.InstallState())
	}
}

func TestTuiInstallFailureDecisions(t *testing.T) {
	m, _ := newInstallModel(t)
	mm, _ := m.Update(tui.InstallStartMsg{})
	model := mm.(*tui.Model)

	var got tui.InstallDecision = -1
	decide := func(d tui.InstallDecision) { got = d }
	mm, _ = model.Update(tui.InstallFailedMsg{
		Cmdline: "emerge -v dev-vcs/git",
		Err:     "exit status 1",
		Decide:  decide,
	})
	model = mm.(*tui.Model)
	if model.InstallState() != "waiting" {
		t.Fatalf("state = %s, want waiting", model.InstallState())
	}
	view := model.View()
	for _, want := range []string{"Retry", "Abort", "dev-vcs/git"} {
		if !strings.Contains(view, want) {
			t.Fatalf("failure panel missing %q", want)
		}
	}
	for _, gone := range []string{"Shell", "Editor"} {
		if strings.Contains(view, gone) {
			t.Fatalf("failure panel must not contain %q", gone)
		}
	}

	// r retries.
	mm, _ = model.Update(keyRunes('r'))
	model = mm.(*tui.Model)
	if got != tui.DecideRetry {
		t.Fatalf("decision = %v, want retry", got)
	}
	if model.InstallState() != "running" {
		t.Fatalf("state = %s, want running after retry", model.InstallState())
	}

	// a aborts; the completion message marks failure.
	got = -1
	mm, _ = model.Update(tui.InstallFailedMsg{Cmdline: "x", Err: "y", Decide: decide})
	model = mm.(*tui.Model)
	mm, _ = model.Update(keyRunes('a'))
	model = mm.(*tui.Model)
	if got != tui.DecideAbort {
		t.Fatalf("decision = %v, want abort", got)
	}
	mm, _ = model.Update(tui.InstallDoneMsg{Err: errors.New("command failed")})
	model = mm.(*tui.Model)
	if model.InstallState() != "aborted" {
		t.Fatalf("state = %s, want aborted", model.InstallState())
	}
}

func TestTuiInstallConfirmation(t *testing.T) {
	cfg := config.Default(true)
	cfg.Disk.UseLuks = false // avoid the passphrase prompt during the test
	m := tui.New(cfg, "/tmp/test-gentoo.toml")
	m.SetInstallFunc(func() error { return nil })

	mm, _ := m.Update(keyRunes('6')) // Install tab
	model := mm.(*tui.Model)

	// i in the idle state opens the confirmation modal instead of installing.
	mm, _ = model.Update(keyRunes('i'))
	model = mm.(*tui.Model)
	view := model.View()
	for _, want := range []string{"DESTROY", "Start installation", "Cancel"} {
		if !strings.Contains(view, want) {
			t.Fatalf("confirmation modal missing %q, got:\n%s", want, view)
		}
	}
	if model.InstallActive() {
		t.Fatal("i must not start the install before confirmation")
	}

	// Enter picks the pre-selected Cancel (no), so nothing starts.
	mm, _ = model.Update(keyEnter())
	model = mm.(*tui.Model)
	if model.InstallActive() || model.InstallState() != "idle" {
		t.Fatalf("Cancel must not start the install: active=%v state=%s",
			model.InstallActive(), model.InstallState())
	}

	// Moving left to Start installation and confirming begins the real run.
	mm, _ = model.Update(keyRunes('i'))
	model = mm.(*tui.Model)
	mm, _ = model.Update(keyLeft())
	model = mm.(*tui.Model)
	mm, _ = model.Update(keyEnter())
	model = mm.(*tui.Model)
	if !model.InstallActive() || model.InstallState() != "running" {
		t.Fatalf("confirming Start must launch the install: active=%v state=%s",
			model.InstallActive(), model.InstallState())
	}
}

func TestTuiInstallAbortedResetsToIdle(t *testing.T) {
	m, _ := newInstallModel(t)
	mm, _ := m.Update(tui.InstallStartMsg{})
	model := mm.(*tui.Model)

	// A failed installation marks the view aborted; leaving it with e
	// resets to idle so a fresh installation can start from the Install tab.
	mm, _ = model.Update(tui.InstallDoneMsg{Err: errors.New("command failed")})
	model = mm.(*tui.Model)
	if model.InstallState() != "aborted" {
		t.Fatalf("state = %s, want aborted", model.InstallState())
	}
	mm, _ = model.Update(keyRunes('e'))
	model = mm.(*tui.Model)
	if model.InstallActive() {
		t.Fatal("e must leave the install view")
	}
	if model.InstallState() != "idle" {
		t.Fatalf("state = %s, want idle", model.InstallState())
	}

	// i on the Install tab opens the fresh-start confirmation.
	mm, _ = model.Update(keyRunes('6'))
	model = mm.(*tui.Model)
	mm, _ = model.Update(keyRunes('i'))
	model = mm.(*tui.Model)
	view := model.View()
	if !strings.Contains(view, "Start installation") {
		t.Fatalf("i should open a fresh installation confirmation, got:\n%s", view)
	}
}

func TestTuiInstallStepsScrollUnderBar(t *testing.T) {
	m, _ := newInstallModel(t)
	mm, _ := m.Update(tui.InstallStartMsg{})
	model := mm.(*tui.Model)

	// Enough steps to overflow the fixed checklist window: the oldest ones
	// scroll up and off under the loading bar, latest stay visible.
	for i := 0; i < 16; i++ {
		mm, _ = model.Update(tui.InstallLineMsg{Line: fmt.Sprintf("[+] Step %d", i+1)})
		model = mm.(*tui.Model)
	}
	view := model.View()
	if !strings.Contains(view, "Step 16") {
		t.Fatal("latest step missing from install view")
	}
	if !strings.Contains(view, "Step 5") {
		t.Fatal("first visible step missing from the scrolling window")
	}
	if strings.Contains(view, "Step 4") {
		t.Fatal("step scrolled off under the loading bar")
	}
	if strings.Contains(view, "⋮") {
		t.Fatal("overflow marker should not be rendered in the step window")
	}
}

func TestTuiInstallCardStaticSize(t *testing.T) {
	m, _ := newInstallModel(t)
	mm, _ := m.Update(tui.InstallStartMsg{})
	model := mm.(*tui.Model)

	span := func() int {
		lines := strings.Split(model.View(), "\n")
		top, bottom := -1, -1
		for i, l := range lines {
			if strings.Contains(l, "╭") && top < 0 {
				top = i
			}
			if top >= 0 && strings.Contains(l, "╰") {
				bottom = i
				break
			}
		}
		return bottom - top + 1
	}

	for i := 0; i < 3; i++ {
		mm, _ = model.Update(tui.InstallLineMsg{Line: fmt.Sprintf("early%d", i)})
		model = mm.(*tui.Model)
	}
	small := span()
	for i := 0; i < 30; i++ {
		mm, _ = model.Update(tui.InstallLineMsg{Line: fmt.Sprintf("more%d", i)})
		model = mm.(*tui.Model)
	}
	large := span()
	if small != large {
		t.Fatalf("install card height changed with log size: %d vs %d", small, large)
	}
	if small != 24 {
		t.Fatalf("install card height = %d, want fixed 24 rows", small)
	}
}

func TestTuiInstallQuitGuard(t *testing.T) {
	m, _ := newInstallModel(t)
	mm, _ := m.Update(tui.InstallStartMsg{})
	model := mm.(*tui.Model)

	mm, _ = model.Update(keyRunes('q'))
	model = mm.(*tui.Model)
	view := model.View()
	if !strings.Contains(view, "Installation in progress") {
		t.Fatal("quit guard overlay missing")
	}

	// Enter picks "Stay" and closes the overlay.
	mm, _ = model.Update(keyEnter())
	model = mm.(*tui.Model)
	if model.View() == "" && model.Dirty() {
		t.Fatal("unexpected model state")
	}
}
