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

func (model *Model) openHelp(title, body string) {
	model.overlay = overlay{kind: ovHelp, title: title, body: body}
}

// openProfilePackages opens a scrollable modal listing the packages that the
// currently selected profile installs.
func (model *Model) openProfilePackages() {
	model.overlay = overlay{kind: ovPackages, title: ePackage + " Packages installed by profile"}
}

func (model *Model) openPicker(title string, opts []option, current string, filter bool,
	onPick func(*Model, string)) {
	in := textinput.New()
	in.Placeholder = "🔍 type to filter…"
	if filter {
		in.Focus()
	}
	model.overlay = overlay{
		kind: ovPicker, title: title, opts: opts, input: in,
		current: current,
	}
	for idx, opt := range opts {
		if opt.Value == current {
			model.overlay.cursor = idx
		}
	}
	model.pickFn = onPick
}

func (model *Model) openText(title, value string, multi bool, onDone func(*Model, string)) {
	model.openTextWithNote(title, value, "", multi, onDone)
}

func (model *Model) openTextWithNote(title, value, note string, multi bool,
	onDone func(*Model, string)) {
	if multi {
		ta := textarea.New()
		ta.SetValue(value)
		ta.Focus()
		ta.SetWidth(70)
		ta.SetHeight(maxInt(3, minInt(8, strings.Count(value, "\n")+3)))
		ta.ShowLineNumbers = false
		model.overlay = overlay{kind: ovText, title: title, note: note, area: ta}
	} else {
		ti := textinput.New()
		ti.SetValue(value)
		ti.Focus()
		ti.CharLimit = 0
		ti.Width = 70
		model.overlay = overlay{kind: ovText, title: title, note: note, input: ti}
	}
	model.textFn = &textState{multi: multi, fn: onDone}
}

type textState struct {
	multi bool
	fn    func(*Model, string)
}

func (model *Model) openMultiPicker(title string, opts []option, current []string,
	fn func(*Model, []string)) {
	sel := map[string]bool{}
	for _, val := range current {
		sel[val] = true
	}
	in := textinput.New()
	in.Placeholder = "🔍 type to filter…"
	in.Focus()
	model.overlay = overlay{
		kind: ovPicker, title: title, opts: opts, input: in,
		filter: "", multiChoice: true, selected: sel,
	}
	// Start on the first selected entry.
	for idx, opt := range opts {
		if len(opt.groupDirs) > 0 {
			continue
		}
		if sel[opt.Value] {
			model.overlay.cursor = idx
			break
		}
	}
	model.multiFn = fn
}

// updatePickerKeys handles key input for pickers; routed reports whether
// the key belongs to the filter input.
func (model *Model) updatePickerKeys(msg tea.KeyMsg) (routed bool, cmd tea.Cmd) {
	switch msg.String() {
	case "down", "ctrl+n":
		if model.overlay.cursor < len(model.filteredOpts())-1 {
			model.overlay.cursor++
		}
		return false, nil
	case "up", "ctrl+p":
		if model.overlay.cursor > 0 {
			model.overlay.cursor--
		}
		return false, nil
	default:
		var cmd tea.Cmd
		before := model.overlay.filter
		model.overlay.input, cmd = model.overlay.input.Update(msg)
		model.overlay.filter = model.overlay.input.Value()
		if before != model.overlay.filter {
			model.overlay.cursor = 0
		}
		return true, cmd
	}
}

// handleRawViewportKeys scrolls the dedicated raw output viewport. Any
// scroll away from the bottom pauses tail-follow; reaching the bottom
// (via keys or End/G) resumes it.
func (model *Model) handleRawViewportKeys(msg tea.KeyMsg) {
	switch msg.String() {
	case "down", "j":
		model.rawVp.LineDown(1)
		model.rawFollow = model.rawVp.AtBottom()
	case "up", "k":
		model.rawVp.LineUp(1)
		model.rawFollow = model.rawVp.AtBottom()
	case "pgdown", "ctrl+f", " ":
		model.rawVp.HalfViewDown()
		model.rawFollow = model.rawVp.AtBottom()
	case "pgup", "ctrl+b":
		model.rawVp.HalfViewUp()
		model.rawFollow = model.rawVp.AtBottom()
	case "home", "g":
		model.rawVp.GotoTop()
		model.rawFollow = false
	case "end", "G":
		model.rawVp.GotoBottom()
		model.rawFollow = true
	}
}

