package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"gentooinstall/internal/config"
	"gentooinstall/internal/disklayout"
)

// layoutForDisplay builds the disk layout for preview purposes.
func layoutForDisplay(c *config.Config) (*disklayout.Layout, error) {
	return disklayout.BuildFromConfig(c, "")
}

// ShouldRunInstall reports whether the user confirmed an installation.
func (m *Model) ShouldRunInstall() bool { return m.runInstall }

// ActiveTab exposes the current tab index (used by tests).
func (m *Model) ActiveTab() int { return m.active }

// Dirty reports whether unsaved changes exist (used by tests).
func (m *Model) Dirty() bool { return m.dirty }

// Config exposes the edited configuration (used by tests).
func (m *Model) Config() *config.Config { return m.cfg }

func (m *Model) confirmInstall() (tea.Model, tea.Cmd) {
	l, err := layoutForDisplay(m.cfg)
	if err != nil {
		m.setStatusErr("disk configuration error: " + err.Error())
		return m, nil
	}
	if errs := m.cfg.Validate(); len(errs) > 0 {
		m.setStatusErr("configuration is invalid")
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
		"\n\nThe partitioning step cannot be undone. Continue?"

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
