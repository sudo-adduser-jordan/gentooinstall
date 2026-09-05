package tui

import (
	"errors"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// EncryptionKeyEnv mirrors installer.EncryptionKeyEnv without importing
// the installer package from the TUI.
const EncryptionKeyEnv = "GENTOO_INSTALL_ENCRYPTION_KEY"

// InstallDecision is the user's answer to a failed installation step.
type InstallDecision int

const (
	DecideRetry  InstallDecision = iota // rerun the failed step
	DecideEditor                        // return to the config tabs first
	DecideAbort                         // abort the installation
)

// InstallFunc performs the entire installation, streaming progress via
// EmitInstallLine and finishing with EmitInstallDone.
type InstallFunc func() error

// ErrEditAndReturn is returned by an InstallFunc when the user chose to
// pause the installation and return to the config tabs. The install view
// resets to idle so a fresh installation can be started again later
// without leaving the program.
var ErrEditAndReturn = errors.New("installation paused by user")

// Messages driving the install view.
type InstallStartMsg struct{}

// InstallLineMsg carries one streamed output line.
type InstallLineMsg struct{ Line string }

// InstallDoneMsg terminates the install view; Err nil means success.
type InstallDoneMsg struct{ Err error }

// InstallFailedMsg reports a failed step and how to answer it.
type InstallFailedMsg struct {
	Cmdline string
	Err     string
	// Decide is called with the user's choice; Shell builds an
	// emergency shell command for tea.ExecProcess (may be nil).
	Decide func(InstallDecision)
	Shell  func() *exec.Cmd
}

type installShellDoneMsg struct{ err error }

// Install states.
const (
	instIdle = iota
	instRunning
	instWaiting
	instDone
	instAborted
)

var instStateNames = map[int]string{
	instIdle: "idle", instRunning: "running", instWaiting: "waiting",
	instDone: "done", instAborted: "aborted",
}

const maxInstLines = 5000

var reAnsi = regexp.MustCompile(
	"\x1b\\[[0-9;?]*[ -/]*[@-~]|\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)?")

func stripAnsi(s string) string { return reAnsi.ReplaceAllString(s, "") }

// Program plumbing (pVPN-style): background goroutines push messages
// through the shared program reference.
var globalProgram *tea.Program

func sendInstall(msg tea.Msg) {
	if globalProgram != nil {
		globalProgram.Send(msg)
	}
}

// SetProgram stores the running tea.Program so installer goroutines can
// stream into the TUI. Call before p.Run().
func SetProgram(p *tea.Program) { globalProgram = p }

// EmitInstallLine streams one output line into the install window.
func EmitInstallLine(line string) { sendInstall(InstallLineMsg{Line: line}) }

// EmitInstallDone signals completion of the installation goroutine.
func EmitInstallDone(err error) { sendInstall(InstallDoneMsg{Err: err}) }

// EmitInstallFailed reports a failed step awaiting a user decision.
func EmitInstallFailed(f InstallFailedMsg) { sendInstall(f) }

// SetInstallFunc wires the install routine triggered by the confirm
// overlay (injected by main).
func (m *Model) SetInstallFunc(fn InstallFunc) { m.instFn = fn }

// InstallState reports idle/running/waiting/done/aborted (tests).
func (m *Model) InstallState() string { return instStateNames[m.instState] }

// InstallActive reports whether the full-screen install view is shown.
func (m *Model) InstallActive() bool { return m.installing }

// InstallLines returns the buffered log lines (tests).
func (m *Model) InstallLines() []string {
	out := make([]string, len(m.instLines))
	copy(out, m.instLines)
	return out
}

// instStep is one entry of the progress checklist. Steps are discovered
// dynamically from "[+] <name>" log lines, which both host and chroot
// phases emit.
type instStep struct {
	name   string
	failed bool
	done   bool
}

func newProgressModel(w int) progress.Model {
	p := progress.New(progress.WithDefaultGradient())
	p.Width = w
	return p
}

func (m *Model) appendInstLine(line string) {
	raw := line // preserve ANSI codes for log overlay
	line = strings.TrimRight(stripAnsi(line), "\r \t")
	if line == "" {
		return
	}
	switch {
	case strings.HasPrefix(line, "[+]"):
		name := strings.TrimSpace(strings.TrimPrefix(line, "[+]"))
		m.curStep = name
		m.markCurrentStepDone()
		if len(m.instSteps) == 0 || m.instSteps[len(m.instSteps)-1].name != name {
			m.instSteps = append(m.instSteps, instStep{name: name})
		}
	case strings.HasPrefix(line, "[!]"):
		if len(m.instSteps) > 0 {
			m.instSteps[len(m.instSteps)-1].failed = true
		}
	}
	if strings.HasPrefix(line, "$ ") {
		m.curCmd = strings.TrimSpace(strings.TrimPrefix(line, "$ "))
	}
	m.instLines = append(m.instLines, line)
	m.instRawLines = append(m.instRawLines, raw)
	if len(m.instLines) > maxInstLines {
		m.instLines = m.instLines[len(m.instLines)-maxInstLines:]
		m.instRawLines = m.instRawLines[len(m.instRawLines)-maxInstLines:]
	}
}

// markCurrentStepDone flags the active step complete before a new one starts.
func (m *Model) markCurrentStepDone() {
	for i := range m.instSteps {
		if !m.instSteps[i].done {
			m.instSteps[i].done = true
			m.instSteps[i].failed = false
		}
	}
}

// stepProgress returns the fraction of finished steps.
func (m *Model) stepProgress() float64 {
	total := len(m.instSteps)
	if total == 0 {
		return 0
	}
	done := 0
	for _, s := range m.instSteps {
		if s.done {
			done++
		}
	}
	if m.instState == instDone && done < total {
		done = total
	}
	return float64(done) / float64(total)
}

// beginInstall switches to the install view and launches the goroutine.
func (m *Model) beginInstall() {
	if m.instFn == nil || m.instState != instIdle {
		return
	}
	m.installing = true
	m.instState = instRunning
	m.instDemo = false
	m.vpInitReset()
	fn := m.instFn
	go func() {
		EmitInstallDone(fn())
	}()
}

// vpInitReset prepares the install card state.
func (m *Model) vpInitReset() {
	m.instSteps = nil
	m.prog = newProgressModel(40)
	m.appendInstLine("[+] Starting installation")
}

// requestStartInstall runs pre-flight collection (luks passphrase)
// before launching. Called from the confirm overlay button.
func (m *Model) requestStartInstall() {
	if m.usedEnc && os.Getenv(EncryptionKeyEnv) == "" {
		m.collectLuksKey("", "")
		return
	}
	m.beginInstall()
}

func (m *Model) collectLuksKey(note, pending string) {
	title := "Disk encryption passphrase (min 8 characters)"
	if pending != "" {
		title = "Repeat encryption passphrase"
	}
	m.openSecret(title, note, func(mm *Model, v string) {
		if v == "" {
			return // cancelled
		}
		if pending == "" {
			if len(v) < 8 {
				mm.collectLuksKey("Passphrase too short (min 8 characters).", "")
				return
			}
			mm.collectLuksKey("", v)
			return
		}
		if v != pending {
			mm.collectLuksKey("Passphrases do not match.", "")
			return
		}
		_ = os.Setenv(EncryptionKeyEnv, v)
		mm.beginInstall()
	})
}

// openSecret opens a masked single-line text overlay.
func (m *Model) openSecret(title, note string, onDone func(*Model, string)) {
	ti := textinput.New()
	ti.Placeholder = "enter passphrase"
	ti.EchoMode = textinput.EchoPassword
	ti.EchoCharacter = '*'
	ti.Focus()
	ti.CharLimit = 0
	ti.Width = 70
	m.overlay = overlay{kind: ovText, title: title, note: note, input: ti}
	m.textFn = &textState{fn: onDone}
}

// updateInstallMsg handles install-related tea.Msg values.
func (m *Model) updateInstallMsg(msg tea.Msg) {
	switch msg := msg.(type) {
	case InstallLineMsg:
		m.appendInstLine(msg.Line)
	case InstallFailedMsg:
		f := msg
		m.fail = &f
		m.btnCur = 0
		m.instState = instWaiting
		m.appendInstLine("[!] Command failed: " + msg.Cmdline + ": " + msg.Err)
	case InstallDoneMsg:
		if errors.Is(msg.Err, ErrEditAndReturn) {
			// The user chose to return to the tabs; reset so a fresh
			// installation can be started from the Install tab.
			m.installing = false
			m.instState = instIdle
			m.instDemo = false
			m.fail = nil
			m.instSteps = nil
			m.curStep = ""
			m.curCmd = ""
			m.appendInstLine("[+] Installation paused. Returned to the tabs; " +
				"edit the config and restart from the Install tab.")
			return
		}
		if msg.Err != nil {
			m.instState = instAborted
			m.appendInstLine("[!] Installation aborted: " + msg.Err.Error())
		} else {
			m.instState = instDone
			m.markCurrentStepDone()
			m.appendInstLine("[+] Installation finished successfully " + eParty)
		}
		m.fail = nil
	case installShellDoneMsg:
		if msg.err != nil {
			m.appendInstLine("[+] Emergency shell exited: " + msg.err.Error())
		} else {
			m.appendInstLine("[+] Emergency shell exited")
		}
	}
}

// failButtons are the choices offered while waiting after a failure.
var failButtons = []string{"Retry", "Shell", "Editor", "Abort"}

func (m *Model) updateInstallKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.quitNow()
		return m, tea.Quit
	case "l":
		m.openLogOverlay()
		return m, nil
	}
	if m.instState == instWaiting && m.fail != nil {
		switch msg.String() {
		case "left", "h":
			if m.btnCur > 0 {
				m.btnCur--
			}
		case "right":
			if m.btnCur < len(failButtons)-1 {
				m.btnCur++
			}
		case "r":
			m.decideFail(DecideRetry)
		case "s":
			return m.runEmergencyShell()
		case "e":
			m.decideFail(DecideEditor)
			m.installing = false
		case "a":
			m.decideFail(DecideAbort)
		case "enter":
			switch m.btnCur {
			case 0:
				m.decideFail(DecideRetry)
			case 1:
				return m.runEmergencyShell()
			case 2:
				m.decideFail(DecideEditor)
				m.installing = false
			case 3:
				m.decideFail(DecideAbort)
			}
		}
		return m, nil
	}
	if m.instState == instWaiting {
		// Waiting but no failure panel (transient); nothing else to do.
		return m, nil
	}
	switch msg.String() {
	case "c":
		if m.instState == instDone {
			return m, tea.ExecProcess(
				exec.Command("sh", "-c", "cd /tmp/gentoo-install/root && chroot . /bin/bash"),
				func(err error) tea.Msg {
					if err != nil {
						m.appendInstLine("[!] chroot failed: " + err.Error())
					}
					return installShellDoneMsg{err: err}
				},
			)
		}
	case "e", "esc":
		if m.instState == instDone || m.instState == instAborted {
			m.leaveInstallView()
		}
	case "q":
		return m.confirmQuit()
	}
	return m, nil
}

