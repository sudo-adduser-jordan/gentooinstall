package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"gentooinstall/internal/config"
	"gentooinstall/internal/disklayout"
)

func osStat(p string) (os.FileInfo, error) { return os.Stat(p) }

// kvList collects aligned label/value rows and renders them with a dynamic
// key column so nothing gets cut off.
type kvList struct {
	rows [][2]string
	kw   int
}

func (k *kvList) add(key, val string) {
	k.rows = append(k.rows, [2]string{key, val})
	if w := lipgloss.Width(key); w > k.kw {
		k.kw = w
	}
}

func (k *kvList) render(b *strings.Builder, indent string, w int) {
	trunc := lipgloss.NewStyle().MaxWidth(maxInt(20, w))
	for _, r := range k.rows {
		pad := strings.Repeat(" ", maxInt(0, k.kw-lipgloss.Width(r[0])))
		b.WriteString(trunc.Render(indent+r[0]+pad+"  "+r[1]) + "\n")
	}
}

// openConfigView opens the configuration summary in a scrollable modal;
// the content is rendered live so it always reflects the current state.
func (m *Model) openConfigView() {
	m.overlay = overlay{kind: ovConfig}
}

// openMakeConfView opens a scrollable viewer for the effective
// /etc/portage/make.conf content. Editing is added later.
func (m *Model) openMakeConfView() {
	m.overlay = overlay{kind: ovMakeConf}
}

// makeConfViewContent renders the effective make.conf content for the
// viewer: the built-in entries, the selected options, and the extra block.
func makeConfViewContent(c *config.Config, jobs int) string {
	var b strings.Builder
	b.WriteString("# /etc/portage/make.conf (effective)\n\n")
	if c.Packages.EnableBinpkg {
		b.WriteString("FEATURES=\"getbinpkg binpkg-request-signature\"\n")
	}
	arch := c.Gentoo.Arch
	for _, key := range c.MakeConf.Options {
		o := config.LookupMakeConfOption(key)
		if o == nil {
			continue
		}
		line := strings.ReplaceAll(o.Line, "${JOBS}", fmt.Sprintf("%d", jobs))
		line = strings.ReplaceAll(line, "${ARCH}", arch)
		b.WriteString(line + "\n")
	}
	extra := strings.TrimSpace(c.MakeConf.Extra)
	if extra != "" {
		b.WriteString("\n" + extra + "\n")
	}
	return b.String()
}

// renderOverview collects the current configuration as a styled summary.
func renderOverview(m *Model) string {
	var b strings.Builder
	c := m.cfg
	w := m.bodyWidth()

	b.WriteString(sectionRule("📄 Configuration", w) + "\n")
	var kv kvList
	kv.add("Config file", valueStyle.Render(m.cfgPath))
	if m.hasEFI {
		kv.add("EFI support", okStyle.Render("✓ yes"))
	} else {
		kv.add("EFI support", errorStyle.Render("✗ no"))
	}
	kv.add("Boot type", badgeStyle.Render(c.Disk.BootType))
	if c.Disk.BootType == "efi" && !m.hasEFI {
		kv.add("", warnStyle.Render("⚠ your system does NOT support EFI — double-check!"))
	}
	kv.add("Init system", badgeStyle.Render(systemdName(c)))
	kv.add("Stage3", valueStyle.Render(c.Stage3BaseNameFinal()))
	kv.render(&b, " ", w)

	b.WriteString("\n" + sectionRule("Disk", w) + "\n")
	kv = kvList{}
	kv.add("Scheme", badgeStyle.Render(c.Disk.Scheme))
	switch c.Disk.Scheme {
	case config.SchemeClassic, config.SchemeExisting:
		kv.add("Device", valueStyle.Render(c.Disk.Device))
		if c.Disk.Scheme == config.SchemeExisting {
			kv.add("Boot device", valueStyle.Render(c.Disk.BootDevice))
		}
	case config.SchemeZFSCentric, config.SchemeBtrfs,
		config.SchemeRaid0Luks, config.SchemeRaid1Luks:
		kv.add("Devices", valueStyle.Render(strings.Join(c.Disk.Devices, " ")))
	}
	kv.render(&b, " ", w)

	b.WriteString("\n" + sectionRule("🧩 System", w) + "\n")
	kv = kvList{}
	orUnset := func(s string) string {
		if strings.TrimSpace(s) == "" {
			return unsetStyle.Render("unset (autodetect)")
		}
		return valueStyle.Render(s)
	}
	kv.add("Hostname", orUnset(c.System.Hostname))
	kv.add("Timezone", orUnset(c.System.Timezone))
	kv.add("Keymap", orUnset(c.System.Keymap))
	if n := len(c.System.Locales); n > 0 {
		kv.add("Locales", badgeStyle.Render(fmt.Sprintf("%d selected", n))+
			" "+unsetStyle.Render(strings.Join(c.System.Locales, ", ")))
	} else {
		kv.add("Locales", unsetStyle.Render("none"))
	}
	kv.add("Locale", orUnset(c.System.Locale))
	kv.render(&b, " ", w)
	return b.String()
}

