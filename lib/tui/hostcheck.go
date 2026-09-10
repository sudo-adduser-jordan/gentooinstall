package tui

import "strings"

// Prereq reports the host prerequisites for an installation. It is
// collected by a callback injected from main (the TUI never imports the
// installer package) so the Install tab can surface missing programs and
// the root requirement before the user commits to a destructive install.
type Prereq struct {
	RootOK          bool
	MissingPrograms []string
}

// SetPrereq wires the host-prerequisite probe used by the Install tab.
// A nil function (the default) hides the section.
func (model *Model) SetPrereq(fn func() Prereq) { model.prereqFn = fn }

// renderPrereq renders the host-prerequisite check shown on the Install
// tab, or "" when no probe is wired (default in tests and the demo).
func (model *Model) renderPrereq(width int) string {
	if model.prereqFn == nil {
		return ""
	}
	prereq := model.prereqFn()
	var body strings.Builder
	if prereq.RootOK {
		body.WriteString("  " + okStyle.Render("✓") + " running as root\n")
	} else {
		body.WriteString("  " + errorStyle.Render("✗") +
			" not running as root — the installation requires root\n")
	}
	if len(prereq.MissingPrograms) == 0 {
		body.WriteString("  " + okStyle.Render("✓") +
			" all required programs present\n")
	} else {
		for _, program := range prereq.MissingPrograms {
			body.WriteString("  " + errorStyle.Render("✗") + " " +
				errorStyle.Render(program) + " — missing\n")
		}
		body.WriteString(warnStyle.Render(
			"  The installer can ask to install these (via apk) when it starts.\n"))
	}
	return body.String()
}
