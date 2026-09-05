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

	"gentooinstall/internal/config"
	"gentooinstall/internal/live"
	"gentooinstall/internal/sysinfo"
)

// Minimum terminal size for the TUI; below it only the resize notice is
// rendered (the layout wraps and breaks when the window is too small).
const (
	minWidth  = 100
	minHeight = 30
)

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

	tabs      []tabDef
	active    int
	cursors   []int
	scrollTop []int

	width, height int
	status        string
	statusKind    int // stOK, stErr
	savedFlash    bool

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
	curStep      string
	curCmd       string
	fail         *InstallFailedMsg
	btnCur       int
	instFn       InstallFunc
	usedEnc      bool
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

func (m *Model) setStatusErr(s string) { m.status, m.statusKind = s, stErr }

type savedClearMsg struct{}

func clearSavedAfter(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return savedClearMsg{} })
}

// MirrorProbeMsg carries the result of an asynchronous mirror reachability
// probe back into the model.
type MirrorProbeMsg struct {
	OK   bool
	Note string
}

// mirrorProbeCmd probes the currently selected Gentoo mirror in the
// background and returns the result as a MirrorProbeMsg.
func (m *Model) mirrorProbeCmd() tea.Cmd {
	mirror := m.cfg.Gentoo.Mirror
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		st := sysinfo.MirrorProbe(ctx, mirror)
		return MirrorProbeMsg{OK: st.OK, Note: st.Note}
	}
}

// mirrorTickCmd schedules the next periodic mirror probe so the indicator
// recovers once the live ISO network (DHCP) comes up.
func (m *Model) mirrorTickCmd() tea.Cmd {
	return tea.Tick(mirrorPollInterval, func(time.Time) tea.Msg { return mirrorTickMsg{} })
}

type mirrorTickMsg struct{}

// probeMirror marks the mirror as "checking" (unless it is already ok) and
// kicks off a probe plus the periodic re-check.
func (m *Model) probeMirror() tea.Cmd {
	if m.mirrorState != mirrorOK {
		m.mirrorState = mirrorChecking
	}
	return tea.Batch(m.mirrorProbeCmd(), m.mirrorTickCmd())
}

// New builds the configurator model.
func New(cfg *config.Config, cfgPath string) *Model {
	m := &Model{cfg: cfg, cfgPath: cfgPath, hasEFI: sysinfo.HasEFI(),
		mirrorState: mirrorUnknown, mirrorHost: mirrorHostName(cfg.Gentoo.Mirror)}
	m.tabs = buildTabs(m)
	for range m.tabs {
		m.cursors = append(m.cursors, 0)
		m.scrollTop = append(m.scrollTop, 0)
	}
	m.clampCursor(m.active)
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	m.spinner = sp
	return m
}

// mirrorHostName returns the host portion of the mirror URL for display,
// falling back to "mirror" when the URL cannot be parsed.
func mirrorHostName(mirror string) string {
	if h := sysinfo.MirrorHost(mirror); h != "" {
		return h
	}
	return "mirror"
}

func (m *Model) markDirty() { m.dirty = true }

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd {
	return m.probeMirror()
}

// visibleRows returns indexes of visible fields for a tab.
func (m *Model) visibleRows(tab int) []int {
	var out []int
	for i, f := range m.tabs[tab].fields {
		if visible(f, m.cfg) {
			out = append(out, i)
		}
	}
	return out
}