// syncRawViewport sizes the dedicated raw viewport, incrementally
// colorizes lines appended since the last render, and refreshes the
// viewport content only when something changed. New output jumps to the
// bottom while tail-follow is on; a scrolled-up viewport keeps its offset.
func (model *Model) syncRawViewport(width, viewportHeight int) {
	if model.rawVp.Width != width || model.rawVp.Height != viewportHeight {
		model.rawVp = viewport.New(width, viewportHeight)
		model.rawDirty = true
	}
	if len(model.rawContent) > len(model.instRawLines) {
		// instRawLines is capped (oldest lines dropped); drop the same
		// prefix from the colorized cache so indexes stay aligned.
		model.rawContent = append([]string(nil),
			model.rawContent[len(model.rawContent)-len(model.instRawLines):]...)
		model.rawDirty = true
	}
	for idx := len(model.rawContent); idx < len(model.instRawLines); idx++ {
		stripped := ""
		if idx < len(model.instLines) {
			stripped = model.instLines[idx]
		} else {
			stripped = stripAnsi(model.instRawLines[idx])
		}
		model.rawContent = append(model.rawContent, colorizeRawLine(stripped, model.instRawLines[idx]))
		model.rawDirty = true
	}
	if model.rawDirty {
		model.rawVp.SetContent(strings.Join(model.rawContent, "\n"))
		model.rawDirty = false
		if model.rawFollow {
			model.rawVp.GotoBottom()
		}
	} else if model.rawFollow && !model.rawVp.AtBottom() {
		model.rawVp.GotoBottom()
	}
}

// colorizeRawLine paints one raw output line for the raw window. Lines that
// already carry ANSI codes (e.g. the demo stream) pass through untouched;
// plain lines — the common case for piped child output, which disables its
// own colors when stdout isn't a TTY — get fallback highlighting so steps,
// commands, atoms and failures are distinguishable.
//
// Explicit SGR codes (shared with demo.go) are used instead of lipgloss
// styles so colors survive dumb/test terminals where lipgloss downgrades to
// ASCII and would otherwise render plain text.
func colorizeRawLine(stripped, raw string) string {
	if strings.Contains(raw, "\x1b") {
		return raw
	}
	lineContent := stripped
	switch {
	case strings.HasPrefix(lineContent, "[+]"):
		name := strings.TrimSpace(strings.TrimPrefix(lineContent, "[+]"))
		return demoCyan + "[+]" + demoReset + " " + demoBold + name + demoReset
	case strings.HasPrefix(lineContent, "[!]"):
		return demoRed + lineContent + demoReset
	case strings.HasPrefix(lineContent, "$ "):
		return colorizeCmdLine(lineContent)
	}
	lower := strings.ToLower(lineContent)
	switch {
	case strings.Contains(lower, "fail"),
		strings.Contains(lower, "error"),
		strings.Contains(lower, "abort"),
		strings.Contains(lower, "not found"),
		strings.Contains(lower, "denied"),
		strings.Contains(lower, "no such"):
		return demoRed + lineContent + demoReset
	case strings.Contains(lower, "warn"):
		return demoYellow + lineContent + demoReset
	case strings.Contains(lineContent, "://") || strings.Contains(lineContent, "/dev/") ||
		strings.HasSuffix(lineContent, ".tar.xz"):
		return demoCyan + lineContent + demoReset
	}
	return highlightAtoms(lineContent)
}

// colorizeCmdLine paints "$ cmd args", highlighting package atoms in green.
func colorizeCmdLine(commandLine string) string {
	rest := strings.TrimPrefix(commandLine, "$ ")
	prompt := demoYellow + "$" + demoReset
	if rest == "" {
		return prompt
	}
	// Keep the dimmed command style across green atoms: re-apply dim after
	// each atom's reset so trailing args don't lose the dimming.
	args := strings.ReplaceAll(highlightAtomsPlain(rest), demoReset, demoReset+demoDim)
	return prompt + " " + demoDim + args + demoReset
}