// leaveInstallView returns to the tabs. A finished or failed installation is
// reset to idle so a fresh installation can be started again without exiting
// the program; only a run still waiting on the user's decision keeps its
// state so it can be resumed with i.
func (m *Model) leaveInstallView() {
	m.installing = false
	if m.instState == instDone || m.instState == instAborted {
		m.instState = instIdle
		m.instDemo = false
		m.instSteps = nil
		m.curStep = ""
		m.curCmd = ""
	}
}

func (m *Model) decideFail(d InstallDecision) {
	if m.fail != nil && m.fail.Decide != nil {
		fn := m.fail.Decide
		if d == DecideRetry {
			m.fail = nil
			m.instState = instRunning
			if len(m.instSteps) > 0 {
				m.instSteps[len(m.instSteps)-1].failed = false
			}
			m.appendInstLine("[+] Retrying…")
		}
		fn(d)
	}
}

func (m *Model) runEmergencyShell() (tea.Model, tea.Cmd) {
	if m.fail == nil || m.fail.Shell == nil {
		return m, nil
	}
	cmd := m.fail.Shell()
	m.appendInstLine("[+] Opening emergency shell (exit to return)")
	return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
		return installShellDoneMsg{err: err}
	})
}

// openLogOverlay shows the buffered raw output in a scrollable modal.
func (m *Model) openLogOverlay() {
	m.overlay = overlay{kind: ovLog}
	if m.logVp.Height == 0 {
		// Sized on first render; jump to bottom once content is set.
		m.logVp.GotoBottom()
	}
}

