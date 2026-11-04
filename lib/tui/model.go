package tui

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"gentooinstall/lib/config"
	"gentooinstall/lib/live"
	"gentooinstall/lib/sysinfo"
)

const (
	minWidth  = 80
	minHeight = 24
)

const minMainWidth = 40

type tabDef struct {
	name   string
	fields []*field
	render func(*Model) string
}

type Model struct {
	cfg      *config.Config
	cfgPath  string
	dirty    bool
	hasEFI   bool
	rootOK   bool
	prereqFn func() Prereq

	tabs      []tabDef
	active    int
	cursors   []int
	scrollTop []int

	width, height int
	status        string
	statusKind    int
	savedFlash    bool

	winsizeStable int

	mirrorState int
	mirrorNote  string
	mirrorHost  string

	overlay     overlay
	quitting    bool
	deferredCmd tea.Cmd

	installing   bool
	instState    int
	instLines    []string
	instRawLines []string
	instSteps    []instStep
	instDemo     bool
	spinner      spinner.Model
	spinOn       bool
	prog         progress.Model
	logVp        viewport.Model
	rawVp        viewport.Model
	rawFollow    bool
	rawDirty     bool
	rawContent   []string
	curStep      string
	curCmd       string
	fail         *InstallFailedMsg
	btnCur       int
	instFn       InstallFunc
	usedEnc      bool
	pickFn       func(*Model, string)
	textFn       *textState
	multiFn      func(*Model, []string)
}

type overlay struct {
	kind  int
	title string
	body  string

	opts    []option
	cursor  int
	filter  string
	current string

	input textinput.Model
	area  textarea.Model

	multiChoice bool
	selected    map[string]bool

	buttons []string
	btnCur  int
	onBtn   func(*Model, int)

	note string
}

const (
	mirrorUnknown = iota
	mirrorChecking
	mirrorOK
	mirrorDown
)

const mirrorPollInterval = 10 * time.Second

const winsizePollInterval = 2 * time.Second

const winsizeStableTicks = 4

const (
	ovNone = iota
	ovHelp
	ovPicker
	ovText
	ovButtons
	ovLog
	ovConfig
	ovPackages
	ovMakeConf
)

const (
	stOK = iota + 1
	stErr
)

func (model *Model) setStatusErr(statusText string) {
	model.status, model.statusKind = statusText, stErr
}

type savedClearMsg struct{}

func clearSavedAfter(duration time.Duration) tea.Cmd {
	return tea.Tick(duration, func(time.Time) tea.Msg { return savedClearMsg{} })
}

type MirrorProbeMsg struct {
	OK   bool
	Note string
}

func (model *Model) mirrorProbeCmd() tea.Cmd {
	mirror := model.cfg.Gentoo.Mirror
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		st := sysinfo.MirrorProbe(ctx, mirror)
		return MirrorProbeMsg{OK: st.OK, Note: st.Note}
	}
}

func (model *Model) mirrorTickCmd() tea.Cmd {
	return tea.Tick(mirrorPollInterval, func(time.Time) tea.Msg { return mirrorTickMsg{} })
}

type mirrorTickMsg struct{}

func (model *Model) probeMirror() tea.Cmd {
	if model.mirrorState != mirrorOK {
		model.mirrorState = mirrorChecking
	}
	return tea.Batch(model.mirrorProbeCmd(), model.mirrorTickCmd())
}

func New(cfg *config.Config, cfgPath string) *Model {
	model := &Model{cfg: cfg, cfgPath: cfgPath, hasEFI: sysinfo.HasEFI(),
		rootOK: true, mirrorState: mirrorUnknown, mirrorHost: mirrorHostName(cfg.Gentoo.Mirror),
		rawFollow: true, rawDirty: true}
	model.tabs = buildTabs(model)
	for range model.tabs {
		model.cursors = append(model.cursors, 0)
		model.scrollTop = append(model.scrollTop, 0)
	}
	model.clampCursor(model.active)
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	model.spinner = sp
	return model
}