// renderInstallTab shows the former overview's configuration summary
// followed by the installation status, checks and layout tree.
func renderInstallTab(m *Model) string {
	var b strings.Builder
	c := m.cfg
	w := m.bodyWidth()

	b.WriteString(sectionRule("Install", w) + "\n\n")

	var st kvList
	st.add("Total packages", badgeStyle.Render(fmt.Sprintf("~%d", c.EstimatePackageCount())))
	st.add("Install size", badgeStyle.Render(c.EstimateInstallSize()))
	st.add("Profile", badgeStyle.Render(profileSummary(c)))
	st.add("Kernel", badgeStyle.Render(kernelSummary(c)))
	st.add("Init", badgeStyle.Render(systemdName(c)))
	st.render(&b, " ", w)
	b.WriteString("\n")

	if errs := c.Validate(); len(errs) > 0 {
		b.WriteString(errorStyle.Render("⛔ Cannot install — fix these problems first:") + "\n")
		for _, e := range errs {
			b.WriteString("  " + errorStyle.Render("✗") + " " + e.Error() + "\n")
		}
		b.WriteString("\n")
	}

	if warns := c.Advisories(); len(warns) > 0 {
		b.WriteString(warnStyle.Render("⚠ Worth a look:") + "\n")
		for _, w := range warns {
			b.WriteString("  " + warnStyle.Render(w) + "\n")
		}
		b.WriteString("\n")
	}

	l, err := layoutForDisplay(c)
	if err != nil {
		b.WriteString(errorStyle.Render("Disk configuration error: "+err.Error()) + "\n")
	} else {
		b.WriteString(renderLayoutTree(l, w))
	}

	b.WriteString("\n")
	switch m.instState {
	case instRunning, instWaiting:
		b.WriteString(warnStyle.Render(
			"An installation is in progress. Press i to view it.") + "\n")
	case instDone:
		b.WriteString(okStyle.Render(
			"The installation finished successfully.") + "\n")
	case instAborted:
		b.WriteString(errorStyle.Render(
			"The installation was aborted. Review the log with i; fix\n"+
				"your configuration and start again once the disks are settled.") + "\n")
	default:
		b.WriteString(helpStyle.Render(
			"Press i to start the installation (destructive), d for a simulated demo, or v to view the raw config."))
	}

	return b.String()
}

// renderLayoutTree renders the disk layout summary as a compact
// single-column tree with inline role badges.
func renderLayoutTree(l *disklayout.Layout, w int) string {
	w = maxInt(50, w)
	var b strings.Builder

	for _, n := range l.Summary() {
		name := n.Name
		if n.Hint != "" {
			name += " " + n.Hint
		}
		left := treeGlyphStyle.Render(n.Indent) + valueStyle.Render(name)

		rightParts := []string{}
		if n.Desc != "" {
			rightParts = append(rightParts, unsetStyle.Render(n.Desc))
		}
		switch n.Role {
		case "bios":
			rightParts = append(rightParts, treeRoleStyle.Render("[bios]"))
		case "efi":
			rightParts = append(rightParts, treeRoleStyle.Render("[boot]"))
		case "swap":
			rightParts = append(rightParts, treeRoleStyle.Render("[swap]"))
		case "root":
			rightParts = append(rightParts, treeRoleStyle.Render("[root]"))
		}

		leftW := lipgloss.Width(left)
		if len(rightParts) > 0 {
			right := strings.Join(rightParts, "  ")
			rightW := lipgloss.Width(right)
			gap := w - leftW - rightW - 4
			if gap < 2 {
				gap = 2
			}
			b.WriteString(left + strings.Repeat(" ", gap) + right + "\n")
		} else {
			b.WriteString(left + "\n")
		}
	}
	return b.String()
}

func systemdName(c *config.Config) string {
	if c.UsesSystemd() {
		return "systemd"
	}
	s := "OpenRC"
	if c.UsesMusl() {
		s += " (musl)"
	}
	return s
}

// profileSummary renders the selected eselect profile as its friendly
// description, falling back to the raw id or "unset".
func profileSummary(c *config.Config) string {
	p := c.Gentoo.Profile
	if p == "" {
		return "unset"
	}
	if d := config.ProfileDesc(p); d != "" {
		return d
	}
	return p
}

// kernelSummary renders the selected kernel package type.
func kernelSummary(c *config.Config) string {
	switch c.Packages.KernelType {
	case "source":
		return "source (gentoo-kernel)"
	case "bin":
		return "binary (gentoo-kernel-bin)"
	}
	return c.Packages.KernelType
}
