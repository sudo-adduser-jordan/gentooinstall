package tests

import (
	"errors"
	"strings"
	"testing"

	"gentooinstall/internal/config"
	"gentooinstall/internal/tui"
)

func newInstallModel(t *testing.T) (*tui.Model, *[]tui.InstallDecision) {
	t.Helper()
	cfg := config.Default(true)
	m := tui.New(cfg, "/tmp/test-gentoo.toml")
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
	for _, want := range []string{"Retry", "Shell", "Editor", "Abort", "dev-vcs/git"} {
		if !strings.Contains(view, want) {
			t.Fatalf("failure panel missing %q", want)
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

	// e moves to editor mode without deciding.
	got = -1
	mm, _ = model.Update(tui.InstallFailedMsg{Cmdline: "x", Err: "y", Decide: decide})
	model = mm.(*tui.Model)
	mm, _ = model.Update(keyRunes('e'))
	model = mm.(*tui.Model)
	if model.InstallActive() {
		t.Fatal("editor choice must show tabs")
	}
	if got != tui.DecideEditor {
		t.Fatalf("decision = %v, want editor", got)
	}
	// i on the install tab returns to the waiting window.
	mm, _ = model.Update(keyRunes('6'))
	model = mm.(*tui.Model)
	mm, _ = model.Update(keyRunes('i'))
	model = mm.(*tui.Model)
	if !model.InstallActive() || model.InstallState() != "waiting" {
		t.Fatalf("return to install view failed: active=%v state=%s",
			model.InstallActive(), model.InstallState())
	}

	// a aborts; the completion message marks failure.
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

func TestTuiInstallPauseResetsToIdle(t *testing.T) {
	m, _ := newInstallModel(t)
	mm, _ := m.Update(tui.InstallStartMsg{})
	model := mm.(*tui.Model)

	// The install routine returns ErrEditAndReturn when the user chose to
	// return to the config tabs; the install view must reset to idle so a
	// fresh installation can be started again without exiting the program.
	mm, _ = model.Update(tui.InstallDoneMsg{Err: tui.ErrEditAndReturn})
	model = mm.(*tui.Model)
	if model.InstallActive() {
		t.Fatal("pausing must leave the install view")
	}
	if model.InstallState() != "idle" {
		t.Fatalf("state = %s, want idle", model.InstallState())
	}

	// i on the Install tab now opens the fresh-start confirmation instead of
	// re-showing the paused run.
	mm, _ = model.Update(keyRunes('6'))
	model = mm.(*tui.Model)
	mm, _ = model.Update(keyRunes('i'))
	model = mm.(*tui.Model)
	view := model.View()
	if !strings.Contains(view, "Start installation") {
		t.Fatalf("i should open a fresh installation confirmation, got:\n%s", view)
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