func (model *Model) SetHasEFI(has bool) { model.hasEFI = has }

func (model *Model) SetRoot(ok bool) { model.rootOK = ok }

func mirrorHostName(mirror string) string {
	if host := sysinfo.MirrorHost(mirror); host != "" {
		return host
	}
	return "mirror"
}

func (model *Model) markDirty() { model.dirty = true }

func (model *Model) Init() tea.Cmd {
	return tea.Batch(model.probeMirror(), winsizeTickCmd())
}

type WinsizeTickMsg struct{}

func winsizeTickCmd() tea.Cmd {
	return tea.Tick(winsizePollInterval, func(time.Time) tea.Msg { return WinsizeTickMsg{} })
}

func (model *Model) syncWinsizeFromKernel() bool {
	cols, rows, ok := live.GetWinsize()
	if !ok || (cols == model.width && rows == model.height) {
		return false
	}
	model.width, model.height = cols, rows
	return true
}

func (model *Model) visibleRows(tab int) []int {
	var out []int
	for index, field := range model.tabs[tab].fields {
		if visible(field, model.cfg) {
			out = append(out, index)
		}
	}
	return out
}

func (model *Model) clampCursor(tab int) {
	rows := model.visibleRows(tab)
	if len(rows) == 0 {
		return
	}
	cursor := model.cursors[tab]
	if cursor > len(rows)-1 {
		cursor = len(rows) - 1
	}
	for cursor > 0 && model.tabs[tab].fields[rows[cursor]].kind == kSeparator {
		cursor--
	}
	for cursor < len(rows)-1 && model.tabs[tab].fields[rows[cursor]].kind == kSeparator {
		cursor++
	}
	if model.cursors[tab] != cursor {
		model.cursors[tab] = cursor
	}
}

func (model *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if model.quitting {
		return model, tea.Quit
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		model.width, model.height = msg.Width, msg.Height
		model.winsizeStable = 0

	case WinsizeTickMsg:
		if model.syncWinsizeFromKernel() {
			model.winsizeStable = 0
		} else {
			model.winsizeStable++
		}
		if model.winsizeStable < winsizeStableTicks {
			return model, winsizeTickCmd()
		}
		return model, nil

	case spinner.TickMsg:
		if model.instState == instRunning {
			var cmd tea.Cmd
			model.spinner, cmd = model.spinner.Update(msg)
			model.spinOn = true
			return model, tea.Batch(cmd, model.spinner.Tick)
		}
		model.spinOn = false
		return model, nil

	case InstallLineMsg:
		model.updateInstallMsg(msg)
	case InstallStartMsg:
		model.beginInstall()
		return model, model.startSpinner()
	case InstallFailedMsg:
		model.updateInstallMsg(msg)
	case InstallDoneMsg:
		model.updateInstallMsg(msg)

	case savedClearMsg:
		model.savedFlash = false

	case MirrorProbeMsg:
		if msg.OK {
			model.mirrorState = mirrorOK
			model.mirrorNote = ""
		} else {
			model.mirrorState = mirrorDown
			model.mirrorNote = msg.Note
			if model.mirrorNote == "" {
				model.mirrorNote = "unreachable"
			}
			if !live.NetworkReady() {
				model.mirrorNote = "no network — DHCP still configuring"
			}
		}
		model.mirrorHost = mirrorHostName(model.cfg.Gentoo.Mirror)
		return model, nil

	case mirrorTickMsg:
		if model.mirrorState != mirrorOK {
			model.mirrorState = mirrorChecking
		}
		return model, tea.Batch(model.mirrorProbeCmd(), model.mirrorTickCmd())

	case tea.KeyMsg:
		if !model.rootOK {
			switch msg.String() {
			case "q", "ctrl+c":
				model.quitNow()
				return model, tea.Quit
			}
			return model, nil
		}
		if model.overlay.kind != ovNone {
			return model.updateOverlay(msg)
		}
		if model.installing {
			return model.updateInstallKeys(msg)
		}
		return model.updateGlobal(msg)
	}

	if cmd := model.startSpinner(); cmd != nil {
		return model, cmd
	}
	return model, nil
}

