package tui

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func (m *Model) openHelp(title, body string) {
	m.overlay = overlay{kind: ovHelp, title: title, body: body}
}

// openProfilePackages opens a scrollable modal listing the packages that the
// currently selected profile installs.
func (m *Model) openProfilePackages() {
	m.overlay = overlay{kind: ovPackages, title: ePackage + " Packages installed by profile"}
}

func (m *Model) openPicker(title string, opts []option, current string, filter bool,
	onPick func(*Model, string)) {
	in := textinput.New()
	in.Placeholder = "🔍 type to filter…"
	if filter {
		in.Focus()
	}
	m.overlay = overlay{
		kind: ovPicker, title: title, opts: opts, input: in,
		current: current,
	}
	for i, o := range opts {
		if o.Value == current {
			m.overlay.cursor = i
		}
	}
	m.pickFn = onPick
}

func (m *Model) openText(title, value string, multi bool, onDone func(*Model, string)) {
	m.openTextWithNote(title, value, "", multi, onDone)
}

func (m *Model) openTextWithNote(title, value, note string, multi bool,
	onDone func(*Model, string)) {
	if multi {
		ta := textarea.New()
		ta.SetValue(value)
		ta.Focus()
		ta.SetWidth(70)
		ta.SetHeight(maxInt(3, minInt(8, strings.Count(value, "\n")+3)))
		ta.ShowLineNumbers = false
		m.overlay = overlay{kind: ovText, title: title, note: note, area: ta}
	} else {
		ti := textinput.New()
		ti.SetValue(value)
		ti.Focus()
		ti.CharLimit = 0
		ti.Width = 70
		m.overlay = overlay{kind: ovText, title: title, note: note, input: ti}
	}
	m.textFn = &textState{multi: multi, fn: onDone}
}

type textState struct {
	multi bool
	fn    func(*Model, string)
}

func (m *Model) openMultiPicker(title string, opts []option, current []string,
	fn func(*Model, []string)) {
	sel := map[string]bool{}
	for _, v := range current {
		sel[v] = true
	}
	in := textinput.New()
	in.Placeholder = "🔍 type to filter…"
	in.Focus()
	m.overlay = overlay{
		kind: ovPicker, title: title, opts: opts, input: in,
		filter: "", multiChoice: true, selected: sel,
	}
	// Start on the first selected entry.
	for i, o := range opts {
		if sel[o.Value] {
			m.overlay.cursor = i
			break
		}
	}
	m.multiFn = fn
}

// updatePickerKeys handles key input for pickers; routed reports whether
// the key belongs to the filter input.
func (m *Model) updatePickerKeys(msg tea.KeyMsg) (routed bool, cmd tea.Cmd) {
	switch msg.String() {
	case "down", "ctrl+n":
		if m.overlay.cursor < len(m.filteredOpts())-1 {
			m.overlay.cursor++
		}
		return false, nil
	case "up", "ctrl+p":
		if m.overlay.cursor > 0 {
			m.overlay.cursor--
		}
		return false, nil
	default:
		var c tea.Cmd
		before := m.overlay.filter
		m.overlay.input, c = m.overlay.input.Update(msg)
		m.overlay.filter = m.overlay.input.Value()
		if before != m.overlay.filter {
			m.overlay.cursor = 0
		}
		return true, c
	}
}

// handleViewportKeys applies scroll keys to the shared log viewport used
// by the log/config/make.conf/packages overlays.
func (m *Model) handleViewportKeys(msg tea.KeyMsg) {
	switch msg.String() {
	case "down", "j":
		m.logVp.LineDown(1)
	case "up", "k":
		m.logVp.LineUp(1)
	case "pgdown", "ctrl+f", " ":
		m.logVp.HalfViewDown()
	case "pgup", "ctrl+b":
		m.logVp.HalfViewUp()
	case "home", "g":
		m.logVp.GotoTop()
	case "end", "G":
		m.logVp.GotoBottom()
	}
}

