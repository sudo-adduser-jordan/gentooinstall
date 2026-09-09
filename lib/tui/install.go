package tui

import (
	"errors"

	tea "github.com/charmbracelet/bubbletea"

	"gentooinstall/lib/config"
	"gentooinstall/lib/disklayout"
	"gentooinstall/lib/sysinfo"
)

// FirmwareBlockError reports whether the configured boot type cannot run on
// the live firmware. An EFI install requires a UEFI boot: without the
// kernel-exposed /sys/firmware/efi there is no efivarfs to mount and no
// efibootmgr boot entry can be registered, so proceeding is guaranteed to
// fail mid-install. BIOS installs always pass.
func FirmwareBlockError(bootType string, hasEFI bool) error {
	if bootType == "efi" && !hasEFI {
		return errors.New("the live system was not booted in UEFI mode " +
			"(/sys/firmware/efi is missing) but the configuration requests an EFI " +
			`install; reboot the live ISO under UEFI (bare metal: enable UEFI boot; ` +
			`QEMU: add OVMF firmware) or set disk.boot_type = "bios" ` +
			"(e.g. builds/bios.toml)")
	}
	return nil
}

// layoutForDisplay builds the disk layout for preview purposes.
func layoutForDisplay(cfg *config.Config) (*disklayout.Layout, error) {
	return disklayout.BuildFromConfig(cfg, "")
}

// ActiveTab exposes the current tab index (used by tests).
func (model *Model) ActiveTab() int { return model.active }

// Dirty reports whether unsaved changes exist (used by tests).
func (model *Model) Dirty() bool { return model.dirty }

// Config exposes the edited configuration (used by tests).
func (model *Model) Config() *config.Config { return model.cfg }

func (model *Model) confirmInstall() (tea.Model, tea.Cmd) {
	if err := FirmwareBlockError(model.cfg.Disk.BootType, model.hasEFI); err != nil {
		model.overlay = overlay{
			kind:    ovButtons,
			title:   eWarn + " Firmware mismatch",
			body:    err.Error(),
			buttons: []string{"Dismiss"},
			btnCur:  0,
			onBtn:   func(mm *Model, index int) { mm.overlay.kind = ovNone },
		}
		return model, nil
	}
	layout, err := layoutForDisplay(model.cfg)
	if err != nil {
		model.setStatusErr("disk configuration error: " + err.Error())
		return model, nil
	}
	if errs := model.cfg.Validate(); len(errs) > 0 {
		model.setStatusErr("configuration is invalid")
		return model, nil
	}
	if err := disklayout.CheckBootTypeConsistency(model.cfg, layout); err != nil {
		model.overlay = overlay{
			kind:    ovButtons,
			title:   eWarn + " Configuration mismatch",
			body:    err.Error(),
			buttons: []string{"Dismiss"},
			btnCur:  0,
			onBtn:   func(mm *Model, index int) { mm.overlay.kind = ovNone },
		}
		return model, nil
	}
	if !sysinfo.SupportsFilesystem("vfat") {
		mountpoint := "/boot/bios"
		if model.cfg.Disk.BootType == "efi" {
			mountpoint = "/boot/efi"
		}
		model.overlay = overlay{
			kind:  ovButtons,
			title: eWarn + " Missing vfat support",
			body: "The live kernel has no vfat support so " + mountpoint +
				" cannot be mounted (the boot partition is FAT32). Rebuild the live ISO " +
				"on a host whose kernel provides vfat, then start a fresh install — " +
				"retrying cannot help.",
			buttons: []string{"Dismiss"},
			btnCur:  0,
			onBtn:   func(mm *Model, index int) { mm.overlay.kind = ovNone },
		}
		return model, nil
	}

	var targets []string
	if layout.Flags.NoPartitioningOrFormatting {
		targets = append(targets, "(existing partitions will be reused)")
	} else {
		targets = append(targets, model.cfg.Disk.Device)
		targets = append(targets, model.cfg.Disk.Devices...)
	}
	body := "This will DESTROY all data on:\n  " + joinNonEmpty(targets, "\n  ") +
		"\n\nEffective boot mode: " + model.cfg.Disk.BootType +
		" (EFIID=" + layout.EFIID + " BIOSID=" + layout.BIOSID + ")" +
		"\nThe partitioning step cannot be undone. Continue?"

	model.overlay = overlay{
		kind:    ovButtons,
		title:   "Apply this disk configuration?",
		body:    body,
		buttons: []string{"Start installation", "Cancel"},
		btnCur:  1,
		onBtn: func(mm *Model, index int) {
			if index == 0 {
				mm.requestStartInstall()
			}
		},
	}
	model.usedEnc = layout.Flags.UsedEncryption
	return model, nil
}

func joinNonEmpty(xs []string, sep string) string {
	out := ""
	for _, item := range xs {
		if item == "" {
			continue
		}
		if out != "" {
			out += sep
		}
		out += item
	}
	return out
}
