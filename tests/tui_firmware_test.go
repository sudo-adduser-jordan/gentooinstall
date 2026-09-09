// Firmware pre-flight block: an EFI config on a BIOS-booted live system must
// be rejected before the install starts.
package tests

import (
	"strings"
	"testing"

	"gentooinstall/lib/config"
	"gentooinstall/lib/tui"
)

func TestFirmwareBlockError(testingT *testing.T) {
	if err := tui.FirmwareBlockError("efi", false); err == nil {
		testingT.Fatal("expected error for EFI without a UEFI host, got nil")
	} else {
		for _, want := range []string{"UEFI", "disk.boot_type", "bios"} {
			if !strings.Contains(err.Error(), want) {
				testingT.Fatalf("error should mention %q: %v", want, err)
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
			testingT.Fatalf("bootType=%q hasEFI=%v should pass, got %v",
				tc.bootType, tc.hasEFI, err)
		}
	}
}

// installTabModel builds a model on the Install tab with a known firmware
// probe result.
func installTabModel(testingT *testing.T, cfg *config.Config, hasEFI bool) *tui.Model {
	testingT.Helper()
	appModel := tui.New(cfg, "/tmp/test-gentoo.toml")
	appModel.SetHasEFI(hasEFI)
	appModel.SetInstallFunc(func() error { testingT.Fatal("install must not start"); return nil })
	mm, _ := appModel.Update(keyRunes('6')) // Install tab
	return mm.(*tui.Model)
}

func TestTuiFirmwareBlockRejectsEFIOnNoneFIHost(testingT *testing.T) {
	appModel := installTabModel(testingT, config.Default(true), false)

	view := appModel.View()
	for _, want := range []string{"Cannot install", "UEFI mode"} {
		if !strings.Contains(view, want) {
			testingT.Fatalf("install tab missing %q:\n%s", want, view)
		}
	}

	mm, _ := appModel.Update(keyRunes('i'))
	appModel = mm.(*tui.Model)
	view = appModel.View()
	if !strings.Contains(view, "Firmware mismatch") {
		testingT.Fatalf("i must explain the firmware mismatch, got:\n%s", view)
	}
	if strings.Contains(view, "Start installation") {
		testingT.Fatalf("i must not offer to start the install:\n%s", view)
	}
	if appModel.InstallActive() {
		testingT.Fatal("pressing i must not launch the install on a firmware mismatch")
	}

	// Dismissing closes the overlay and returns to the tab.
	mm, _ = appModel.Update(keyEnter())
	appModel = mm.(*tui.Model)
	if strings.Contains(appModel.View(), "Firmware mismatch") {
		testingT.Fatal("dismiss must close the mismatch dialog")
	}
}

func TestTuiFirmwareAllowsBIOSOnNoneFIHost(testingT *testing.T) {
	appModel := installTabModel(testingT, config.Default(false), false)

	mm, _ := appModel.Update(keyRunes('i'))
	appModel = mm.(*tui.Model)
	view := appModel.View()
	if strings.Contains(view, "Firmware mismatch") {
		testingT.Fatalf("BIOS config must not be blocked, got:\n%s", view)
	}
	for _, want := range []string{"DESTROY", "Start installation", "Cancel"} {
		if !strings.Contains(view, want) {
			testingT.Fatalf("confirm modal missing %q:\n%s", want, view)
		}
	}
	if !strings.Contains(view, "Effective boot mode: bios") {
		testingT.Fatalf("confirm modal should show effective boot mode:\n%s", view)
	}
	if appModel.InstallActive() {
		testingT.Fatal("i must not start before explicit confirmation")
	}
}

func TestTuiFirmwareAllowsEFIOnEFIHost(testingT *testing.T) {
	appModel := installTabModel(testingT, config.Default(true), true)

	mm, _ := appModel.Update(keyRunes('i'))
	appModel = mm.(*tui.Model)
	view := appModel.View()
	if strings.Contains(view, "Firmware mismatch") {
		testingT.Fatalf("EFI on a UEFI host must not be blocked, got:\n%s", view)
	}
	if !strings.Contains(view, "Start installation") {
		testingT.Fatalf("EFI on a UEFI host should open confirmation:\n%s", view)
	}
}
