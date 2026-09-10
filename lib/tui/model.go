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

// Minimum terminal size for the TUI; below it only the resize notice is
// rendered (the layout wraps and breaks when the window is too small).
// Serial consoles (qemu -nographic) are commonly 80x24, so the narrow
// layout below must stay usable down to that size.
const (
	minWidth  = 80
	minHeight = 24
)

// minMainWidth is the smallest main-pane width worth showing beside the
// logo/hint sidebar. Below it the sidebar is hidden and the main pane
// takes the full window width (e.g. 80-column serial terminals).
const minMainWidth = 40

// tabDef is one numbered tab.
type tabDef struct {
	name   string
	fields []*field
	render func(*Model) string // custom static content (overview)
}

// Model is the root bubbletea model.
type Model struct {
	cfg     *config.Config
	cfgPath string
	dirty   bool
	hasEFI  bool
	// rootOK gates the whole UI: an unprivileged user gets the centered
	// root-required page instead of the configurator (the demo bypasses it).
	rootOK   bool
	prereqFn func() Prereq

	tabs      []tabDef
	active    int
	cursors   []int
	scrollTop []int

	width, height int
	status        string
	statusKind    int // stOK, stErr
	savedFlash    bool

	// winsizeStable counts consecutive size polls without change; the
	// poll stops after winsizeStableTicks to avoid repaint churn.
	winsizeStable int

	// Mirror reachability indicator (bordered box left of the config path).
	mirrorState int    // mirrorUnknown, mirrorChecking, mirrorOK, mirrorDown
	mirrorNote  string // short diagnostic shown when down
	mirrorHost  string // cached scheme://host of the selected mirror

	// overlay is any modal element stacked above the tab UI.
	overlay     overlay
	quitting    bool
	deferredCmd tea.Cmd // cmd to return after overlay callbacks

	// Install view state (see installview.go).
	installing   bool
	instState    int
	instLines    []string // stripped lines for step parsing
	instRawLines []string // raw lines with ANSI codes for log overlay
	instSteps    []instStep
	instDemo     bool
	spinner      spinner.Model
	spinOn       bool
	prog         progress.Model
	logVp        viewport.Model
	// rawVp is the dedicated viewport for the raw output window (ovLog).
	// It is separate from logVp (used by config/make.conf/packages
	// overlays) so resizing one overlay never resets another, and so
	// SetContent can be cached instead of rebuilt on every View().
	rawVp      viewport.Model
	rawFollow  bool     // tail-follow: jump to bottom on new output
	rawDirty   bool     // cached rawContent needs a SetContent
	rawContent []string // colorized lines parallel to instRawLines
	curStep    string
	curCmd     string
	fail       *InstallFailedMsg
	btnCur     int
	instFn     InstallFunc
	usedEnc    bool
	// Overlay callbacks (moved off package-level maps).
	pickFn  func(*Model, string)
	textFn  *textState
	multiFn func(*Model, []string)
}

// overlay is any modal element stacked above the tab UI.
type overlay struct {
	kind  int // ovNone..
	title string
	body  string

	opts    []option
	cursor  int
	filter  string
	current string // single-choice pickers: currently selected value

	input textinput.Model
	area  textarea.Model

	multiChoice bool
	selected    map[string]bool

	buttons []string
	btnCur  int
	onBtn   func(*Model, int) // index into buttons

	note string // optional hint line for text overlays
}

// Mirror indicator states.
const (
	mirrorUnknown = iota
	mirrorChecking
	mirrorOK
	mirrorDown
)

// mirrorPollInterval is how often the indicator re-probes the selected mirror
// so it recovers automatically once the live ISO's background DHCP comes up.
const mirrorPollInterval = 10 * time.Second

// winsizePollInterval is how often the TUI re-reads the kernel winsize.
// QEMU -serial stdio never forwards host resizes, so this only picks up
// sizes that appear late (or are re-applied); it stops after a few stable
// reads to avoid repaint churn.
const winsizePollInterval = 2 * time.Second