// highlightAtoms paints whitespace-separated category/name tokens green,
// leaving the rest of the line untouched.
func highlightAtoms(subject string) string {
	out := highlightAtomsPlain(subject)
	if out == subject {
		return subject
	}
	return out
}

// highlightAtomsPlain is the ANSI-core of highlightAtoms so callers can nest
// it inside other sequences (e.g. the dimmed command line).
func highlightAtomsPlain(subject string) string {
	parts := strings.Split(subject, " ")
	painted := false
	for idx, part := range parts {
		if strings.Contains(part, "/") {
			parts[idx] = demoGreen + part + demoReset
			painted = true
		}
	}
	if !painted {
		return subject
	}
	return strings.Join(parts, " ")
}

// handleViewportKeys applies scroll keys to the shared log viewport used
// by the log/config/make.conf/packages overlays.
func (model *Model) handleViewportKeys(msg tea.KeyMsg) {
	switch msg.String() {
	case "down", "j":
		model.logVp.LineDown(1)
	case "up", "k":
		model.logVp.LineUp(1)
	case "pgdown", "ctrl+f", " ":
		model.logVp.HalfViewDown()
	case "pgup", "ctrl+b":
		model.logVp.HalfViewUp()
	case "home", "g":
		model.logVp.GotoTop()
	case "end", "G":
		model.logVp.GotoBottom()
	}
}

func (model *Model) updateOverlay(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch model.overlay.kind {

	case ovHelp:
		model.closeOverlay()
		return model, nil

	case ovLog:
		switch msg.String() {
		case "esc", "l", "q":
			model.closeOverlay()
		default:
			model.handleRawViewportKeys(msg)
		}
		return model, nil

	case ovConfig:
		switch msg.String() {
		case "esc", "v", "q":
			model.closeOverlay()
		default:
			model.handleViewportKeys(msg)
		}
		return model, nil

	case ovMakeConf:
		switch msg.String() {
		case "esc", "q":
			model.closeOverlay()
		default:
			model.handleViewportKeys(msg)
		}
		return model, nil

	case ovPackages:
		switch msg.String() {
		case "esc", "q":
			model.closeOverlay()
		default:
			model.handleViewportKeys(msg)
		}
		return model, nil

	case ovPicker:
		switch msg.String() {
		case "esc":
			model.closeOverlay()
			return model, nil
		case "enter":
			opts := model.filteredOpts()
			if model.overlay.multiChoice {
				var vals []string
				for _, opt := range model.overlay.opts { // preserve original order
					if len(opt.groupDirs) > 0 {
						continue // category rows never contribute a value themselves
					}
					if model.overlay.selected[opt.Value] {
						vals = append(vals, opt.Value)
					}
				}
				fn := model.multiFn
				model.closeOverlay()
				if fn != nil {
					fn(model, vals)
				}
				return model, nil
			}
			if len(opts) > 0 && model.overlay.cursor < len(opts) {
				val := opts[model.overlay.cursor].Value
				fn := model.pickFn
				model.closeOverlay()
				if fn != nil {
					fn(model, val)
				}
			}
			return model, nil
		case " ", "space":
			// For multi-choice, Space toggles the focused row regardless of
			// filter focus (so users can search then toggle). Package/repo
			// names don't need a literal space in the filter.
			if model.overlay.multiChoice {
				opts := model.filteredOpts()
				if model.overlay.cursor < len(opts) {
					if model.overlay.selected == nil {
						model.overlay.selected = map[string]bool{}
					}
					opt := opts[model.overlay.cursor]
					if len(opt.groupDirs) > 0 {
						// Category row: select all members if any are missing,
						// otherwise clear them all.
						allSelected := true
						for _, dir := range opt.groupDirs {
							if !model.overlay.selected[dir] {
								allSelected = false
								break
							}
						}
						for _, dir := range opt.groupDirs {
							model.overlay.selected[dir] = !allSelected
						}
					} else {
						model.overlay.selected[opt.Value] = !model.overlay.selected[opt.Value]
					}
				}
				return model, nil
			}
			routed, cmd := model.updatePickerKeys(msg)
			if routed && cmd != nil {
				return model, cmd
			}
			return model, nil
		default:
			routed, cmd := model.updatePickerKeys(msg)
			if !routed {
				return model, nil
			}
			return model, cmd
		}

	case ovText:
		isArea := model.textFn != nil && model.textFn.multi
		switch msg.String() {
		case "esc":
			model.closeOverlay()
			return model, nil
		case "enter":
			if isArea {
				break // newline goes to the editor below
			}
			val := strings.TrimSpace(model.overlay.input.Value())
			st := model.textFn
			model.closeOverlay()
			if st != nil && st.fn != nil {
				st.fn(model, val)
			}
			cmd := model.deferredCmd
			model.deferredCmd = nil
			return model, cmd
		case "ctrl+d":
			if isArea {
				val := strings.TrimSpace(model.overlay.area.Value())
				st := model.textFn
				model.closeOverlay()
				if st != nil && st.fn != nil {
					st.fn(model, val)
				}
				cmd := model.deferredCmd
				model.deferredCmd = nil
				return model, cmd
			}
		}
		var cmd tea.Cmd
		if isArea {
			model.overlay.area, cmd = model.overlay.area.Update(msg)
		} else {
			model.overlay.input, cmd = model.overlay.input.Update(msg)
		}
		return model, cmd

	case ovButtons:
		switch msg.String() {
		case "ctrl+c":
			model.quitNow()
			return model, tea.Quit
		case "esc":
			model.closeOverlay()
		case "left", "h":
			if model.overlay.btnCur > 0 {
				model.overlay.btnCur--
			}
		case "right", "l":
			if model.overlay.btnCur < len(model.overlay.buttons)-1 {
				model.overlay.btnCur++
			}
		case "enter":
			buttonIdx := model.overlay.btnCur
			fn := model.overlay.onBtn
			model.overlay.kind = ovNone
			if fn != nil {
				fn(model, buttonIdx)
			}
		}
		return model, nil
	}
	return model, nil
}

