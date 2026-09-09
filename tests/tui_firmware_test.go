// Firmware pre-flight block: an EFI config on a BIOS-booted live system must
// be rejected before the install starts.
package tests

import (
	"strings"
	"testing"

	"gentooinstall/lib/config"
	"gentooinstall/lib/tui"
)

func TestFirmwareBlockError(t *testing.T) {
	if err := tui.FirmwareBlockError("efi", false); err == nil {
		t.Fatal("expected error for EFI without a UEFI host, got nil")
	} else {
		for _, want := range []string{"UEFI", "disk.boot_type", "bios"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error should mention %q: %v", want, err)
			}
		}
	}
	for _, tc := range []struct {
		bootType string
		hasEFI   bool
	}{
		{"efi", true},
		{"bios", true},
		{"bios", false},
	} {
		if err := tui.FirmwareBlockError(tc.bootType, tc.hasEFI); err != nil {
			t.Fatalf("bootType=%q hasEFI=%v should pass, got %v",
				tc.bootType, tc.hasEFI, err)
		}
	}
}

// installTabModel builds a model on the Install tab with a known firmware
// probe result.
func installTabModel(t *testing.T, cfg *config.Config, hasEFI bool) *tui.Model {
	t.Helper()
	m := tui.New(cfg, "/tmp/test-gentoo.toml")
	m.SetHasEFI(hasEFI)
	m.SetInstallFunc(func() error { t.Fatal("install must not start"); return nil })
	mm, _ := m.Update(keyRunes('6')) // Install tab
	return mm.(*tui.Model)
}

func TestTuiFirmwareBlockRejectsEFIOnNoneFIHost(t *testing.T) {
	m := installTabModel(t, config.Default(true), false)

	view := m.View()
	for _, want := range []string{"Cannot install", "UEFI mode"} {
		if !strings.Contains(view, want) {
			t.Fatalf("install tab missing %q:\n%s", want, view)
		}
	}

	mm, _ := m.Update(keyRunes('i'))
	m = mm.(*tui.Model)
	view = m.View()
	if !strings.Contains(view, "Firmware mismatch") {
		t.Fatalf("i must explain the firmware mismatch, got:\n%s", view)
	}
	if strings.Contains(view, "Start installation") {
		t.Fatalf("i must not offer to start the install:\n%s", view)
	}
	if m.InstallActive() {
		t.Fatal("pressing i must not launch the install on a firmware mismatch")
	}

	// Dismissing closes the overlay and returns to the tab.
	mm, _ = m.Update(keyEnter())
	m = mm.(*tui.Model)
	if strings.Contains(m.View(), "Firmware mismatch") {
		t.Fatal("dismiss must close the mismatch dialog")
	}
}

func TestTuiFirmwareAllowsBIOSOnNoneFIHost(t *testing.T) {
	m := installTabModel(t, config.Default(false), false)

	mm, _ := m.Update(keyRunes('i'))
	m = mm.(*tui.Model)
	view := m.View()
	if strings.Contains(view, "Firmware mismatch") {
		t.Fatalf("BIOS config must not be blocked, got:\n%s", view)
	}
	for _, want := range []string{"DESTROY", "Start installation", "Cancel"} {
		if !strings.Contains(view, want) {
			t.Fatalf("confirm modal missing %q:\n%s", want, view)
		}
	}
	if !strings.Contains(view, "Effective boot mode: bios") {
		t.Fatalf("confirm modal should show effective boot mode:\n%s", view)
	}
	if m.InstallActive() {
		t.Fatal("i must not start before explicit confirmation")
	}
}

func TestTuiFirmwareAllowsEFIOnEFIHost(t *testing.T) {
	m := installTabModel(t, config.Default(true), true)

	mm, _ := m.Update(keyRunes('i'))
	m = mm.(*tui.Model)
	view := m.View()
	if strings.Contains(view, "Firmware mismatch") {
		t.Fatalf("EFI on a UEFI host must not be blocked, got:\n%s", view)
	}
	if !strings.Contains(view, "Start installation") {
		t.Fatalf("EFI on a UEFI host should open confirmation:\n%s", view)
	}
}
