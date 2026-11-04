package tui

import "strings"

type Prereq struct {
	RootOK          bool
	MissingPrograms []string
}

func (model *Model) SetPrereq(fn func() Prereq) { model.prereqFn = fn }

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