func (model *Model) closeOverlay() {
	model.pickFn = nil
	model.textFn = nil
	model.multiFn = nil
	model.overlay.kind = ovNone
}

func (model *Model) filteredOpts() []option {
	filter := strings.ToLower(strings.TrimSpace(model.overlay.filter))
	if filter == "" {
		return model.overlay.opts
	}
	var out []option
	for _, opt := range model.overlay.opts {
		if strings.Contains(strings.ToLower(opt.Value), filter) ||
			strings.Contains(strings.ToLower(opt.Desc), filter) {
			out = append(out, opt)
		}
	}
	return out
}

const pickerVisibleRows = 14

func (model *Model) renderOverlay() string {
	width := maxInt(40, minInt(90, model.width-6))
	switch model.overlay.kind {
	case ovHelp:
		body := wrapText(model.overlay.body, minInt(78, maxInt(40, model.width-12)))
		content := titleStyle.Render("❓ "+model.overlay.title) + "\n\n" + body +
			"\n\n" + helpStyle.Render("Press any key to return")
		return modalBoxStyle.MaxWidth(width).Render(content)

	case ovLog:
		height := maxInt(6, model.height-8)
		boxWidth := maxInt(40, minInt(110, model.width-10))
		model.syncRawViewport(boxWidth-2, height-2)
		box := overlayBoxStyle.Width(boxWidth).Height(height).Render(
			titleStyle.Render(eScroll+" Raw output") + "\n\n" + model.rawVp.View())
		hint := "↑↓ scroll · End resumes tail · l/Esc close"
		if !model.rawFollow {
			hint = "▲ scrolled — End resumes tail · l/Esc close"
		}
		return lipgloss.JoinVertical(lipgloss.Center, box, helpStyle.Render(hint))

	case ovConfig:
		height := maxInt(6, model.height-8)
		boxWidth := maxInt(40, minInt(90, model.width-12))
		if model.logVp.Width != boxWidth-2 || model.logVp.Height != height-4 {
			model.logVp = viewport.New(boxWidth-2, height-4)
		}
		model.logVp.SetContent(highlightTOML(model.cfg.String()))
		box := overlayBoxStyle.Width(boxWidth).Height(height).Render(
			titleStyle.Render(eInfo+" "+filepath.Base(model.cfgPath)) + "\n\n" + model.logVp.View() + "\n" +
				helpStyle.Render("↑↓ scroll · v/Esc close"))
		return lipgloss.JoinVertical(lipgloss.Center, box)

	case ovMakeConf:
		height := maxInt(6, model.height-8)
		boxWidth := maxInt(40, minInt(90, model.width-12))
		if model.logVp.Width != boxWidth-2 || model.logVp.Height != height-4 {
			model.logVp = viewport.New(boxWidth-2, height-4)
		}
		jobs := runtime.NumCPU()
		if jobs < 2 {
			jobs = 2
		}
		model.logVp.SetContent(makeConfViewContent(model.cfg, jobs))
		box := overlayBoxStyle.Width(boxWidth).Height(height).Render(
			titleStyle.Render(ePencil+" make.conf") + "\n\n" + model.logVp.View() + "\n" +
				helpStyle.Render("↑↓ scroll · q/Esc close"))
		return lipgloss.JoinVertical(lipgloss.Center, box)

	case ovPackages:
		height := maxInt(6, model.height-8)
		boxWidth := maxInt(40, minInt(90, model.width-12))
		if model.logVp.Width != boxWidth-2 || model.logVp.Height != height-4 {
			model.logVp = viewport.New(boxWidth-2, height-4)
		}
		var builder strings.Builder
		pkgs := model.cfg.ProfilePackages()
		if len(pkgs) == 0 {
			builder.WriteString(unsetStyle.Render("(no packages are defined for the selected profile)\n"))
		} else {
			for _, pkgName := range pkgs {
				builder.WriteString(profilePkgStyle.Render("  • "+pkgName) + "\n")
			}
		}
		model.logVp.SetContent(builder.String())
		box := overlayBoxStyle.Width(boxWidth).Height(height).Render(
			titleStyle.Render(model.overlay.title) + "\n\n" + model.logVp.View() + "\n" +
				helpStyle.Render("↑↓ scroll · q/Esc close"))
		return lipgloss.JoinVertical(lipgloss.Center, box)

	case ovPicker:
		opts := model.filteredOpts()
		cur := model.overlay.cursor
		start := 0
		if cur >= pickerVisibleRows {
			start = cur - pickerVisibleRows + 1
		}
		end := start + pickerVisibleRows
		if end > len(opts) {
			end = len(opts)
			start = maxInt(0, end-pickerVisibleRows)
		}
		titleLine := titleStyle.Render(model.overlay.title)
		counter := unsetStyle.Render(strconv.Itoa(min(cur+1, maxInt(1, len(opts)))) + "/" + strconv.Itoa(len(opts)))
		gap := maxInt(1, width-lipgloss.Width(titleLine)-lipgloss.Width(counter)-4)
		head := titleLine + strings.Repeat(" ", gap) + counter

		var builder strings.Builder
		builder.WriteString(head + "\n")
		if model.overlay.filter != "" || model.overlay.input.Focused() {
			builder.WriteString(model.overlay.input.View() + "\n")
		}
		if start > 0 {
			builder.WriteString(unsetStyle.Render("  ▲ more") + "\n")
		}
		trunc := lipgloss.NewStyle().MaxWidth(width - 4)
		for idx := start; idx < end; idx++ {
			opt := opts[idx]
			marker := "  "
			if opt.indent {
				marker += "  "
			}
			style := lipgloss.NewStyle()
			if idx == cur {
				style = selectedRowStyle
				if !model.overlay.multiChoice {
					marker = rowCursorStyle.Render("▌ ")
				}
			}
			if model.overlay.multiChoice {
				if len(opt.groupDirs) > 0 {
					count := 0
					for _, dir := range opt.groupDirs {
						if model.overlay.selected[dir] {
							count++
						}
					}
					switch {
					case count == len(opt.groupDirs):
						marker += "[" + okStyle.Render("✓") + "] "
					case count > 0:
						marker += "[" + unsetStyle.Render("-") + "] "
					default:
						marker += "[ ] "
					}
				} else if model.overlay.selected[opt.Value] {
					marker += "[" + okStyle.Render("✓") + "] "
				} else {
					marker += "[ ] "
				}
			}
			line := marker + opt.Value
			if opt.primaryDesc {
				// Prefer the friendly description as the primary label.
				primary := opt.Desc
				if primary == "" {
					primary = opt.Value
				}
				line = marker + primary
				if primary != opt.Value {
					line += "  " + unsetStyle.Render(opt.Value)
				}
			}
			if !model.overlay.multiChoice && opt.Value == model.overlay.current {
				line += " " + okStyle.Render("✓")
			}
			if opt.Desc != "" && !opt.primaryDesc {
				line += "  " + unsetStyle.Render(opt.Desc)
			}
			builder.WriteString(trunc.Render(style.Render(line)) + "\n")
		}
		if end < len(opts) {
			builder.WriteString(unsetStyle.Render("  ▼ more") + "\n")
		}
		if len(opts) == 0 {
			builder.WriteString(unsetStyle.Render("no matches") + "\n")
		}
		hint := "↑↓ navigate · Enter select · Esc cancel"
		if model.overlay.multiChoice {
			nSel := 0
			for _, val := range model.overlay.selected {
				if val {
					nSel++
				}
			}
			hint = fmt.Sprintf("↑↓ navigate · Space toggle (%d selected) · Enter apply · Esc cancel", nSel)
		}
		if model.overlay.input.Focused() {
			hint += " · type to filter"
		}
		builder.WriteString(helpStyle.Render(hint))
		return overlayBoxStyle.MaxWidth(width).Render(builder.String())

	case ovText:
		var fieldView string
		if model.textFn != nil && model.textFn.multi {
			fieldView = model.overlay.area.View()
		} else {
			fieldView = model.overlay.input.View()
		}
		content := titleStyle.Render(ePencil+" "+stripLeadingEmoji(model.overlay.title)) + "\n"
		if model.overlay.note != "" {
			content += warnStyle.Render(model.overlay.note) + "\n"
		}
		hint := "Enter confirm · Esc cancel"
		if model.textFn != nil && model.textFn.multi {
			hint = "one entry per line · Ctrl+D confirm · Esc cancel"
		}
		content += "\n" + fieldView + "\n\n" + helpStyle.Render(hint)
		return modalBoxStyle.MaxWidth(width).Render(content)

	case ovButtons:
		var builder strings.Builder
		builder.WriteString(titleStyle.Render(eWarn+" "+stripLeadingEmoji(model.overlay.title)) + "\n\n")
		builder.WriteString(wrapText(model.overlay.body, minInt(64, width-8)) + "\n\n")
		var pills []string
		for btnIdx, btn := range model.overlay.buttons {
			style := pillInactiveStyle
			if btnIdx == model.overlay.btnCur {
				style = pillActiveStyle
			}
			pills = append(pills, style.Render(btn))
		}
		builder.WriteString(lipgloss.JoinHorizontal(lipgloss.Center, pills...))
		builder.WriteString("\n" + helpStyle.Render("←→ choose · Enter confirm · Esc cancel"))
		return modalBoxStyle.MaxWidth(width).Render(builder.String())
	}
	return ""
}

// stripLeadingEmoji removes an emoji prefix added at call sites so titles
// are not decorated twice.
func stripLeadingEmoji(emoji string) string {
	for _, prefix := range []string{eWarn + " ", eInfo + " ", ePencil + " ", eScroll + " "} {
		if rest, ok := strings.CutPrefix(emoji, prefix); ok {
			return rest
		}
	}
	return emoji
}

// wrapText wraps s at width runes per line, preserving blank lines.
func wrapText(text string, width int) string {
	width = maxInt(10, width)
	var out []string
	for _, para := range strings.Split(text, "\n") {
		if para == "" {
			out = append(out, "")
			continue
		}
		line := ""
		lineLen := 0
		for _, word := range strings.Fields(para) {
			wordLen := utf8.RuneCountInString(word)
			if line == "" {
				line = word
				lineLen = wordLen
			} else if lineLen+1+wordLen <= width {
				line += " " + word
				lineLen += 1 + wordLen
			} else {
				out = append(out, line)
				line = word
				lineLen = wordLen
			}
		}
		if line != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}