func (m *Model) updateOverlay(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.overlay.kind {

	case ovHelp:
		m.closeOverlay()
		return m, nil

	case ovLog:
		switch msg.String() {
		case "esc", "l", "q":
			m.closeOverlay()
		default:
			m.handleViewportKeys(msg)
		}
		return m, nil

	case ovConfig:
		switch msg.String() {
		case "esc", "v", "q":
			m.closeOverlay()
		default:
			m.handleViewportKeys(msg)
		}
		return m, nil

	case ovMakeConf:
		switch msg.String() {
		case "esc", "q":
			m.closeOverlay()
		default:
			m.handleViewportKeys(msg)
		}
		return m, nil

	case ovPackages:
		switch msg.String() {
		case "esc", "q":
			m.closeOverlay()
		default:
			m.handleViewportKeys(msg)
		}
		return m, nil

	case ovPicker:
		switch msg.String() {
		case "esc":
			m.closeOverlay()
			return m, nil
		case "enter":
			opts := m.filteredOpts()
			if m.overlay.multiChoice {
				var vals []string
				for _, o := range m.overlay.opts { // preserve original order
					if m.overlay.selected[o.Value] {
						vals = append(vals, o.Value)
					}
				}
				fn := m.multiFn
				m.closeOverlay()
				if fn != nil {
					fn(m, vals)
				}
				return m, nil
			}
			if len(opts) > 0 && m.overlay.cursor < len(opts) {
				v := opts[m.overlay.cursor].Value
				fn := m.pickFn
				m.closeOverlay()
				if fn != nil {
					fn(m, v)
				}
			}
			return m, nil
		case " ", "space":
			// For multi-choice, Space toggles the focused row regardless of
			// filter focus (so users can search then toggle). Package/repo
			// names don't need a literal space in the filter.
			if m.overlay.multiChoice {
				opts := m.filteredOpts()
				if m.overlay.cursor < len(opts) {
					if m.overlay.selected == nil {
						m.overlay.selected = map[string]bool{}
					}
					v := opts[m.overlay.cursor].Value
					m.overlay.selected[v] = !m.overlay.selected[v]
				}
				return m, nil
			}
			routed, cmd := m.updatePickerKeys(msg)
			if routed && cmd != nil {
				return m, cmd
			}
			return m, nil
		default:
			routed, cmd := m.updatePickerKeys(msg)
			if !routed {
				return m, nil
			}
			return m, cmd
		}

	case ovText:
		isArea := m.textFn != nil && m.textFn.multi
		switch msg.String() {
		case "esc":
			m.closeOverlay()
			return m, nil
		case "enter":
			if isArea {
				break // newline goes to the editor below
			}
			v := strings.TrimSpace(m.overlay.input.Value())
			st := m.textFn
			m.closeOverlay()
			if st != nil && st.fn != nil {
				st.fn(m, v)
			}
			cmd := m.deferredCmd
			m.deferredCmd = nil
			return m, cmd
		case "ctrl+d":
			if isArea {
				v := strings.TrimSpace(m.overlay.area.Value())
				st := m.textFn
				m.closeOverlay()
				if st != nil && st.fn != nil {
					st.fn(m, v)
				}
				cmd := m.deferredCmd
				m.deferredCmd = nil
				return m, cmd
			}
		}
		var cmd tea.Cmd
		if isArea {
			m.overlay.area, cmd = m.overlay.area.Update(msg)
		} else {
			m.overlay.input, cmd = m.overlay.input.Update(msg)
		}
		return m, cmd

	case ovButtons:
		switch msg.String() {
		case "ctrl+c":
			m.quitNow()
			return m, tea.Quit
		case "esc":
			m.closeOverlay()
		case "left", "h":
			if m.overlay.btnCur > 0 {
				m.overlay.btnCur--
			}
		case "right", "l":
			if m.overlay.btnCur < len(m.overlay.buttons)-1 {
				m.overlay.btnCur++
			}
		case "enter":
			i := m.overlay.btnCur
			fn := m.overlay.onBtn
			m.overlay.kind = ovNone
			if fn != nil {
				fn(m, i)
			}
		}
		return m, nil
	}
	return m, nil
}

func (m *Model) closeOverlay() {
	m.pickFn = nil
	m.textFn = nil
	m.multiFn = nil
	m.overlay.kind = ovNone
}

func (m *Model) filteredOpts() []option {
	f := strings.ToLower(strings.TrimSpace(m.overlay.filter))
	if f == "" {
		return m.overlay.opts
	}
	var out []option
	for _, o := range m.overlay.opts {
		if strings.Contains(strings.ToLower(o.Value), f) ||
			strings.Contains(strings.ToLower(o.Desc), f) {
			out = append(out, o)
		}
	}
	return out
}

const pickerVisibleRows = 14