// renderFailPanel renders the decision panel shown while a step waits
// for the user's choice after a failure.
func (m *Model) renderFailPanel() string {
	var b strings.Builder
	b.WriteString(warnStyle.Render("$") + " " + helpStyle.Render(m.fail.Cmdline) + "\n")
	b.WriteString(warnStyle.Render(m.fail.Err) + "\n\n")
	var pills []string
	for i, btn := range failButtons {
		s := pillInactiveStyle
		if i == m.btnCur {
			s = pillActiveStyle
		}
		pills = append(pills, s.Render(btn))
	}
	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Center, pills...))
	b.WriteString("\n" + helpStyle.Render("←→ choose · Enter confirm"))
	return b.String()
}

// renderInstallView draws the status card: title, loading bar, checklist,
// current command and (on failure) the decision panel. No scrolling log —
// that lives behind the `l` overlay.
func (m *Model) renderInstallView() string {
	w, h := m.width, m.height
	if w == 0 {
		w, h = 100, 30
	}

	status, st := "running", helpStyle
	switch m.instState {
	case instRunning:
		status = "running"
		st = okStyle
	case instWaiting:
		status = "paused — action required"
		st = warnStyle
	case instDone:
		status = "finished"
		st = okStyle
	case instAborted:
		status = "failed"
		st = errorStyle
	}

	hint := "l raw output · "
	switch m.instState {
	case instRunning:
		hint += "q quit (dangerous)"
	case instWaiting:
		hint += "←→ choose · Enter · r retry · s shell · e edit · a abort"
	case instDone:
		hint += "c chroot · e back to tabs · q quit"
	default:
		hint += "e back to tabs · q quit"
	}

	cardW := maxInt(46, minInt(w-6, 84))
	barW := minInt(cardW-10, 56)
	if m.prog.Width != barW {
		m.prog.Width = barW
	}

	step := m.curStep
	if step == "" {
		step = "starting…"
	}

	header := titleStyle.Render("Installation") +
		helpStyle.Render(" · ") + st.Render(status)

	var cmdLine string
	if m.curCmd != "" {
		cmdLine = "\n" + unsetStyle.Render("$ "+m.curCmd)
	}

	bar := m.prog.ViewAs(m.stepProgress())

	checklist := m.renderChecklist(cardW-8, h-12)

	body := header + cmdLine + "\n\n" + bar + "\n" + checklist + "\n\n" +
		helpStyle.Render(hint)

	card := modalBoxStyle.Width(cardW - 6).Render(body)

	stack := []string{card}
	if m.fail != nil {
		panel := modalBoxStyle.Width(cardW - 6).
			BorderForeground(bad).
			Render(m.renderFailPanel())
		stack = append(stack, panel)
	}

	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center,
		lipgloss.JoinVertical(lipgloss.Center, stack...))
}

// renderChecklist renders the step list inside the available height with
// a sliding window around the newest entries.
func (m *Model) renderChecklist(maxW, maxH int) string {
	if len(m.instSteps) == 0 {
		return ""
	}
	maxH = maxInt(1, maxH)
	start := 0
	if n := len(m.instSteps); n > maxH {
		start = n - maxH
	}
	last := len(m.instSteps) - 1
	trunc := lipgloss.NewStyle().MaxWidth(maxInt(24, maxW))
	var b strings.Builder
	if start > 0 {
		b.WriteString(unsetStyle.Render("  ⋮") + "\n")
	}
	for i, s := range m.instSteps[start:] {
		idx := start + i
		var line string
		switch {
		case s.failed:
			line = errorStyle.Render("✗ " + s.name)
		case s.done:
			line = okStyle.Render("✓ " + s.name)
		case idx == last && m.instState == instRunning:
			line = spinnerStyle.Render(m.spinner.View()) + " " +
				valueStyle.Render(s.name)
		default:
			line = unsetStyle.Render("· " + s.name)
		}
		b.WriteString(trunc.Render(line) + "\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}