// clampCursor keeps the active tab's cursor on a selectable (non-separator)
// row so section titles are never focused.
func (m *Model) clampCursor(tab int) {
	rows := m.visibleRows(tab)
	if len(rows) == 0 {
		return
	}
	c := m.cursors[tab]
	if c > len(rows)-1 {
		c = len(rows) - 1
	}
	for c > 0 && m.tabs[tab].fields[rows[c]].kind == kSeparator {
		c--
	}
	if m.cursors[tab] != c {
		m.cursors[tab] = c
	}
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Check quit first so that every message type honours it, including
	// key messages that would otherwise return early from the switch.
	if m.quitting {
		return m, tea.Quit
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height

	case spinner.TickMsg:
		if m.instState == instRunning {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			m.spinOn = true
			return m, tea.Batch(cmd, m.spinner.Tick)
		}
		m.spinOn = false
		return m, nil

	case InstallLineMsg:
		m.updateInstallMsg(msg)
	case InstallStartMsg:
		m.beginInstall()
		return m, m.startSpinner()
	case InstallFailedMsg:
		m.updateInstallMsg(msg)
	case InstallDoneMsg:
		m.updateInstallMsg(msg)
	case installShellDoneMsg:
		m.updateInstallMsg(msg)

	case savedClearMsg:
		m.savedFlash = false

	case MirrorProbeMsg:
		if msg.OK {
			m.mirrorState = mirrorOK
			m.mirrorNote = ""
		} else {
			m.mirrorState = mirrorDown
			m.mirrorNote = msg.Note
			if m.mirrorNote == "" {
				m.mirrorNote = "unreachable"
			}
			if !live.NetworkReady() {
				m.mirrorNote = "no network — DHCP still configuring"
			}
		}
		m.mirrorHost = mirrorHostName(m.cfg.Gentoo.Mirror)
		return m, nil

	case mirrorTickMsg:
		// The ticker is strictly self-renewing (one tick arms exactly one
		// successor tick and one probe), so periodic re-probes run at a
		// steady cadence — including while the mirror is unreachable — and
		// the indicator recovers automatically once DHCP comes up.
		if m.mirrorState != mirrorOK {
			m.mirrorState = mirrorChecking
		}
		return m, tea.Batch(m.mirrorProbeCmd(), m.mirrorTickCmd())

	case tea.KeyMsg:
		if m.overlay.kind != ovNone {
			return m.updateOverlay(msg)
		}
		if m.installing {
			return m.updateInstallKeys(msg)
		}
		return m.updateGlobal(msg)
	}

	// Keep the spinner alive while an install is running.
	if cmd := m.startSpinner(); cmd != nil {
		return m, cmd
	}
	return m, nil
}

// startSpinner schedules the first spinner tick once per run; subsequent
// ticks reschedule themselves until the install stops running.
func (m *Model) startSpinner() tea.Cmd {
	if m.instState == instRunning && !m.spinOn {
		m.spinOn = true
		return m.spinner.Tick
	}
	return nil
}

func (m *Model) updateGlobal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.quitNow()
		return m, tea.Quit
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		n := int(msg.Runes[0] - '1')
		if n < len(m.tabs) {
			m.active = n
		}
		return m, nil
	case "tab", "shift+tab", "right", "left", "h", "l":
		d := 1
		if msg.String() == "shift+tab" || msg.String() == "left" || msg.String() == "h" {
			d = -1
		}
		m.active = (m.active + d + len(m.tabs)) % len(m.tabs)
		return m, nil
	case "j", "down":
		rows := m.visibleRows(m.active)
		for len(rows) > 0 && m.cursors[m.active] < len(rows)-1 {
			m.cursors[m.active]++
			if m.tabs[m.active].fields[rows[m.cursors[m.active]]].kind != kSeparator {
				break
			}
		}
		return m, nil
	case "k", "up":
		rows := m.visibleRows(m.active)
		for m.cursors[m.active] > 0 {
			m.cursors[m.active]--
			if m.tabs[m.active].fields[rows[m.cursors[m.active]]].kind != kSeparator {
				break
			}
		}
		return m, nil
	case "enter", " ", "space":
		return m.activateRow()
	case "?":
		return m.showRowHelp()
	case "i":
		if m.tabs[m.active].name == "Install" {
			if m.instState != instIdle {
				m.installing = true // return to a paused/finished run
				return m, nil
			}
			return m.confirmInstall()
		}
	case "d":
		if m.tabs[m.active].name == "Install" && m.instState == instIdle {
			return m.startDemo()
		}
	case "v":
		m.openConfigView()
		return m, nil
	case "s":
		return m.save()
	case "S":
		return m.saveAs()
	case "q", "esc":
		return m.confirmQuit()
	}
	return m, nil
}

func (m *Model) currentField() *field {
	rows := m.visibleRows(m.active)
	if len(rows) == 0 {
		return nil
	}
	idx := rows[min(m.cursors[m.active], len(rows)-1)]
	return m.tabs[m.active].fields[idx]
}