func (m *Model) renderOverlay() string {
	w := maxInt(40, minInt(90, m.width-6))
	switch m.overlay.kind {
	case ovHelp:
		body := wrapText(m.overlay.body, minInt(78, maxInt(40, m.width-12)))
		content := titleStyle.Render("❓ "+m.overlay.title) + "\n\n" + body +
			"\n\n" + helpStyle.Render("Press any key to return")
		return modalBoxStyle.MaxWidth(w).Render(content)

	case ovLog:
		h := maxInt(6, m.height-8)
		bw := maxInt(40, minInt(110, m.width-10))
		if m.logVp.Width != bw-2 || m.logVp.Height != h-2 {
			m.logVp = viewport.New(bw-2, h-2)
		}
		m.logVp.SetContent(strings.Join(m.instRawLines, "\n"))
		box := overlayBoxStyle.Width(bw).Height(h).Render(
			titleStyle.Render(eScroll+" Raw output") + "\n\n" + m.logVp.View())
		hint := helpStyle.Render("↑↓ scroll · l/Esc close")
		return lipgloss.JoinVertical(lipgloss.Center, box, hint)

	case ovConfig:
		h := maxInt(6, m.height-8)
		bw := maxInt(40, minInt(90, m.width-12))
		if m.logVp.Width != bw-2 || m.logVp.Height != h-4 {
			m.logVp = viewport.New(bw-2, h-4)
		}
		m.logVp.SetContent(highlightTOML(m.cfg.String()))
		box := overlayBoxStyle.Width(bw).Height(h).Render(
			titleStyle.Render(eInfo+" "+filepath.Base(m.cfgPath)) + "\n\n" + m.logVp.View() + "\n" +
				helpStyle.Render("↑↓ scroll · v/Esc close"))
		return lipgloss.JoinVertical(lipgloss.Center, box)

	case ovMakeConf:
		h := maxInt(6, m.height-8)
		bw := maxInt(40, minInt(90, m.width-12))
		if m.logVp.Width != bw-2 || m.logVp.Height != h-4 {
			m.logVp = viewport.New(bw-2, h-4)
		}
		jobs := runtime.NumCPU()
		if jobs < 2 {
			jobs = 2
		}
		m.logVp.SetContent(makeConfViewContent(m.cfg, jobs))
		box := overlayBoxStyle.Width(bw).Height(h).Render(
			titleStyle.Render(ePencil+" make.conf") + "\n\n" + m.logVp.View() + "\n" +
				helpStyle.Render("↑↓ scroll · q/Esc close"))
		return lipgloss.JoinVertical(lipgloss.Center, box)

	case ovPackages:
		h := maxInt(6, m.height-8)
		bw := maxInt(40, minInt(90, m.width-12))
		if m.logVp.Width != bw-2 || m.logVp.Height != h-4 {
			m.logVp = viewport.New(bw-2, h-4)
		}
		var b strings.Builder
		pkgs := m.cfg.ProfilePackages()
		if len(pkgs) == 0 {
			b.WriteString(unsetStyle.Render("(no packages are defined for the selected profile)\n"))
		} else {
			for _, p := range pkgs {
				b.WriteString(profilePkgStyle.Render("  • "+p) + "\n")
			}
		}
		m.logVp.SetContent(b.String())
		box := overlayBoxStyle.Width(bw).Height(h).Render(
			titleStyle.Render(m.overlay.title) + "\n\n" + m.logVp.View() + "\n" +
				helpStyle.Render("↑↓ scroll · q/Esc close"))
		return lipgloss.JoinVertical(lipgloss.Center, box)

	case ovPicker:
		opts := m.filteredOpts()
		cur := m.overlay.cursor
		start := 0
		if cur >= pickerVisibleRows {
			start = cur - pickerVisibleRows + 1
		}
		end := start + pickerVisibleRows
		if end > len(opts) {
			end = len(opts)
			start = maxInt(0, end-pickerVisibleRows)
		}
		titleLine := titleStyle.Render(m.overlay.title)
		counter := unsetStyle.Render(strconv.Itoa(min(cur+1, maxInt(1, len(opts)))) + "/" + strconv.Itoa(len(opts)))
		gap := maxInt(1, w-lipgloss.Width(titleLine)-lipgloss.Width(counter)-4)
		head := titleLine + strings.Repeat(" ", gap) + counter

		var b strings.Builder
		b.WriteString(head + "\n")
		if m.overlay.filter != "" || m.overlay.input.Focused() {
			b.WriteString(m.overlay.input.View() + "\n")
		}
		if start > 0 {
			b.WriteString(unsetStyle.Render("  ▲ more") + "\n")
		}
		trunc := lipgloss.NewStyle().MaxWidth(w - 4)
		for i := start; i < end; i++ {
			o := opts[i]
			marker := "  "
			style := lipgloss.NewStyle()
			if i == cur {
				style = selectedRowStyle
				if !m.overlay.multiChoice {
					marker = rowCursorStyle.Render("▌ ")
				}
			}
			if m.overlay.multiChoice {
				if m.overlay.selected[o.Value] {
					marker += "[" + okStyle.Render("✓") + "] "
				} else {
					marker += "[ ] "
				}
			}
			line := marker + o.Value
			if o.primaryDesc {
				// Prefer the friendly description as the primary label.
				primary := o.Desc
				if primary == "" {
					primary = o.Value
				}
				line = marker + primary
				if primary != o.Value {
					line += "  " + unsetStyle.Render(o.Value)
				}
			}
			if !m.overlay.multiChoice && o.Value == m.overlay.current {
				line += " " + okStyle.Render("✓")
			}
			if o.Desc != "" && !o.primaryDesc {
				line += "  " + unsetStyle.Render(o.Desc)
			}
			b.WriteString(trunc.Render(style.Render(line)) + "\n")
		}
		if end < len(opts) {
			b.WriteString(unsetStyle.Render("  ▼ more") + "\n")
		}
		if len(opts) == 0 {
			b.WriteString(unsetStyle.Render("no matches") + "\n")
		}
		hint := "↑↓ navigate · Enter select · Esc cancel"
		if m.overlay.multiChoice {
			nSel := 0
			for _, v := range m.overlay.selected {
				if v {
					nSel++
				}
			}
			hint = fmt.Sprintf("↑↓ navigate · Space toggle (%d selected) · Enter apply · Esc cancel", nSel)
		}
		if m.overlay.input.Focused() {
			hint += " · type to filter"
		}
		b.WriteString(helpStyle.Render(hint))
		return overlayBoxStyle.MaxWidth(w).Render(b.String())

	case ovText:
		var fieldView string
		if m.textFn != nil && m.textFn.multi {
			fieldView = m.overlay.area.View()
		} else {
			fieldView = m.overlay.input.View()
		}
		content := titleStyle.Render(ePencil+" "+stripLeadingEmoji(m.overlay.title)) + "\n"
		if m.overlay.note != "" {
			content += warnStyle.Render(m.overlay.note) + "\n"
		}
		hint := "Enter confirm · Esc cancel"
		if m.textFn != nil && m.textFn.multi {
			hint = "one entry per line · Ctrl+D confirm · Esc cancel"
		}
		content += "\n" + fieldView + "\n\n" + helpStyle.Render(hint)
		return modalBoxStyle.MaxWidth(w).Render(content)

	case ovButtons:
		var b strings.Builder
		b.WriteString(titleStyle.Render(eWarn+" "+stripLeadingEmoji(m.overlay.title)) + "\n\n")
		b.WriteString(wrapText(m.overlay.body, minInt(64, w-8)) + "\n\n")
		var pills []string
		for i, btn := range m.overlay.buttons {
			s := pillInactiveStyle
			if i == m.overlay.btnCur {
				s = pillActiveStyle
			}
			pills = append(pills, s.Render(btn))
		}
		b.WriteString(lipgloss.JoinHorizontal(lipgloss.Center, pills...))
		b.WriteString("\n" + helpStyle.Render("←→ choose · Enter confirm · Esc cancel"))
		return modalBoxStyle.MaxWidth(w).Render(b.String())
	}
	return ""
}

// stripLeadingEmoji removes an emoji prefix added at call sites so titles
// are not decorated twice.
func stripLeadingEmoji(s string) string {
	for _, e := range []string{eWarn + " ", eInfo + " ", ePencil + " ", eScroll + " "} {
		if rest, ok := strings.CutPrefix(s, e); ok {
			return rest
		}
	}
	return s
}

// wrapText wraps s at width runes per line, preserving blank lines.
func wrapText(s string, width int) string {
	width = maxInt(10, width)
	var out []string
	for _, para := range strings.Split(s, "\n") {
		if para == "" {
			out = append(out, "")
			continue
		}
		line := ""
		lineLen := 0
		for _, w := range strings.Fields(para) {
			wl := utf8.RuneCountInString(w)
			if line == "" {
				line = w
				lineLen = wl
			} else if lineLen+1+wl <= width {
				line += " " + w
				lineLen += 1 + wl
			} else {
				out = append(out, line)
				line = w
				lineLen = wl
			}
		}
		if line != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}
