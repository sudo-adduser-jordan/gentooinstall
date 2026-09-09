package tui

import (
	"strings"
	"unicode"

	"github.com/charmbracelet/lipgloss"
)

var (
	tomlCommentStyle = lipgloss.NewStyle().Foreground(dim)
	tomlSectionStyle = lipgloss.NewStyle().Bold(true).Foreground(accent)
	tomlKeyStyle     = lipgloss.NewStyle().Bold(true).Foreground(fg)
	tomlStringStyle  = lipgloss.NewStyle().Foreground(good)
	tomlBoolStyle    = lipgloss.NewStyle().Foreground(warn)
	tomlNumberStyle  = lipgloss.NewStyle().Foreground(accent)
	tomlQuoteStyle   = lipgloss.NewStyle().Foreground(faint)
	tomlEqStyle      = lipgloss.NewStyle().Foreground(dim)
)

// highlightTOML returns s with TOML syntax coloured via lipgloss ANSI
// escape sequences. The result can be fed directly to a viewport.
func highlightTOML(src string) string {
	var out strings.Builder
	for idx, line := range strings.Split(src, "\n") {
		if idx > 0 {
			out.WriteByte('\n')
		}
		hlLine(&out, line)
	}
	return out.String()
}

func hlLine(out *strings.Builder, line string) {
	trimmed := strings.TrimLeft(line, " \t")
	indent := line[:len(line)-len(trimmed)]

	// blank line
	if trimmed == "" {
		out.WriteString(line)
		return
	}

	// comment
	if trimmed[0] == '#' {
		out.WriteString(tomlCommentStyle.Render(line))
		return
	}

	// [section] or [[array-of-tables]]
	if strings.HasPrefix(trimmed, "[") {
		out.WriteString(indent)
		out.WriteString(tomlSectionStyle.Render(trimmed))
		return
	}

	// key = value
	if eq := strings.Index(trimmed, "="); eq >= 0 {
		keyPart := trimmed[:eq]
		valPart := trimmed[eq:]

		out.WriteString(indent)
		hlKey(out, strings.TrimSpace(keyPart))

		// "=" and trailing value
		if len(valPart) > 0 {
			// the "="
			out.WriteString(tomlEqStyle.Render(string(valPart[0])))
			rest := valPart[1:]
			hlValue(out, rest)
		}
		return
	}

	// fallback — unrecognised line, render dim
	out.WriteString(tomlCommentStyle.Render(line))
}

// hlKey highlights a TOML key (may be bare or "quoted").
func hlKey(out *strings.Builder, key string) {
	if len(key) >= 2 && key[0] == '"' && key[len(key)-1] == '"' {
		out.WriteString(tomlQuoteStyle.Render(`"`))
		out.WriteString(tomlKeyStyle.Render(key[1 : len(key)-1]))
		out.WriteString(tomlQuoteStyle.Render(`"`))
		return
	}
	out.WriteString(tomlKeyStyle.Render(key))
}

// hlValue highlights the value portion after the '=' sign.
func hlValue(out *strings.Builder, val string) {
	val = strings.TrimSpace(val)
	if val == "" {
		return
	}

	// multi-line basic string
	if strings.HasPrefix(val, `"""`) || strings.HasPrefix(val, "'''") {
		out.WriteString(tomlStringStyle.Render(val))
		return
	}

	// quoted string
	if val[0] == '"' || val[0] == '\'' {
		hlQuotedString(out, val)
		return
	}

	// inline array
	if val[0] == '[' {
		out.WriteString(tomlStringStyle.Render(val))
		return
	}

	// inline table
	if val[0] == '{' {
		out.WriteString(tomlStringStyle.Render(val))
		return
	}

	// bare word: bool / date / number / inf / nan
	lower := strings.ToLower(val)
	if lower == "true" || lower == "false" {
		out.WriteString(tomlBoolStyle.Render(val))
		return
	}
	if lower == "inf" || lower == "+inf" || lower == "-inf" ||
		lower == "nan" || lower == "+nan" || lower == "-nan" {
		out.WriteString(tomlNumberStyle.Render(val))
		return
	}
	if isTOMLNumber(val) || isTOMLDate(val) {
		out.WriteString(tomlNumberStyle.Render(val))
		return
	}

	// fallback
	out.WriteString(tomlStringStyle.Render(val))
}

// hlQuotedString highlights a single or double-quoted string, colouring
// the delimiters faint and the content green.
func hlQuotedString(out *strings.Builder, val string) {
	quote := byte(val[0])
	// find closing quote (skip escaped quotes)
	end := -1
	for idx := 1; idx < len(val); idx++ {
		if val[idx] == quote && val[idx-1] != '\\' {
			end = idx
			break
		}
	}
	if end < 0 {
		// unclosed — highlight whole thing
		out.WriteString(tomlStringStyle.Render(val))
		return
	}
	out.WriteString(tomlQuoteStyle.Render(string(quote)))
	out.WriteString(tomlStringStyle.Render(val[1:end]))
	out.WriteString(tomlQuoteStyle.Render(string(quote)))
	if end+1 < len(val) {
		// trailing content after closing quote (e.g. comment)
		out.WriteString(tomlCommentStyle.Render(val[end+1:]))
	}
}

func isTOMLNumber(val string) bool {
	if val == "" {
		return false
	}
	idx := 0
	if val[0] == '+' || val[0] == '-' {
		idx = 1
	}
	if idx >= len(val) {
		return false
	}
	if val[idx] == '0' && idx+1 < len(val) && val[idx+1] == 'x' {
		// hex
		idx += 2
		if idx >= len(val) {
			return false
		}
		for ; idx < len(val); idx++ {
			ch := val[idx]
			if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F') || ch == '_') {
				return false
			}
		}
		return true
	}
	// decimal / octal / binary
	hasDot := false
	for ; idx < len(val); idx++ {
		ch := val[idx]
		if ch == '_' {
			continue
		}
		if ch == '.' && !hasDot {
			hasDot = true
			continue
		}
		if ch == 'e' || ch == 'E' {
			// exponent
			idx++
			if idx < len(val) && (val[idx] == '+' || val[idx] == '-') {
				idx++
			}
			for ; idx < len(val); idx++ {
				if !unicode.IsDigit(rune(val[idx])) {
					return false
				}
			}
			return true
		}
		if !unicode.IsDigit(rune(ch)) {
			return false
		}
	}
	return true
}

func isTOMLDate(val string) bool {
	// rough check: starts with a digit and contains '-' or 'T' or ':'
	if len(val) < 4 || !unicode.IsDigit(rune(val[0])) {
		return false
	}
	for _, ch := range val {
		if ch == '-' || ch == 'T' || ch == ':' || ch == 'Z' || ch == '+' || ch == '.' {
			return true
		}
	}
	return false
}