func (model *Model) startSpinner() tea.Cmd {
	if model.instState == instRunning && !model.spinOn {
		model.spinOn = true
		return model.spinner.Tick
	}
	return nil
}

func (model *Model) updateGlobal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		model.quitNow()
		return model, tea.Quit
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		tabIndex := int(msg.Runes[0] - '1')
		if tabIndex < len(model.tabs) {
			model.active = tabIndex
			model.clampCursor(model.active)
		}
		return model, nil
	case "tab", "shift+tab", "right", "left", "h", "l":
		dir := 1
		if msg.String() == "shift+tab" || msg.String() == "left" || msg.String() == "h" {
			dir = -1
		}
		model.active = (model.active + dir + len(model.tabs)) % len(model.tabs)
		model.clampCursor(model.active)
		return model, nil
	case "j", "down":
		rows := model.visibleRows(model.active)
		for len(rows) > 0 && model.cursors[model.active] < len(rows)-1 {
			model.cursors[model.active]++
			if model.tabs[model.active].fields[rows[model.cursors[model.active]]].kind != kSeparator {
				break
			}
		}
		return model, nil
	case "k", "up":
		rows := model.visibleRows(model.active)
		for model.cursors[model.active] > 0 {
			model.cursors[model.active]--
			if model.tabs[model.active].fields[rows[model.cursors[model.active]]].kind != kSeparator {
				break
			}
		}
		return model, nil
	case "enter", " ", "space":
		return model.activateRow()
	case "?":
		return model.showRowHelp()
	case "i":
		if model.tabs[model.active].name == "Install" {
			if model.instState != instIdle {
				model.installing = true
				return model, nil
			}
			return model.confirmInstall()
		}
	case "d":
		if model.tabs[model.active].name == "Install" && model.instState == instIdle {
			return model.startDemo()
		}
	case "v":
		model.openConfigView()
		return model, nil
	case "s":
		return model.save()
	case "S":
		return model.saveAs()
	case "q", "esc":
		return model.confirmQuit()
	}
	return model, nil
}

func (model *Model) currentField() *field {
	rows := model.visibleRows(model.active)
	if len(rows) == 0 {
		return nil
	}
	idx := rows[min(model.cursors[model.active], len(rows)-1)]
	return model.tabs[model.active].fields[idx]
}

func (model *Model) activateRow() (tea.Model, tea.Cmd) {
	field := model.currentField()
	if field == nil || (model.tabs[model.active].render != nil && field.label == "") {
		return model, nil
	}
	switch field.kind {
	case kToggle:
		field.setBool(model.cfg, !field.getBool(model.cfg))
		if field.label == "Different initramfs keymap" && field.getBool(model.cfg) &&
			strings.TrimSpace(model.cfg.System.KeymapInitramfs) == "" {
			model.cfg.System.KeymapInitramfs = model.cfg.System.Keymap
		}
		model.markDirty()
		model.status = ""
	case kText, kMultiText:
		onDone := func(mm *Model, value string) {
			field.setText(mm.cfg, value)
			mm.dirty = true
			mm.status = ""
			if field.watchMirror {
				mm.mirrorState = mirrorChecking
				mm.mirrorHost = ""
				mm.deferredCmd = tea.Batch(mm.mirrorProbeCmd(), mm.mirrorTickCmd())
			}
		}
		model.openText("Edit "+field.label, field.getText(model.cfg), field.multi, onDone)
	case kChoice:
		if field.onPick != nil {
			field.onPick(model, field, "")
			return model, nil
		}
		model.openPicker(field.label, field.options(model.cfg), field.getChoice(model.cfg), field.filter,
			func(mm *Model, value string) {
				field.setChoice(mm.cfg, value)
				mm.dirty = true
				mm.status = ""
			})
	case kMultiChoice:
		current := field.getStrings(model.cfg)
		if field.preSeed != nil && len(current) == 0 {
			current = field.preSeed(model.cfg)
		}
		model.openMultiPicker(field.label, field.options(model.cfg), current,
			func(mm *Model, vals []string) {
				field.setStrings(mm.cfg, vals)
				mm.dirty = true
				mm.status = ""
			})
	case kReadOnly:
		if field.onPick != nil {
			field.onPick(model, field, "")
			return model, nil
		}
		model.openHelp(field.label, field.help)
	case kSeparator:
	}
	model.clampCursor(model.active)
	return model, nil
}

