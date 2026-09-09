package tui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"

	"gentooinstall/lib/config"
)

// field is one editable row inside a tab.
type field struct {
	label string
	help  string
	vis   func(*config.Config) bool
	summ  func(*config.Config) string
	kind  fieldKind
	// toggle
	getBool func(*config.Config) bool
	setBool func(*config.Config, bool)
	// text
	getText func(*config.Config) string
	setText func(*config.Config, string)
	multi   bool // textarea (newline separated)
	// choice / picker
	options   func(*config.Config) []option
	getChoice func(*config.Config) string
	setChoice func(*config.Config, string)
	onPick    func(*Model, *field, string) // optional custom pick handler
	filter    bool
	// multi choice
	getStrings  func(*config.Config) []string
	setStrings  func(*config.Config, []string)
	multiChoice bool

	// watchMirror marks the "Gentoo mirror" text row so editing it re-probes
	// the mirror reachability indicator.
	watchMirror bool
}

type fieldKind int

const (
	kToggle fieldKind = iota
	kText
	kMultiText
	kChoice
	kSeparator
	kMultiChoice
	kReadOnly
)

type option struct {
	Value       string
	Desc        string
	primaryDesc bool // render Desc as the leading label instead of Value
}

func sep(label string) *field {
	return &field{label: label, kind: kSeparator}
}

// readOnly builds a non-editable display row. Its value is rendered in the
// given style. get returns the string to display. An optional onPick handler
// is invoked when the row is activated (e.g. to open a picker or modal);
// when nil, activation shows the field's help.
func readOnly(label, help string, get func(*config.Config) string, style lipgloss.Style,
	onPick ...func(*Model, *field, string)) *field {
	fld := &field{
		label: label, help: help, kind: kReadOnly,
		getText: func(cc *config.Config) string { return get(cc) },
		vis:     func(*config.Config) bool { return true },
		summ: func(cc *config.Config) string {
			subject := get(cc)
			if subject == "" {
				return unsetStyle.Render("none")
			}
			return style.Render(subject)
		},
	}
	if len(onPick) > 0 {
		fld.onPick = onPick[0]
	}
	return fld
}

func toggle(label, help string, get func(*config.Config) bool, set func(*config.Config, bool)) *field {
	return &field{label: label, help: help, kind: kToggle, getBool: get, setBool: set,
		vis: func(*config.Config) bool { return true }}
}

func text(label, help string, get func(*config.Config) string,
	set func(*config.Config, string)) *field {
	return &field{label: label, help: help, kind: kText, getText: get, setText: set,
		vis: func(*config.Config) bool { return true }}
}

func multiText(label, help string, get func(*config.Config) string,
	set func(*config.Config, string)) *field {
	return &field{label: label, help: help, kind: kMultiText, getText: get, setText: set,
		multi: true,
		vis:   func(*config.Config) bool { return true }}
}

func choice(label string, opts func() []option, cur func(*config.Config) string,
	set func(*config.Config, string), help string) *field {
	return &field{label: label, help: help, kind: kChoice, options: func(*config.Config) []option {
		return opts()
	}, getChoice: cur, setChoice: set,
		vis: func(*config.Config) bool { return true }, filter: false}
}

func filteredChoice(label string, opts func() []option, cur func(*config.Config) string,
	set func(*config.Config, string), help string) *field {
	field := choice(label, opts, cur, set, help)
	field.filter = true
	return field
}

func visible(field *field, cfg *config.Config) bool {
	if field.vis == nil {
		return true
	}
	return field.vis(cfg)
}

func summaryOf(field *field, cfg *config.Config) string {
	if field.summ != nil {
		return field.summ(cfg)
	}
	switch field.kind {
	case kToggle:
		if field.getBool(cfg) {
			return toggleOnStyle.Render("●") + " on"
		}
		return toggleOffStyle.Render("○") + " off"
	case kText, kMultiText:
		subject := field.getText(cfg)
		if subject == "" {
			return unsetStyle.Render("unset")
		}
		return subject
	case kChoice:
		value := field.getChoice(cfg)
		if value == "" {
			return unsetStyle.Render("unset")
		}
		return value
	case kMultiChoice:
		count := len(field.getStrings(cfg))
		if count == 0 {
			return unsetStyle.Render("none")
		}
		return badgeStyle.Render(fmt.Sprintf("%d selected", count))
	}
	return ""
}