func (m *Model) activateRow() (tea.Model, tea.Cmd) {
	f := m.currentField()
	if f == nil || (m.tabs[m.active].render != nil && f.label == "") {
		return m, nil
	}
	switch f.kind {
	case kToggle:
		f.setBool(m.cfg, !f.getBool(m.cfg))
		if f.label == "Different initramfs keymap" && f.getBool(m.cfg) &&
			strings.TrimSpace(m.cfg.System.KeymapInitramfs) == "" {
			m.cfg.System.KeymapInitramfs = m.cfg.System.Keymap
		}
		m.markDirty()
		m.status = ""
	case kText, kMultiText:
		onDone := func(mm *Model, v string) {
			f.setText(mm.cfg, v)
			mm.dirty = true
			mm.status = ""
			if f.watchMirror {
				mm.mirrorState = mirrorChecking
				mm.mirrorHost = ""
				mm.deferredCmd = tea.Batch(mm.mirrorProbeCmd(), mm.mirrorTickCmd())
			}
		}
		m.openText("Edit "+f.label, f.getText(m.cfg), f.multi, onDone)
	case kChoice:
		if f.onPick != nil {
			f.onPick(m, f, "") // custom pickers manage themselves
			return m, nil
		}
		m.openPicker(f.label, f.options(m.cfg), f.getChoice(m.cfg), f.filter,
			func(mm *Model, v string) {
				f.setChoice(mm.cfg, v)
				mm.dirty = true
				mm.status = ""
			})
	case kMultiChoice:
		m.openMultiPicker(f.label, f.options(m.cfg), f.getStrings(m.cfg),
			func(mm *Model, vals []string) {
				f.setStrings(mm.cfg, vals)
				mm.dirty = true
				mm.status = ""
			})
	case kReadOnly:
		// read-only rows cannot be edited; a custom handler (e.g. opening a
		// picker or modal) wins, otherwise show the field's help.
		if f.onPick != nil {
			f.onPick(m, f, "")
			return m, nil
		}
		m.openHelp(f.label, f.help)
	case kSeparator:
	}
	m.clampCursor(m.active)
	return m, nil
}

func (m *Model) showRowHelp() (tea.Model, tea.Cmd) {
	if m.tabs[m.active].render != nil {
		m.openHelp(m.tabs[m.active].name, overviewHelp)
		return m, nil
	}
	f := m.currentField()
	if f == nil {
		return m, nil
	}
	m.openHelp(f.label, f.help)
	return m, nil
}

func (m *Model) save() (tea.Model, tea.Cmd) {
	path := config.ResolveSavePath(m.cfgPath)
	if m.cfgPath != path {
		m.cfgPath = path
	}
	if err := m.cfg.Save(m.cfgPath); err != nil {
		m.setStatusErr("save failed: " + err.Error())
		return m, nil
	}
	m.dirty = false
	m.status = ""
	m.savedFlash = true
	return m, clearSavedAfter(5 * time.Second)
}

func (m *Model) saveAs() (tea.Model, tea.Cmd) {
	m.openText("Save configuration as", config.ResolveSavePath(m.cfgPath), false, func(mm *Model, v string) {
		if strings.TrimSpace(v) == "" {
			return
		}
		mm.cfgPath = v
		if err := mm.cfg.Save(mm.cfgPath); err != nil {
			mm.setStatusErr("save failed: " + err.Error())
		} else {
			mm.dirty = false
			mm.status = ""
			mm.savedFlash = true
			mm.deferredCmd = clearSavedAfter(5 * time.Second)
		}
	})
	return m, nil
}

// quitNow flags the model as quitting; the next Update call returns tea.Quit.
func (m *Model) quitNow() {
	m.quitting = true
}