func (model *Model) showRowHelp() (tea.Model, tea.Cmd) {
	if model.tabs[model.active].render != nil {
		model.openHelp(model.tabs[model.active].name, overviewHelp)
		return model, nil
	}
	field := model.currentField()
	if field == nil {
		return model, nil
	}
	model.openHelp(field.label, field.help)
	return model, nil
}

func (model *Model) save() (tea.Model, tea.Cmd) {
	path := config.ResolveSavePath(model.cfgPath)
	if model.cfgPath != path {
		model.cfgPath = path
	}
	if err := model.cfg.Save(model.cfgPath); err != nil {
		model.setStatusErr("save failed: " + err.Error())
		return model, nil
	}
	model.dirty = false
	model.status = ""
	model.savedFlash = true
	return model, clearSavedAfter(5 * time.Second)
}

func (model *Model) saveAs() (tea.Model, tea.Cmd) {
	model.openText("Save configuration as", config.ResolveSavePath(model.cfgPath), false, func(mm *Model, value string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		mm.cfgPath = value
		if err := mm.cfg.Save(mm.cfgPath); err != nil {
			mm.setStatusErr("save failed: " + err.Error())
		} else {
			mm.dirty = false
			mm.status = ""
			mm.savedFlash = true
			mm.deferredCmd = clearSavedAfter(5 * time.Second)
		}
	})
	return model, nil
}

func (model *Model) quitNow() {
	model.quitting = true
}

func (model *Model) confirmQuit() (tea.Model, tea.Cmd) {
	if model.instState == instRunning || model.instState == instWaiting {
		model.overlay = overlay{
			kind:  ovButtons,
			title: eWarn + " Installation in progress",
			body: "An installation is currently running. Quitting will NOT stop it — " +
				"background processes may keep modifying the target disks.",
			buttons: []string{"Stay", "Quit anyway"},
			btnCur:  0,
			onBtn: func(mm *Model, buttonIndex int) {
				if buttonIndex == 1 {
					mm.quitNow()
				}
			},
		}
		return model, nil
	}
	if !model.dirty {
		model.quitNow()
		return model, tea.Quit
	}
	model.overlay = overlay{
		kind:    ovButtons,
		title:   eWarn + " Unsaved changes",
		body:    "Do you want to save your configuration before quitting?",
		buttons: []string{eSave + " Save", "🗑 Discard", "Back"},
		btnCur:  0,
		onBtn: func(mm *Model, buttonIndex int) {
			switch buttonIndex {
			case 0:
				path := config.ResolveSavePath(mm.cfgPath)
				if mm.cfgPath != path {
					mm.cfgPath = path
				}
				if err := mm.cfg.Save(mm.cfgPath); err != nil {
					mm.setStatusErr("save failed: " + err.Error())
					return
				}
				mm.quitNow()
			case 1:
				mm.quitNow()
			default:
				mm.overlay.kind = ovNone
			}
		},
	}
	return model, nil
}