// winsizeStableTicks bounds the size poll: stop ticking after this many
// consecutive reads without change (or without a readable size).
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

// MirrorProbeMsg carries the result of an asynchronous mirror reachability
// probe back into the model.
type MirrorProbeMsg struct {
	OK   bool
	Note string
}

// mirrorProbeCmd probes the currently selected Gentoo mirror in the
// background and returns the result as a MirrorProbeMsg.
func (model *Model) mirrorProbeCmd() tea.Cmd {
	mirror := model.cfg.Gentoo.Mirror
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		st := sysinfo.MirrorProbe(ctx, mirror)
		return MirrorProbeMsg{OK: st.OK, Note: st.Note}
	}
}

// mirrorTickCmd schedules the next periodic mirror probe so the indicator
// recovers once the live ISO network (DHCP) comes up.
func (model *Model) mirrorTickCmd() tea.Cmd {
	return tea.Tick(mirrorPollInterval, func(time.Time) tea.Msg { return mirrorTickMsg{} })
}

type mirrorTickMsg struct{}

// probeMirror marks the mirror as "checking" (unless it is already ok) and
// kicks off a probe plus the periodic re-check.
func (model *Model) probeMirror() tea.Cmd {
	if model.mirrorState != mirrorOK {
		model.mirrorState = mirrorChecking
	}
	return tea.Batch(model.mirrorProbeCmd(), model.mirrorTickCmd())
}

// New builds the configurator model.
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

// SetHasEFI overrides the host-firmware probe for the install pre-flight
// checks. Production leaves it at the sysinfo.HasEFI result probed in New;
// tests use it to simulate a BIOS-booted live system.
func (model *Model) SetHasEFI(has bool) { model.hasEFI = has }

// SetRoot sets whether the process runs with root privileges. A false value
// replaces the whole UI with the root-required page (see renderRootRequired)
// so an unprivileged user cannot walk into a doomed installation. The demo
// recording keeps the gate off via main, and tests default to root.
func (model *Model) SetRoot(ok bool) { model.rootOK = ok }

// mirrorHostName returns the host portion of the mirror URL for display,
// falling back to "mirror" when the URL cannot be parsed.
func mirrorHostName(mirror string) string {
	if host := sysinfo.MirrorHost(mirror); host != "" {
		return host
	}
	return "mirror"
}

func (model *Model) markDirty() { model.dirty = true }

// Init implements tea.Model.
func (model *Model) Init() tea.Cmd {
	return tea.Batch(model.probeMirror(), winsizeTickCmd())
}

type WinsizeTickMsg struct{}

// winsizeTickCmd re-reads the kernel winsize; the guest serial port
// reports 0x0 until detection publishes the host size, which can land
// after the first frames.
func winsizeTickCmd() tea.Cmd {
	return tea.Tick(winsizePollInterval, func(time.Time) tea.Msg { return WinsizeTickMsg{} })
}

// syncWinsizeFromKernel applies the kernel-known size when it is valid
// and differs; it reports whether the model changed.
func (model *Model) syncWinsizeFromKernel() bool {
	cols, rows, ok := live.GetWinsize()
	if !ok || (cols == model.width && rows == model.height) {
		return false
	}
	model.width, model.height = cols, rows
	return true
}

// visibleRows returns indexes of visible fields for a tab.
func (model *Model) visibleRows(tab int) []int {
	var out []int
	for index, field := range model.tabs[tab].fields {
		if visible(field, model.cfg) {
			out = append(out, index)
		}
	}
	return out
}

// clampCursor keeps the active tab's cursor on a selectable (non-separator)
// row so section titles are never focused.
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

