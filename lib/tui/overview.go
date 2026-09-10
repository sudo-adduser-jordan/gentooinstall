package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"gentooinstall/lib/config"
	"gentooinstall/lib/disklayout"
)

func osStat(path string) (os.FileInfo, error) { return os.Stat(path) }

// kvList collects aligned label/value rows and renders them with a dynamic
// key column so nothing gets cut off.
type kvList struct {
	rows [][2]string
	kw   int
}

func (list *kvList) add(key, val string) {
	list.rows = append(list.rows, [2]string{key, val})
	if width := lipgloss.Width(key); width > list.kw {
		list.kw = width
	}
}

func (list *kvList) render(body *strings.Builder, indent string, width int) {
	trunc := lipgloss.NewStyle().MaxWidth(maxInt(20, width))
	for _, row := range list.rows {
		pad := strings.Repeat(" ", maxInt(0, list.kw-lipgloss.Width(row[0])))
		body.WriteString(trunc.Render(indent+row[0]+pad+"  "+row[1]) + "\n")
	}
}

// openConfigView opens the configuration summary in a scrollable modal;
// the content is rendered live so it always reflects the current state.
func (model *Model) openConfigView() {
	model.overlay = overlay{kind: ovConfig}
}

// openMakeConfView opens a scrollable viewer for the effective
// /etc/portage/make.conf content. Editing is added later.
func (model *Model) openMakeConfView() {
	model.overlay = overlay{kind: ovMakeConf}
}

// makeConfViewContent renders the effective make.conf content for the
// viewer: the built-in entries, the selected options, and the extra block.
func makeConfViewContent(cfg *config.Config, jobs int) string {
	var body strings.Builder
	body.WriteString("# /etc/portage/make.conf (effective)\n\n")
	if cfg.Packages.EnableBinpkg {
		body.WriteString("FEATURES=\"getbinpkg binpkg-request-signature\"\n")
	}
	arch := cfg.Gentoo.Arch
	for _, key := range cfg.MakeConf.Options {
		opt := config.LookupMakeConfOption(key)
		if opt == nil {
			continue
		}
		line := strings.ReplaceAll(opt.Line, "${JOBS}", fmt.Sprintf("%d", jobs))
		line = strings.ReplaceAll(line, "${ARCH}", arch)
		body.WriteString(line + "\n")
	}
	extra := strings.TrimSpace(cfg.MakeConf.Extra)
	if extra != "" {
		body.WriteString("\n" + extra + "\n")
	}
	return body.String()
}

// renderOverview collects the current configuration as a styled summary.
func renderOverview(model *Model) string {
	var body strings.Builder
	cfg := model.cfg
	width := model.bodyWidth()

	body.WriteString(sectionRule("📄 Configuration", width) + "\n")
	var kv kvList
	kv.add("Config file", valueStyle.Render(model.cfgPath))
	if model.hasEFI {
		kv.add("EFI support", okStyle.Render("✓ yes"))
	} else {
		kv.add("EFI support", errorStyle.Render("✗ no"))
	}
	kv.add("Boot type", badgeStyle.Render(cfg.Disk.BootType))
	if cfg.Disk.BootType == "efi" && !model.hasEFI {
		kv.add("", warnStyle.Render("⚠ your system does NOT support EFI — double-check!"))
	}
	kv.add("Init system", badgeStyle.Render(systemdName(cfg)))
	kv.add("Stage3", valueStyle.Render(cfg.Stage3BaseNameFinal()))
	kv.render(&body, " ", width)

	body.WriteString("\n" + sectionRule("Disk", width) + "\n")
	kv = kvList{}
	kv.add("Scheme", badgeStyle.Render(cfg.Disk.Scheme))
	switch cfg.Disk.Scheme {
	case config.SchemeClassic, config.SchemeExisting:
		kv.add("Device", valueStyle.Render(cfg.Disk.Device))
		if cfg.Disk.Scheme == config.SchemeExisting {
			kv.add("Boot device", valueStyle.Render(cfg.Disk.BootDevice))
		}
	case config.SchemeZFSCentric, config.SchemeBtrfs,
		config.SchemeRaid0Luks, config.SchemeRaid1Luks:
		kv.add("Devices", valueStyle.Render(strings.Join(cfg.Disk.Devices, " ")))
	}
	kv.render(&body, " ", width)

	body.WriteString("\n" + sectionRule("🧩 System", width) + "\n")
	kv = kvList{}
	orUnset := func(str string) string {
		if strings.TrimSpace(str) == "" {
			return unsetStyle.Render("unset (autodetect)")
		}
		return valueStyle.Render(str)
	}
	kv.add("Hostname", orUnset(cfg.System.Hostname))
	kv.add("Timezone", orUnset(cfg.System.Timezone))
	kv.add("Keymap", orUnset(cfg.System.Keymap))
	if count := len(cfg.System.Locales); count > 0 {
		kv.add("Locales", badgeStyle.Render(fmt.Sprintf("%d selected", count))+
			" "+unsetStyle.Render(strings.Join(cfg.System.Locales, ", ")))
	} else {
		kv.add("Locales", unsetStyle.Render("none"))
	}
	kv.add("Locale", orUnset(cfg.System.Locale))
	kv.render(&body, " ", width)
	return body.String()
}

