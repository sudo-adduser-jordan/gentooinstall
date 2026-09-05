package tui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"

	"gentooinstall/internal/config"
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
	f := &field{
		label: label, help: help, kind: kReadOnly,
		getText: func(cc *config.Config) string { return get(cc) },
		vis:     func(*config.Config) bool { return true },
		summ: func(cc *config.Config) string {
			s := get(cc)
			if s == "" {
				return unsetStyle.Render("none")
			}
			return style.Render(s)
		},
	}
	if len(onPick) > 0 {
		f.onPick = onPick[0]
	}
	return f
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
	f := choice(label, opts, cur, set, help)
	f.filter = true
	return f
}

func visible(f *field, c *config.Config) bool {
	if f.vis == nil {
		return true
	}
	return f.vis(c)
}

func summaryOf(f *field, c *config.Config) string {
	if f.summ != nil {
		return f.summ(c)
	}
	switch f.kind {
	case kToggle:
		if f.getBool(c) {
			return toggleOnStyle.Render("●") + " on"
		}
		return toggleOffStyle.Render("○") + " off"
	case kText, kMultiText:
		s := f.getText(c)
		if s == "" {
			return unsetStyle.Render("unset")
		}
		return s
	case kChoice:
		v := f.getChoice(c)
		if v == "" {
			return unsetStyle.Render("unset")
		}
		return v
	case kMultiChoice:
		n := len(f.getStrings(c))
		if n == 0 {
			return unsetStyle.Render("none")
		}
		return badgeStyle.Render(fmt.Sprintf("%d selected", n))
	}
	return ""
}