// Update implements tea.Model.
func (model *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Check quit first so that every message type honours it, including
	// key messages that would otherwise return early from the switch.
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
		// The ticker is strictly self-renewing (one tick arms exactly one
		// successor tick and one probe), so periodic re-probes run at a
		// steady cadence — including while the mirror is unreachable — and
		// the indicator recovers automatically once DHCP comes up.
		if model.mirrorState != mirrorOK {
			model.mirrorState = mirrorChecking
		}
		return model, tea.Batch(model.mirrorProbeCmd(), model.mirrorTickCmd())

	case tea.KeyMsg:
		if !model.rootOK {
			// Root-required page: only quitting makes sense.
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

	// Keep the spinner alive while an install is running.
	if cmd := model.startSpinner(); cmd != nil {
		return model, cmd
	}
	return model, nil
}

// startSpinner schedules the first spinner tick once per run; subsequent
// ticks reschedule themselves until the install stops running.
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
				model.installing = true // return to a paused/finished run
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
			field.onPick(model, field, "") // custom pickers manage themselves
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
		// read-only rows cannot be edited; a custom handler (e.g. opening a
		// picker or modal) wins, otherwise show the field's help.
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

// quitNow flags the model as quitting; the next Update call returns tea.Quit.
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

// View implements tea.Model.
func (model *Model) View() string {
	if model.width == 0 {
		// Unknown size (e.g. serial ttyS0 with no winsize yet): assume a
		// conservative 80x24 so first frames fit instead of emitting a
		// 100-column layout clipped by the host terminal.
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

	// One layout, live or not: every element is budgeted off measured
	// chrome (frame + optional sidebar), never off width brackets.
	// The tab strip keeps the full frame budget (it centers the header);
	// fields and rules use the pane budget below.
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

	// Render the status message to the right of the path box when it
	// fits, otherwise on its own row.
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

	// Header budget: tab bar, bordered path line, blank separator, and the
	// two rows consumed by the window frame.
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
		// Static divider height: the full terminal (or more if content
		// overflows), so it does not change size when switching tabs.
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
		// Single column: sidebar hidden, main pane spans the window.
		out = model.frameWindow(right)
	}

	if model.overlay.kind != ovNone {
		box := model.renderOverlay()
		out = lipgloss.Place(model.width, model.height,
			lipgloss.Center, lipgloss.Center, box)
	}
	return out
}

// sidebar returns the logo/hint column when it fits alongside a usable
// main pane, and reports whether it does. The layout measures chrome
// instead of branching on width brackets, so live serial and normal
// binary renders share one path.
func (model *Model) sidebar() (string, bool) {
	inner := maxInt(1, model.width-8) // frameWindow budget: border + padding
	left := lipgloss.JoinVertical(lipgloss.Top, renderLogo(), "", model.renderHints())
	// Sidebar + divider + side paddings must leave minMainWidth for content.
	if lipgloss.Width(left)+3+minMainWidth > inner {
		return "", false
	}
	return left, true
}

// contentWidth is the usable width of the main pane: the frame budget
// minus the sidebar when shown.
func (model *Model) contentWidth() int {
	inner := maxInt(1, model.width-8)
	if left, ok := model.sidebar(); ok {
		return maxInt(1, inner-lipgloss.Width(left)-3)
	}
	return inner
}

// frameWindow draws the outer window border around content, sizing the
// inner area to the terminal minus the border (2 rows) and side padding
// (2 columns). Content lines wider than the inner width are clipped rather
// than wrapped, so lipgloss never folds a row onto the next.
func (model *Model) frameWindow(content string) string {
	iw, ih := maxInt(1, model.width-4), maxInt(1, model.height-2)
	inner := maxInt(1, model.width-8) // border (2 cols) + window padding (2 cols)
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

// renderTooSmall shows a centered notice instead of the UI when the
// terminal is below the minimum size (see minWidth/minHeight), mirroring
// the behavior of btop.
func (model *Model) renderTooSmall() string {
	msg := tooSmallStyle.Render("Terminal too small — resize to at least " +
		fmt.Sprintf("%dx%d", minWidth, minHeight))
	return lipgloss.Place(model.width, model.height, lipgloss.Center, lipgloss.Center, msg)
}

// renderRootRequired shows a centered notice instead of the UI when the
// process runs without root privileges (see SetRoot), using the same
// presentation as renderTooSmall.
func (model *Model) renderRootRequired() string {
	msg := tooSmallStyle.Render("gentooinstall must be run as root to perform an installation.\n\n" +
		"Re-run it with sudo, or boot the live ISO (it runs as root).\n\n" +
		"Press q to quit.")
	return lipgloss.Place(model.width, model.height, lipgloss.Center, lipgloss.Center, msg)
}

// truncateToWidth trims s to w visible columns, splitting within a line.
// ANSI SGR escape sequences are copied through verbatim and never counted
// against the width budget, so styled text is truncated only at glyph
// boundaries (escaping like this keeps the surrounding box borders intact).
func truncateToWidth(str string, width int) string {
	var out strings.Builder
	used := 0
	data := []byte(str)
	for pos := 0; pos < len(data); {
		if data[pos] == '\x1b' {
			// CSI (model, K, J, H, …): consume params + final byte verbatim.
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
			// OSC: skip until BEL or ST, carrying it across verbatim.
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
			pos++ // lone ESC: consume the byte that follows
			continue
		}
		runeVal, size := utf8.DecodeRune(data[pos:])
		if runeVal == utf8.RuneError && size < 2 {
			pos++ // invalid byte: skip so the loop always advances
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

// tabStripParts renders one tab box per tab. Compact drops the emoji and
// box padding so all six tabs fit narrow windows (numbered labels keep
// the 1-6 keyboard mapping visible).
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

// renderTabBarMax renders the tab strip constrained to maxW visible columns.
// The full strip is returned when it fits, else the compact strip (all six
// tabs, no emoji), else a window around the active tab with ellipsis
// markers so the active tab is never the part clipped off.
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
	// Expand outward from the active tab while the window fits.
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
	// Per-line safety: the strip is multi-row (borders), so a whole-string
	// cut would slice rows apart. The window above already fits by
	// construction; this only guards rounding.
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

// bodyWidth is the usable width of the main pane (contentWidth, capped);
// used for rules and value truncation.
func (model *Model) bodyWidth() int {
	return maxInt(40, minInt(90, model.contentWidth()))
}

// labelWidth computes the padded label column width for the active tab's
// visible rows so no label ever gets truncated.
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

// mirrorLine renders the mirror reachability indicator as a bordered box
// shown to the left of the config file path. It shows the mirror host with a
// red ✗ and short diagnostic when down (the live ISO shows "no network" while
// DHCP is still coming up), and "..." while a probe is in flight.
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
	default: // mirrorDown
		note := model.mirrorNote
		if note == "" {
			note = "unreachable"
		}
		return mirrorBoxInvalidStyle.Render(host + " " + errorStyle.Render("✗") + " " + truncateRunes(note, 24))
	}
}

// pathLine renders the config file path inside a bordered box whose color
// reflects the current state: red on validation errors or an error status
// message, yellow while the configuration is unsaved, amber with
// advisories, green otherwise. The saved ✓ only appears briefly after saving.
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

	// Scroll window. Leaves room for masthead, path line and status.
	const viewportPad = 4
	maxRows := model.height - 11
	if maxRows < 5 {
		maxRows = 5
	}
	start := 0
	if len(rows) > maxRows {
		// Keep cursor in view: show it at the bottom of the viewport
		// when near the bottom, at the top when near the top.
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

// sectionRule renders a titled section separator: "── Title ─────".
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

// logoArt is a Gentoo gull ASCII art.
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

// renderLogo returns the colored logo block shown left of the tab row.
func renderLogo() string {
	lines := make([]string, len(logoArt))
	for index, line := range logoArt {
		lines[index] = logoStyle.Render(line)
	}
	return strings.Join(lines, "\n")
}

// renderHints builds the left-hand vertical hint column in pVPN style:
// bracketed bold keys with dim action labels and the color-coded status
// message at the end.
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