func (model *Model) View() string {
	if model.width == 0 {
		model.width, model.height = 80, 24
	}

	if model.width < minWidth || model.height < minHeight {
		return model.renderTooSmall()
	}

	if !model.rootOK {
		return model.renderRootRequired()
	}

	if model.installing {
		out := model.renderInstallView()
		if model.overlay.kind != ovNone {
			box := model.renderOverlay()
			out = lipgloss.Place(model.width, model.height,
				lipgloss.Center, lipgloss.Center, box)
		}
		return out
	}

	inner := maxInt(1, model.width-8)
	tabs := model.renderTabBarMax(inner)
	cw := model.contentWidth()
	mirror, path := model.mirrorLine(), model.pathLine()
	var pathBox string
	if lipgloss.Width(mirror)+1+lipgloss.Width(path) <= cw {
		pathBox = lipgloss.JoinHorizontal(lipgloss.Top, mirror, " ", path)
	} else {
		pathBox = lipgloss.JoinVertical(lipgloss.Left, mirror, path)
	}

	pathLine := pathBox
	if model.status != "" {
		st := helpStyle
		switch model.statusKind {
		case stOK:
			st = okStyle
		case stErr:
			st = errorStyle
		}
		statusText := st.Render(truncateRunes(model.status, maxInt(12, model.width/4)))
		joined := lipgloss.JoinHorizontal(lipgloss.Top, pathBox, "  ", statusText)
		if lipgloss.Width(joined) <= cw {
			pathLine = joined
		} else {
			pathLine = lipgloss.JoinVertical(lipgloss.Left, pathBox, statusText)
		}
	}

	var body strings.Builder
	if model.tabs[model.active].render != nil {
		body.WriteString(model.tabs[model.active].render(model))
	} else {
		body.WriteString(model.renderFields())
	}
	rawBody := body.String()

	header := lipgloss.JoinVertical(lipgloss.Top, tabs, pathLine)
	reserved := lipgloss.Height(header) + 1 + 2
	maxLines := model.height - reserved
	lines := strings.Split(rawBody, "\n")
	if maxLines > 0 && len(lines) > maxLines {
		rawBody = strings.Join(lines[:maxLines], "\n")
	}

	main := pageStyle.Render(rawBody)

	right := lipgloss.JoinVertical(lipgloss.Top, header, "", main)

	var out string
	if left, ok := model.sidebar(); ok {
		totalHeight := maxInt(model.height,
			maxInt(lipgloss.Height(left), lipgloss.Height(right)))
		padTo := func(str string, targetLines int) string {
			lines := strings.Split(str, "\n")
			for len(lines) < targetLines {
				lines = append(lines, "")
			}
			return strings.Join(lines, "\n")
		}
		left = padTo(left, totalHeight)
		right = padTo(right, totalHeight)
		divider := helpStyle.Render(strings.TrimSuffix(strings.Repeat("│\n", totalHeight), "\n"))
		cols := lipgloss.JoinHorizontal(lipgloss.Top,
			lipgloss.NewStyle().PaddingRight(1).Render(left),
			divider,
			lipgloss.NewStyle().PaddingLeft(1).Render(right))
		out = model.frameWindow(cols)
	} else {
		out = model.frameWindow(right)
	}

	if model.overlay.kind != ovNone {
		box := model.renderOverlay()
		out = lipgloss.Place(model.width, model.height,
			lipgloss.Center, lipgloss.Center, box)
	}
	return out
}

func (model *Model) sidebar() (string, bool) {
	inner := maxInt(1, model.width-8)
	left := lipgloss.JoinVertical(lipgloss.Top, renderLogo(), "", model.renderHints())
	if lipgloss.Width(left)+3+minMainWidth > inner {
		return "", false
	}
	return left, true
}

func (model *Model) contentWidth() int {
	inner := maxInt(1, model.width-8)
	if left, ok := model.sidebar(); ok {
		return maxInt(1, inner-lipgloss.Width(left)-3)
	}
	return inner
}

