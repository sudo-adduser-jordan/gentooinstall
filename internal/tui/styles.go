// Package tui implements the gentooinstall configurator: a Bubble Tea app with
// numbered tabs, inspired by pVPN's terminal UI.
package tui

import "github.com/charmbracelet/lipgloss"

// Emoji used across the UI.
const (
	eGear    = "🔧"
	eGlobe   = "🌐"
	eCrane   = "🔧"
	ePackage = "📦"
	eWarn    = "⚠️"
	eParty   = "🎉"
	eScroll  = "📜"
	eFlask   = "🧪"
	eDoor    = "🚪"
	eSave    = "💾"
	ePencil  = "✏️"
	eInfo    = "💡"
	eRocket  = "🚀"
	eDisk    = "💾"
)

// tabEmoji maps a tab name to its icon.
func tabEmoji(name string) string {
	switch name {
	case "Disk":
		return eDisk
	case "System":
		return eGear
	case "Network":
		return eGlobe
	case "Gentoo":
		return eCrane
	case "Packages":
		return ePackage
	case "Install":
		return eRocket
	}
	return ""
}

var (
	accent       = lipgloss.AdaptiveColor{Light: "#7C3AED", Dark: "#A78BFA"}
	gentooPurple = lipgloss.Color("#5B308A")
	warn         = lipgloss.Color("#F59E0B")
	bad          = lipgloss.Color("#EF4444")
	good         = lipgloss.Color("#10B981")
	white        = lipgloss.Color("#FFFFFF")
	dim          = lipgloss.AdaptiveColor{Light: "#4B5563", Dark: "#9CA3AF"}

	// teal is used for read-only, profile-derived packages so they are
	// visually distinct from manually entered additional packages.
	profileTeal = lipgloss.AdaptiveColor{Light: "#0F766E", Dark: "#5EEAD4"}

	// fg is the regular value color; faint is for placeholders and
	// pending rows; selBg is the soft highlight behind the selected row.
	fg    = lipgloss.AdaptiveColor{Light: "#1F2937", Dark: "#E5E7EB"}
	faint = lipgloss.AdaptiveColor{Light: "#B0B5BD", Dark: "#4B5563"}
	selBg = lipgloss.AdaptiveColor{Light: "#EDE9FE", Dark: "#312A45"}
)

var (
	// Tab bar boxes.
	tabActiveBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(accent).
				Foreground(white).
				Bold(true).
				Padding(0, 1)

	tabInactiveBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(dim).
				Foreground(fg).
				Padding(0, 1)

	// Filled pills used inside overlays and panels.
	pillActiveStyle = lipgloss.NewStyle().
			Foreground(white).
			Background(accent).
			Bold(true).
			Padding(0, 1)

	pillInactiveStyle = lipgloss.NewStyle().
				Foreground(dim).
				Padding(0, 1)

	titleStyle         = lipgloss.NewStyle().Bold(true).Foreground(accent)
	logoStyle          = lipgloss.NewStyle().Bold(true).Foreground(gentooPurple)
	selectedRowStyle   = lipgloss.NewStyle().Bold(true).Background(selBg)
	rowCursorStyle     = lipgloss.NewStyle().Bold(true).Foreground(accent)
	valueStyle         = lipgloss.NewStyle().Foreground(fg)
	profilePkgStyle    = lipgloss.NewStyle().Foreground(profileTeal)
	unsetStyle         = lipgloss.NewStyle().Foreground(faint)
	toggleOnStyle      = lipgloss.NewStyle().Bold(true).Foreground(good)
	toggleOffStyle     = lipgloss.NewStyle().Foreground(faint)
	badgeStyle         = lipgloss.NewStyle().Bold(true).Foreground(accent)
	errorStyle         = lipgloss.NewStyle().Foreground(bad).Bold(true)
	warnStyle          = lipgloss.NewStyle().Foreground(warn)
	okStyle            = lipgloss.NewStyle().Foreground(good)
	helpStyle          = lipgloss.NewStyle().Foreground(dim)
	cfgBoxValidStyle   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(good).Padding(0, 1)
	cfgBoxInvalidStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(bad).Padding(0, 1)
	cfgBoxWarnStyle    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(warn).Padding(0, 1)
	windowStyle        = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(dim).Padding(0, 1)
	tooSmallStyle      = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(warn).Foreground(warn).Bold(true).Padding(1, 3)
	modalBoxStyle      = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accent).Padding(1, 2)
	overlayBoxStyle    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(dim).Padding(0, 1)
	dirtyStyle         = lipgloss.NewStyle().Foreground(warn).Bold(true)
	treeGlyphStyle     = helpStyle
	treeRoleStyle      = lipgloss.NewStyle().Bold(true).Foreground(accent)
	pageStyle          = lipgloss.NewStyle().Padding(0, 1)
	hintKeyStyle       = lipgloss.NewStyle().Bold(true).Foreground(white)
	spinnerStyle       = lipgloss.NewStyle().Bold(true).Foreground(accent)
)