// renderInstallTab shows the former overview's configuration summary
// followed by the installation status, checks and layout tree.
func renderInstallTab(model *Model) string {
	var body strings.Builder
	cfg := model.cfg
	width := model.bodyWidth()

	body.WriteString(sectionRule("Install", width) + "\n\n")

	var st kvList
	st.add("Total packages", badgeStyle.Render(fmt.Sprintf("~%d", cfg.EstimatePackageCount())))
	st.add("Install size", badgeStyle.Render(cfg.EstimateInstallSize()))
	st.add("Profile", badgeStyle.Render(profileSummary(cfg)))
	st.add("Kernel", badgeStyle.Render(kernelSummary(cfg)))
	st.add("Init", badgeStyle.Render(systemdName(cfg)))
	st.render(&body, " ", width)
	body.WriteString("\n")

	var problems []string
	for _, entry := range cfg.Validate() {
		problems = append(problems, entry.Error())
	}
	if err := FirmwareBlockError(cfg.Disk.BootType, model.hasEFI); err != nil {
		problems = append(problems, err.Error())
	}
	if len(problems) > 0 {
		body.WriteString(errorStyle.Render("⛔ Cannot install — fix these problems first:") + "\n")
		for _, entry := range problems {
			body.WriteString("  " + errorStyle.Render("✗") + " " + entry + "\n")
		}
		body.WriteString("\n")
	}

	if warns := cfg.Advisories(); len(warns) > 0 {
		body.WriteString(warnStyle.Render("⚠ Worth a look:") + "\n")
		for _, warn := range warns {
			body.WriteString("  " + warnStyle.Render(warn) + "\n")
		}
		body.WriteString("\n")
	}

	if prereq := model.renderPrereq(width); prereq != "" {
		body.WriteString(sectionRule("Host prerequisites", width) + "\n\n")
		body.WriteString(prereq)
		body.WriteString("\n")
	}

	layout, err := layoutForDisplay(cfg)
	if err != nil {
		body.WriteString(errorStyle.Render("Disk configuration error: "+err.Error()) + "\n")
	} else {
		body.WriteString(renderLayoutTree(layout, width))
	}

	body.WriteString("\n")
	switch model.instState {
	case instRunning, instWaiting:
		body.WriteString(warnStyle.Render(
			"An installation is in progress. Press i to view it.") + "\n")
	case instDone:
		body.WriteString(okStyle.Render(
			"The installation finished successfully.") + "\n")
	case instAborted:
		body.WriteString(errorStyle.Render(
			"The installation was aborted. Review the log with i; fix\n"+
				"your configuration and start again once the disks are settled.") + "\n")
	default:
		body.WriteString(helpStyle.Render(
			"Press i to start the installation (destructive), d for a simulated demo, or v to view the raw config."))
	}

	return body.String()
}

// renderLayoutTree renders the disk layout summary as a compact
// single-column tree with inline role badges.
func renderLayoutTree(layout *disklayout.Layout, width int) string {
	width = maxInt(50, width)
	var body strings.Builder

	for _, node := range layout.Summary() {
		name := node.Name
		if node.Hint != "" {
			name += " " + node.Hint
		}
		left := treeGlyphStyle.Render(node.Indent) + valueStyle.Render(name)

		rightParts := []string{}
		if node.Desc != "" {
			rightParts = append(rightParts, unsetStyle.Render(node.Desc))
		}
		switch node.Role {
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
			gap := width - leftW - rightW - 4
			if gap < 2 {
				gap = 2
			}
			body.WriteString(left + strings.Repeat(" ", gap) + right + "\n")
		} else {
			body.WriteString(left + "\n")
		}
	}
	return body.String()
}

func systemdName(cfg *config.Config) string {
	if cfg.UsesSystemd() {
		return "systemd"
	}
	name := "OpenRC"
	if cfg.UsesMusl() {
		name += " (musl)"
	}
	return name
}

// profileSummary renders the selected eselect profile as its friendly
// description, falling back to the raw id or "unset".
func profileSummary(cfg *config.Config) string {
	profile := cfg.Gentoo.Profile
	if profile == "" {
		return "unset"
	}
	if desc := config.ProfileDesc(profile); desc != "" {
		return desc
	}
	return profile
}

// kernelSummary renders the selected kernel package type.
func kernelSummary(cfg *config.Config) string {
	switch cfg.Packages.KernelType {
	case "source":
		return "source (gentoo-kernel)"
	case "bin":
		return "binary (gentoo-kernel-bin)"
	}
	return cfg.Packages.KernelType
}