func (model *Model) frameWindow(content string) string {
	iw, ih := maxInt(1, model.width-4), maxInt(1, model.height-2)
	inner := maxInt(1, model.width-8)
	lines := strings.Split(content, "\n")
	if len(lines) > ih {
		lines = lines[:ih]
	}
	for index, line := range lines {
		if lineWidth := lipgloss.Width(line); lineWidth > inner {
			lines[index] = truncateToWidth(line, inner)
		}
	}
	content = strings.Join(lines, "\n")
	content = lipgloss.Place(iw, ih, lipgloss.Left, lipgloss.Top, content)
	return windowStyle.Width(iw).Height(ih).Render(content)
}

func (model *Model) renderTooSmall() string {
	msg := tooSmallStyle.Render("Terminal too small — resize to at least " +
		fmt.Sprintf("%dx%d", minWidth, minHeight))
	return lipgloss.Place(model.width, model.height, lipgloss.Center, lipgloss.Center, msg)
}

func (model *Model) renderRootRequired() string {
	msg := tooSmallStyle.Render("gentooinstall must be run as root to perform an installation.\n\n" +
		"Re-run it with sudo, or boot the live ISO (it runs as root).\n\n" +
		"Press q to quit.")
	return lipgloss.Place(model.width, model.height, lipgloss.Center, lipgloss.Center, msg)
}

func truncateToWidth(str string, width int) string {
	var out strings.Builder
	used := 0
	data := []byte(str)
	for pos := 0; pos < len(data); {
		if data[pos] == '\x1b' {
			if pos+1 < len(data) && data[pos+1] == '[' {
				end := pos + 2
				for end < len(data) && !(data[end] >= 0x40 && data[end] <= 0x7e) {
					end++
				}
				if end >= len(data) {
					return out.String()
				}
				out.Write(data[pos : end+1])
				pos = end + 1
				continue
			}
			if pos+1 < len(data) && data[pos+1] == ']' {
				end := pos + 2
				for end < len(data) && data[end] != 0x07 && !(data[end] == '\x1b' && end+1 < len(data) && data[end+1] == '\\') {
					end++
				}
				if end >= len(data) {
					return out.String()
				}
				if data[end] == 0x07 {
					end++
				} else {
					end += 2
				}
				out.Write(data[pos:end])
				pos = end
				continue
			}
			pos++
			continue
		}
		runeVal, size := utf8.DecodeRune(data[pos:])
		if runeVal == utf8.RuneError && size < 2 {
			pos++
			continue
		}
		rw := runewidth.RuneWidth(runeVal)
		if rw < 1 {
			rw = 1
		}
		if used+rw > width {
			break
		}
		out.Write(data[pos : pos+size])
		used += rw
		pos += size
	}
	return out.String()
}

func (model *Model) renderTabBar() string {
	return model.renderTabBarMax(maxInt(1, model.width-8))
}

func (model *Model) tabStripParts(compact bool) []string {
	parts := make([]string, len(model.tabs))
	for index, tab := range model.tabs {
		label := tabEmoji(tab.name) + " " + tab.name
		active, inactive := tabActiveBorderStyle, tabInactiveBorderStyle
		if compact {
			label = fmt.Sprintf("%d %s", index+1, tab.name)
			active, inactive = active.Padding(0, 0), inactive.Padding(0, 0)
		}
		if index == model.active {
			parts[index] = active.Render(label)
		} else {
			parts[index] = inactive.Render(label)
		}
	}
	return parts
}

