package tui

import (
	"errors"

	tea "github.com/charmbracelet/bubbletea"

	"gentooinstall/internal/config"
	"gentooinstall/internal/disklayout"
	"gentooinstall/internal/sysinfo"
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
func layoutForDisplay(c *config.Config) (*disklayout.Layout, error) {
	return disklayout.BuildFromConfig(c, "")
}

// ActiveTab exposes the current tab index (used by tests).
func (m *Model) ActiveTab() int { return m.active }

// Dirty reports whether unsaved changes exist (used by tests).
func (m *Model) Dirty() bool { return m.dirty }

// Config exposes the edited configuration (used by tests).
func (m *Model) Config() *config.Config { return m.cfg }

func (m *Model) confirmInstall() (tea.Model, tea.Cmd) {
	if err := FirmwareBlockError(m.cfg.Disk.BootType, m.hasEFI); err != nil {
		m.overlay = overlay{
			kind:    ovButtons,
			title:   eWarn + " Firmware mismatch",
			body:    err.Error(),
			buttons: []string{"Dismiss"},
			btnCur:  0,
			onBtn:   func(mm *Model, i int) { mm.overlay.kind = ovNone },
		}
		return m, nil
	}
	l, err := layoutForDisplay(m.cfg)
	if err != nil {
		m.setStatusErr("disk configuration error: " + err.Error())
		return m, nil
	}
	if errs := m.cfg.Validate(); len(errs) > 0 {
		m.setStatusErr("configuration is invalid")
		return m, nil
	}
	if err := disklayout.CheckBootTypeConsistency(m.cfg, l); err != nil {
		m.overlay = overlay{
			kind:    ovButtons,
			title:   eWarn + " Configuration mismatch",
			body:    err.Error(),
			buttons: []string{"Dismiss"},
			btnCur:  0,
			onBtn:   func(mm *Model, i int) { mm.overlay.kind = ovNone },
		}
		return m, nil
	}
	if !sysinfo.SupportsFilesystem("vfat") {
		mountpoint := "/boot/bios"
		if m.cfg.Disk.BootType == "efi" {
			mountpoint = "/boot/efi"
		}
		m.overlay = overlay{
			kind:  ovButtons,
			title: eWarn + " Missing vfat support",
			body: "The live kernel has no vfat support so " + mountpoint +
				" cannot be mounted (the boot partition is FAT32). Rebuild the live ISO " +
				"on a host whose kernel provides vfat, then start a fresh install — " +
				"retrying cannot help.",
			buttons: []string{"Dismiss"},
			btnCur:  0,
			onBtn:   func(mm *Model, i int) { mm.overlay.kind = ovNone },
		}
		return m, nil
	}

	var targets []string
	if l.Flags.NoPartitioningOrFormatting {
		targets = append(targets, "(existing partitions will be reused)")
	} else {
		targets = append(targets, m.cfg.Disk.Device)
		targets = append(targets, m.cfg.Disk.Devices...)
	}
	body := "This will DESTROY all data on:\n  " + joinNonEmpty(targets, "\n  ") +
		"\n\nEffective boot mode: " + m.cfg.Disk.BootType +
		" (EFIID=" + l.EFIID + " BIOSID=" + l.BIOSID + ")" +
		"\nThe partitioning step cannot be undone. Continue?"

	m.overlay = overlay{
		kind:    ovButtons,
		title:   "Apply this disk configuration?",
		body:    body,
		buttons: []string{"Start installation", "Cancel"},
		btnCur:  1,
		onBtn: func(mm *Model, i int) {
			if i == 0 {
				mm.requestStartInstall()
			}
		},
	}
	m.usedEnc = l.Flags.UsedEncryption
	return m, nil
}

func joinNonEmpty(xs []string, sep string) string {
	out := ""
	for _, x := range xs {
		if x == "" {
			continue
		}
		if out != "" {
			out += sep
		}
		out += x
	}
	return out
}