func (m *Model) confirmQuit() (tea.Model, tea.Cmd) {
	if m.instState == instRunning || m.instState == instWaiting {
		m.overlay = overlay{
			kind:  ovButtons,
			title: eWarn + " Installation in progress",
			body: "An installation is currently running. Quitting will NOT stop it — " +
				"background processes may keep modifying the target disks.",
			buttons: []string{"Stay", "Quit anyway"},
			btnCur:  0,
			onBtn: func(mm *Model, i int) {
				if i == 1 {
					mm.quitNow()
				}
			},
		}
		return m, nil
	}
	if !m.dirty {
		m.quitNow()
		return m, tea.Quit
	}
	m.overlay = overlay{
		kind:    ovButtons,
		title:   eWarn + " Unsaved changes",
		body:    "Do you want to save your configuration before quitting?",
		buttons: []string{eSave + " Save", "🗑 Discard", "Back"},
		btnCur:  0,
		onBtn: func(mm *Model, i int) {
			switch i {
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
	return m, nil
}

// View implements tea.Model.
func (m *Model) View() string {
	if m.width == 0 {
		m.width, m.height = 100, 30
	}

	if m.width < minWidth || m.height < minHeight {
		return m.renderTooSmall()
	}

	if m.installing {
		out := m.renderInstallView()
		if m.overlay.kind != ovNone {
			box := m.renderOverlay()
			out = lipgloss.Place(m.width, m.height,
				lipgloss.Center, lipgloss.Center, box)
		}
		return out
	}

	logo := renderLogo()
	tabs := m.renderTabBar()
	pathBox := lipgloss.JoinHorizontal(lipgloss.Top, m.mirrorLine(), " ", m.pathLine())
	left := lipgloss.JoinVertical(lipgloss.Top, logo, "", m.renderHints())

	// Render the status message to the right of the path box.
	pathLine := pathBox
	if m.status != "" {
		st := helpStyle
		switch m.statusKind {
		case stOK:
			st = okStyle
		case stErr:
			st = errorStyle
		}
		statusText := st.Render(truncateRunes(m.status, maxInt(12, m.width/4)))
		pathLine = lipgloss.JoinHorizontal(lipgloss.Top, pathBox, "  ", statusText)
	}

	var b strings.Builder
	if m.tabs[m.active].render != nil {
		b.WriteString(m.tabs[m.active].render(m))
	} else {
		b.WriteString(m.renderFields())
	}
	rawBody := b.String()

	// Header budget: tab bar, bordered path line, blank separator, and the
	// two rows consumed by the window frame.
	header := lipgloss.JoinVertical(lipgloss.Top, tabs, pathLine)
	reserved := lipgloss.Height(header) + 1 + 2
	maxLines := m.height - reserved
	lines := strings.Split(rawBody, "\n")
	if maxLines > 0 && len(lines) > maxLines {
		rawBody = strings.Join(lines[:maxLines], "\n")
	}

	main := pageStyle.Render(rawBody)

	right := lipgloss.JoinVertical(lipgloss.Top, header, "", main)

	// Static divider height: the full terminal (or more if content
	// overflows), so it does not change size when switching tabs.
	H := maxInt(m.height,
		maxInt(lipgloss.Height(left), lipgloss.Height(right)))
	padTo := func(s string, n int) string {
		lines := strings.Split(s, "\n")
		for len(lines) < n {
			lines = append(lines, "")
		}
		return strings.Join(lines, "\n")
	}
	left = padTo(left, H)
	right = padTo(right, H)
	divider := helpStyle.Render(strings.TrimSuffix(strings.Repeat("│\n", H), "\n"))
	cols := lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.NewStyle().PaddingRight(1).Render(left),
		divider,
		lipgloss.NewStyle().PaddingLeft(1).Render(right))

	out := m.frameWindow(cols)

	if m.overlay.kind != ovNone {
		box := m.renderOverlay()
		out = lipgloss.Place(m.width, m.height,
			lipgloss.Center, lipgloss.Center, box)
	}
	return out
}

// frameWindow draws the outer window border around content, sizing the
// inner area to the terminal minus the border (2 rows) and side padding
// (2 columns). Content lines wider than the inner width are clipped rather
// than wrapped, so lipgloss never folds a row onto the next.
func (m *Model) frameWindow(content string) string {
	iw, ih := maxInt(1, m.width-4), maxInt(1, m.height-2)
	inner := maxInt(1, m.width-8) // border (2 cols) + window padding (2 cols)
	lines := strings.Split(content, "\n")
	if len(lines) > ih {
		lines = lines[:ih]
	}
	for i, ln := range lines {
		if w := lipgloss.Width(ln); w > inner {
			lines[i] = truncateToWidth(ln, inner)
		}
	}
	content = strings.Join(lines, "\n")
	content = lipgloss.Place(iw, ih, lipgloss.Left, lipgloss.Top, content)
	return windowStyle.Width(iw).Height(ih).Render(content)
}

// renderTooSmall shows a centered notice instead of the UI when the
// terminal is below the minimum size (see minWidth/minHeight), mirroring
// the behavior of btop.
func (m *Model) renderTooSmall() string {
	msg := tooSmallStyle.Render("Terminal too small — resize to at least " +
		fmt.Sprintf("%dx%d", minWidth, minHeight))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, msg)
}

// truncateToWidth trims s to w visible columns, splitting within a line.
// ANSI SGR escape sequences are copied through verbatim and never counted
// against the width budget, so styled text is truncated only at glyph
// boundaries (escaping like this keeps the surrounding box borders intact).
func truncateToWidth(s string, w int) string {
	var b strings.Builder
	n := 0
	d := []byte(s)
	for i := 0; i < len(d); {
		if d[i] == '\x1b' {
			// CSI (m, K, J, H, …): consume params + final byte verbatim.
			if i+1 < len(d) && d[i+1] == '[' {
				j := i + 2
				for j < len(d) && !(d[j] >= 0x40 && d[j] <= 0x7e) {
					j++
				}
				if j >= len(d) {
					return b.String()
				}
				b.Write(d[i : j+1])
				i = j + 1
				continue
			}
			// OSC: skip until BEL or ST, carrying it across verbatim.
			if i+1 < len(d) && d[i+1] == ']' {
				j := i + 2
				for j < len(d) && d[j] != 0x07 && !(d[j] == '\x1b' && j+1 < len(d) && d[j+1] == '\\') {
					j++
				}
				if j >= len(d) {
					return b.String()
				}
				if d[j] == 0x07 {
					j++
				} else {
					j += 2
				}
				b.Write(d[i:j])
				i = j
				continue
			}
			i++ // lone ESC: consume the byte that follows
			continue
		}
		r, size := utf8.DecodeRune(d[i:])
		if r == utf8.RuneError && size < 2 {
			i++ // invalid byte: skip so the loop always advances
			continue
		}
		rw := runewidth.RuneWidth(r)
		if rw < 1 {
			rw = 1
		}
		if n+rw > w {
			break
		}
		b.Write(d[i : i+size])
		n += rw
		i += size
	}
	return b.String()
}

func (m *Model) renderTabBar() string {
	var parts []string
	for i, t := range m.tabs {
		label := tabEmoji(t.name) + " " + t.name
		if i == m.active {
			parts = append(parts, tabActiveBorderStyle.Render(label))
		} else {
			parts = append(parts, tabInactiveBorderStyle.Render(label))
		}
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, parts...)
}

// bodyWidth is the usable width of the main pane (sidebar, divider, window
// frame and padding are subtracted); used for rules and value truncation.
func (m *Model) bodyWidth() int {
	return maxInt(40, minInt(90, m.width-20))
}

// labelWidth computes the padded label column width for the active tab's
// visible rows so no label ever gets truncated.
func (m *Model) labelWidth() int {
	w := 0
	for _, idx := range m.visibleRows(m.active) {
		f := m.tabs[m.active].fields[idx]
		if f.kind == kSeparator {
			continue
		}
		if lw := lipgloss.Width(f.label); lw > w {
			w = lw
		}
	}
	return minInt(maxInt(w, 22)+2, maxInt(24, m.bodyWidth()/2))
}

// mirrorLine renders the mirror reachability indicator as a bordered box
// shown to the left of the config file path. It shows the mirror host with a
// red ✗ and short diagnostic when down (the live ISO shows "no network" while
// DHCP is still coming up), and "..." while a probe is in flight.
func (m *Model) mirrorLine() string {
	host := m.mirrorHost
	if host == "" {
		host = mirrorHostName(m.cfg.Gentoo.Mirror)
	}
	host = truncateRunes(host, 28)
	switch m.mirrorState {
	case mirrorChecking, mirrorUnknown:
		return mirrorBoxWarnStyle.Render(host + " ...")
	case mirrorOK:
		return mirrorBoxValidStyle.Render(host)
	default: // mirrorDown
		note := m.mirrorNote
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
func (m *Model) pathLine() string {
	mark := ""
	box := cfgBoxValidStyle
	errs := m.cfg.Validate()
	switch {
	case m.statusKind == stErr, len(errs) > 0:
		box = cfgBoxInvalidStyle
		mark = errorStyle.Render("✗")
	case m.dirty:
		box = cfgBoxWarnStyle
	case len(m.cfg.Advisories()) > 0:
		box = cfgBoxWarnStyle
		mark = warnStyle.Render("⚠")
	case m.savedFlash:
		mark = okStyle.Render("✓")
	}
	path := m.cfgPath
	if m.dirty {
		path += " " + dirtyStyle.Render("⚠")
	}
	s := path
	if mark != "" {
		s += " " + mark
	}
	return box.Render(s)
}

func (m *Model) renderFields() string {
	rows := m.visibleRows(m.active)
	cur := m.cursors[m.active]

	// Scroll window. Leaves room for masthead, path line and status.
	const viewportPad = 4
	maxRows := m.height - 11
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

	labelW := m.labelWidth()
	bodyW := m.bodyWidth()
	trunc := lipgloss.NewStyle().MaxWidth(bodyW)

	var b strings.Builder
	first := true
	for ri, rowIdx := range rows {
		if ri < start || ri >= end {
			continue
		}
		f := m.tabs[m.active].fields[rowIdx]
		if f.kind == kSeparator {
			if !first {
				b.WriteString("\n")
			}
			b.WriteString(sectionRule(f.label, bodyW) + "\n")
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
		label := f.label
		if pad := labelW - lipgloss.Width(label); pad > 0 {
			label += strings.Repeat(" ", pad)
		} else if pad < 0 {
			label = truncateRunes(label, -pad-1) + "…"
		}
		line := marker + style.Render(label) + " " + summaryOf(f, m.cfg)
		b.WriteString(trunc.Render(line) + "\n")
	}
	return b.String()
}

// sectionRule renders a titled section separator: "── Title ─────".
func sectionRule(title string, width int) string {
	t := titleStyle.Render(title)
	used := lipgloss.Width(t) + 1
	rule := ""
	if n := width - used; n > 2 {
		rule = treeGlyphStyle.Render(" " + strings.Repeat("─", n))
	}
	return t + rule
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
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
	for i, l := range logoArt {
		lines[i] = logoStyle.Render(l)
	}
	return strings.Join(lines, "\n")
}

// renderHints builds the left-hand vertical hint column in pVPN style:
// bracketed bold keys with dim action labels and the color-coded status
// message at the end.
func (m *Model) renderHints() string {
	onInstall := m.tabs[m.active].name == "Install"
	hints := [][2]string{
		{"↑/k ↓/j", "move"},
		{"Enter", "edit"},
		{"?", "help"},
		{"s", "save"},
		{"S", "save as"},
		{"1-" + fmt.Sprint(len(m.tabs)), "tabs"},
	}
	if onInstall {
		hints = append(hints, [2]string{"i", "install"}, [2]string{"d", "demo"},
			[2]string{"v", "config"})
	}
	hints = append(hints, [2]string{"q", "quit"})

	keyW := 0
	for _, h := range hints {
		if w := lipgloss.Width("[" + h[0] + "]"); w > keyW {
			keyW = w
		}
	}
	var b strings.Builder
	for i, h := range hints {
		if i > 0 {
			b.WriteString("\n")
		}
		key := helpStyle.Render("[") + hintKeyStyle.Render(h[0]) + helpStyle.Render("]")
		pad := strings.Repeat(" ", keyW-lipgloss.Width("["+h[0]+"]"))
		label := h[1]
		switch h[0] {
		case "install":
			label = "install 🚀"
		case "demo":
			label = "demo " + eFlask
		case "quit":
			label = "quit " + eDoor
		case "save":
			label = "save " + eSave
		}
		b.WriteString(key + pad + " " + helpStyle.Render(label))
	}
	return b.String()
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