func (model *Model) renderTabBarMax(maxW int) string {
	parts := model.tabStripParts(false)
	if stripWidth(parts) <= maxW {
		return lipgloss.JoinHorizontal(lipgloss.Top, parts...)
	}
	parts = model.tabStripParts(true)
	if stripWidth(parts) <= maxW {
		return lipgloss.JoinHorizontal(lipgloss.Top, parts...)
	}
	widths := make([]int, len(parts))
	for index := range parts {
		widths[index] = lipgloss.Width(parts[index])
	}
	ellipsisL := unsetStyle.Render("…")
	ellipsisR := unsetStyle.Render("…")
	ew := lipgloss.Width(ellipsisL) + lipgloss.Width(ellipsisR)
	start, end := model.active, model.active+1
	used := widths[model.active]
	for {
		grew := false
		if start > 0 && used+widths[start-1]+ew <= maxW {
			start--
			used += widths[start]
			grew = true
		}
		if end < len(parts) && used+widths[end]+ew <= maxW {
			used += widths[end]
			end++
			grew = true
		}
		if !grew {
			break
		}
	}
	out := lipgloss.JoinHorizontal(lipgloss.Top, parts[start:end]...)
	if start > 0 {
		out = lipgloss.JoinHorizontal(lipgloss.Top, ellipsisL, out)
	}
	if end < len(parts) {
		out = lipgloss.JoinHorizontal(lipgloss.Top, out, ellipsisR)
	}
	lines := strings.Split(out, "\n")
	for index, line := range lines {
		if lipgloss.Width(line) > maxW {
			lines[index] = truncateToWidth(line, maxW)
		}
	}
	return strings.Join(lines, "\n")
}

func stripWidth(parts []string) int {
	total := 0
	for _, part := range parts {
		total += lipgloss.Width(part)
	}
	return total
}

func (model *Model) bodyWidth() int {
	return maxInt(40, minInt(90, model.contentWidth()))
}

func (model *Model) labelWidth() int {
	maxLabel := 0
	for _, idx := range model.visibleRows(model.active) {
		field := model.tabs[model.active].fields[idx]
		if field.kind == kSeparator {
			continue
		}
		if lw := lipgloss.Width(field.label); lw > maxLabel {
			maxLabel = lw
		}
	}
	return minInt(maxInt(maxLabel, 22)+2, maxInt(24, model.bodyWidth()/2))
}

func (model *Model) mirrorLine() string {
	host := model.mirrorHost
	if host == "" {
		host = mirrorHostName(model.cfg.Gentoo.Mirror)
	}
	host = truncateRunes(host, 28)
	switch model.mirrorState {
	case mirrorChecking, mirrorUnknown:
		return mirrorBoxWarnStyle.Render(host + " ...")
	case mirrorOK:
		return mirrorBoxValidStyle.Render(host)
	default:
		note := model.mirrorNote
		if note == "" {
			note = "unreachable"
		}
		return mirrorBoxInvalidStyle.Render(host + " " + errorStyle.Render("✗") + " " + truncateRunes(note, 24))
	}
}

func (model *Model) pathLine() string {
	mark := ""
	box := cfgBoxValidStyle
	errs := model.cfg.Validate()
	switch {
	case model.statusKind == stErr, len(errs) > 0:
		box = cfgBoxInvalidStyle
		mark = errorStyle.Render("✗")
	case model.dirty:
		box = cfgBoxWarnStyle
	case len(model.cfg.Advisories()) > 0:
		box = cfgBoxWarnStyle
		mark = warnStyle.Render("⚠")
	case model.savedFlash:
		mark = okStyle.Render("✓")
	}
	path := model.cfgPath
	if model.dirty {
		path += " " + dirtyStyle.Render("⚠")
	}
	text := path
	if mark != "" {
		text += " " + mark
	}
	return box.Render(text)
}

func (model *Model) renderFields() string {
	rows := model.visibleRows(model.active)
	cur := model.cursors[model.active]

	const viewportPad = 4
	maxRows := model.height - 11
	if maxRows < 5 {
		maxRows = 5
	}
	start := 0
	if len(rows) > maxRows {
		if cur >= len(rows)-maxRows {
			start = cur - maxRows + 1
		} else if cur > viewportPad {
			start = cur - viewportPad
		}
		if start < 0 {
			start = 0
		}
	}
	end := start + maxRows
	if end > len(rows) {
		end = len(rows)
		start = end - maxRows
		if start < 0 {
			start = 0
		}
	}

	labelW := model.labelWidth()
	bodyW := model.bodyWidth()
	trunc := lipgloss.NewStyle().MaxWidth(bodyW)

	var body strings.Builder
	first := true
	for ri, rowIdx := range rows {
		if ri < start || ri >= end {
			continue
		}
		field := model.tabs[model.active].fields[rowIdx]
		if field.kind == kSeparator {
			if !first {
				body.WriteString("\n")
			}
			body.WriteString(sectionRule(field.label, bodyW) + "\n")
			first = false
			continue
		}
		first = false
		marker := "  "
		style := lipgloss.NewStyle()
		if ri == cur {
			marker = rowCursorStyle.Render("▌ ")
			style = selectedRowStyle
		}
		label := field.label
		if pad := labelW - lipgloss.Width(label); pad > 0 {
			label += strings.Repeat(" ", pad)
		} else if pad < 0 {
			label = truncateRunes(label, -pad-1) + "…"
		}
		line := marker + style.Render(label) + " " + summaryOf(field, model.cfg)
		body.WriteString(trunc.Render(line) + "\n")
	}
	return body.String()
}

func sectionRule(title string, width int) string {
	styledTitle := titleStyle.Render(title)
	used := lipgloss.Width(styledTitle) + 1
	rule := ""
	if remaining := width - used; remaining > 2 {
		rule = treeGlyphStyle.Render(" " + strings.Repeat("─", remaining))
	}
	return styledTitle + rule
}

func truncateRunes(text string, max int) string {
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	return string(runes[:max])
}

var logoArt = []string{
	"         ____		     ",
	"        /   /\\      ",
	"       /   /  \\  ___    ",
	"      /   /   / /   /\\  ",
	"     /   /   / /   /  \\  ",
	"    /   /   / /   /    \\  ",
	"   /   /   / /   /      \\    ",
	"  /   /   / /   /   /\\   \\    ",
	" /___/   / /___/   /  \\   \\",
	" \\   \\   \\ \\   \\  /   /   /",
	"  \\   \\   \\ \\___\\/   /   /",
	"   \\   \\   \\    /   /   /  ",
	"    \\   \\   \\  /   /   /   ",
	"     \\   \\   \\/   /   /	 ",
	"      \\   \\   \\  /   /	    ",
	"       \\   \\   \\/   /	    ",
	"        \\   \\      /	    ",
	"         \\   \\    /	      ",
	"          \\   \\  /	      ",
	"           \\___\\/   ",
	"                        ",
}

func renderLogo() string {
	lines := make([]string, len(logoArt))
	for index, line := range logoArt {
		lines[index] = logoStyle.Render(line)
	}
	return strings.Join(lines, "\n")
}

func (model *Model) renderHints() string {
	onInstall := model.tabs[model.active].name == "Install"
	hints := [][2]string{
		{"↑/k ↓/j", "move"},
		{"Enter", "edit"},
		{"?", "help"},
		{"s", "save"},
		{"S", "save as"},
		{"1-" + fmt.Sprint(len(model.tabs)), "tabs"},
	}
	if onInstall {
		hints = append(hints, [2]string{"i", "install"}, [2]string{"d", "demo"},
			[2]string{"v", "config"})
	}
	hints = append(hints, [2]string{"q", "quit"})

	keyW := 0
	for _, hint := range hints {
		if hintWidth := lipgloss.Width("[" + hint[0] + "]"); hintWidth > keyW {
			keyW = hintWidth
		}
	}
	var body strings.Builder
	for index, hint := range hints {
		if index > 0 {
			body.WriteString("\n")
		}
		key := helpStyle.Render("[") + hintKeyStyle.Render(hint[0]) + helpStyle.Render("]")
		pad := strings.Repeat(" ", keyW-lipgloss.Width("["+hint[0]+"]"))
		label := hint[1]
		switch hint[0] {
		case "install":
			label = "install 🚀"
		case "demo":
			label = "demo " + eFlask
		case "quit":
			label = "quit " + eDoor
		case "save":
			label = "save " + eSave
		}
		body.WriteString(key + pad + " " + helpStyle.Render(label))
	}
	return body.String()
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
