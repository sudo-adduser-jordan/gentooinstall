package tui

import (
	"os"
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
	// DecideRetry reruns the failed step.
	DecideRetry InstallDecision = iota
	// DecideAbort aborts the installation.
	DecideAbort
)

// InstallFunc performs the entire installation, streaming progress via
// EmitInstallLine and finishing with EmitInstallDone.
type InstallFunc func() error

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
	// Decide is called with the user's choice.
	Decide func(InstallDecision)
}

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

// installCardH is the fixed outer height (border + padding included) of the
// install status card, chosen so the modal stays a constant size while
// steps scroll under the loading bar.
const installCardH = 24

var reAnsi = regexp.MustCompile(
	"\x1b\\[[0-9;?]*[ -/]*[@-~]|\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)?")

func stripAnsi(str string) string { return reAnsi.ReplaceAllString(str, "") }

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
func SetProgram(prog *tea.Program) { globalProgram = prog }

// EmitInstallLine streams one output line into the install window.
func EmitInstallLine(line string) { sendInstall(InstallLineMsg{Line: line}) }

// EmitInstallDone signals completion of the installation goroutine.
func EmitInstallDone(err error) { sendInstall(InstallDoneMsg{Err: err}) }

// EmitInstallFailed reports a failed step awaiting a user decision.
func EmitInstallFailed(failedMsg InstallFailedMsg) { sendInstall(failedMsg) }

// SetInstallFunc wires the install routine triggered by the confirm
// overlay (injected by main).
func (model *Model) SetInstallFunc(fn InstallFunc) { model.instFn = fn }

// InstallState reports idle/running/waiting/done/aborted (tests).
func (model *Model) InstallState() string { return instStateNames[model.instState] }

// InstallActive reports whether the full-screen install view is shown.
func (model *Model) InstallActive() bool { return model.installing }

// InstallLines returns the buffered log lines (tests).
func (model *Model) InstallLines() []string {
	out := make([]string, len(model.instLines))
	copy(out, model.instLines)
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

func newProgressModel(width int) progress.Model {
	prog := progress.New(progress.WithDefaultGradient())
	prog.Width = width
	return prog
}

func (model *Model) appendInstLine(line string) {
	raw := line // preserve ANSI codes for log overlay
	line = strings.TrimRight(stripAnsi(line), "\r \t")
	if line == "" {
		return
	}
	// Command echo (Runner logs "$ cmd" via Log, arriving as "[+] $ cmd"):
	// pin to the header line, never a checklist step. Otherwise every
	// mount/emerge floods the card and pins progress at (n-1)/n.
	if cmd, ok := strings.CutPrefix(line, "[+] $ "); ok {
		model.curCmd = strings.TrimSpace(cmd)
	} else if cmd, ok := strings.CutPrefix(line, "$ "); ok {
		model.curCmd = strings.TrimSpace(cmd)
	} else {
		switch {
		case strings.HasPrefix(line, "[+]"):
			name := strings.TrimSpace(strings.TrimPrefix(line, "[+]"))
			model.curStep = name
			model.markCurrentStepDone()
			if len(model.instSteps) == 0 || model.instSteps[len(model.instSteps)-1].name != name {
				model.instSteps = append(model.instSteps, instStep{name: name})
			}
		case strings.HasPrefix(line, "[!]"):
			if len(model.instSteps) > 0 {
				model.instSteps[len(model.instSteps)-1].failed = true
			}
		}
	}
	model.instLines = append(model.instLines, line)
	model.instRawLines = append(model.instRawLines, raw)
	if len(model.instLines) > maxInstLines {
		model.instLines = model.instLines[len(model.instLines)-maxInstLines:]
		model.instRawLines = model.instRawLines[len(model.instRawLines)-maxInstLines:]
	}
}

// markCurrentStepDone flags the active step complete before a new one starts.
func (model *Model) markCurrentStepDone() {
	for idx := range model.instSteps {
		if !model.instSteps[idx].done {
			model.instSteps[idx].done = true
			model.instSteps[idx].failed = false
		}
	}
}

// stepProgress returns the fraction of finished steps.
func (model *Model) stepProgress() float64 {
	total := len(model.instSteps)
	if total == 0 {
		return 0
	}
	done := 0
	for _, step := range model.instSteps {
		if step.done {
			done++
		}
	}
	if model.instState == instDone && done < total {
		done = total
	}
	return float64(done) / float64(total)
}

// beginInstall switches to the install view and launches the goroutine.
func (model *Model) beginInstall() {
	if model.instFn == nil || model.instState != instIdle {
		return
	}
	model.installing = true
	model.instState = instRunning
	model.instDemo = false
	model.vpInitReset()
	fn := model.instFn
	go func() {
		EmitInstallDone(fn())
	}()
}

// vpInitReset prepares the install card state.
func (model *Model) vpInitReset() {
	model.instSteps = nil
	model.prog = newProgressModel(40)
	model.appendInstLine("[+] Starting installation")
}

// requestStartInstall runs pre-flight collection (luks passphrase)
// before launching. Called from the confirm overlay button.
func (model *Model) requestStartInstall() {
	if model.usedEnc && os.Getenv(EncryptionKeyEnv) == "" {
		model.collectLuksKey("", "")
		return
	}
	model.beginInstall()
}

func (model *Model) collectLuksKey(note, pending string) {
	title := "Disk encryption passphrase (min 8 characters)"
	if pending != "" {
		title = "Repeat encryption passphrase"
	}
	model.openSecret(title, note, func(mm *Model, value string) {
		if value == "" {
			return // cancelled
		}
		if pending == "" {
			if len(value) < 8 {
				mm.collectLuksKey("Passphrase too short (min 8 characters).", "")
				return
			}
			mm.collectLuksKey("", value)
			return
		}
		if value != pending {
			mm.collectLuksKey("Passphrases do not match.", "")
			return
		}
		_ = os.Setenv(EncryptionKeyEnv, value)
		mm.beginInstall()
	})
}

// openSecret opens a masked single-line text overlay.
func (model *Model) openSecret(title, note string, onDone func(*Model, string)) {
	ti := textinput.New()
	ti.Placeholder = "enter passphrase"
	ti.EchoMode = textinput.EchoPassword
	ti.EchoCharacter = '*'
	ti.Focus()
	ti.CharLimit = 0
	ti.Width = 70
	model.overlay = overlay{kind: ovText, title: title, note: note, input: ti}
	model.textFn = &textState{fn: onDone}
}

// updateInstallMsg handles install-related tea.Msg values.
func (model *Model) updateInstallMsg(msg tea.Msg) {
	switch msg := msg.(type) {
	case InstallLineMsg:
		model.appendInstLine(msg.Line)
	case InstallFailedMsg:
		failedMsg := msg
		model.fail = &failedMsg
		model.btnCur = 0
		model.instState = instWaiting
		model.appendInstLine("[!] Command failed: " + msg.Cmdline + ": " + msg.Err)
	case InstallDoneMsg:
		if msg.Err != nil {
			model.instState = instAborted
			model.appendInstLine("[!] Installation aborted: " + msg.Err.Error())
		} else {
			model.instState = instDone
			model.markCurrentStepDone()
			model.appendInstLine("[+] Installation finished successfully " + eParty)
		}
		model.fail = nil
	}
}

// failButtons are the choices offered while waiting after a failure.
var failButtons = []string{"Retry", "Abort"}

func (model *Model) updateInstallKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		model.quitNow()
		return model, tea.Quit
	case "l":
		model.openLogOverlay()
		return model, nil
	}
	if model.instState == instWaiting && model.fail != nil {
		switch msg.String() {
		case "left", "h":
			if model.btnCur > 0 {
				model.btnCur--
			}
		case "right":
			if model.btnCur < len(failButtons)-1 {
				model.btnCur++
			}
		case "r":
			model.decideFail(DecideRetry)
		case "a":
			model.decideFail(DecideAbort)
		case "enter":
			switch model.btnCur {
			case 0:
				model.decideFail(DecideRetry)
			case 1:
				model.decideFail(DecideAbort)
			}
		}
		return model, nil
	}
	if model.instState == instWaiting {
		// Waiting but no failure panel (transient); nothing else to do.
		return model, nil
	}
	switch msg.String() {
	case "e", "esc":
		if model.instState == instDone || model.instState == instAborted {
			model.leaveInstallView()
		}
	case "q":
		return model.confirmQuit()
	}
	return model, nil
}

// leaveInstallView returns to the tabs. A finished or failed installation is
// reset to idle so a fresh installation can be started again without exiting
// the program.
func (model *Model) leaveInstallView() {
	model.installing = false
	if model.instState == instDone || model.instState == instAborted {
		model.instState = instIdle
		model.instDemo = false
		model.instSteps = nil
		model.curStep = ""
		model.curCmd = ""
	}
}

func (model *Model) decideFail(decision InstallDecision) {
	if model.fail != nil && model.fail.Decide != nil {
		fn := model.fail.Decide
		if decision == DecideRetry {
			model.fail = nil
			model.instState = instRunning
			if len(model.instSteps) > 0 {
				model.instSteps[len(model.instSteps)-1].failed = false
			}
			model.appendInstLine("[+] Retrying…")
		}
		fn(decision)
	}
}

// openLogOverlay shows the buffered raw output in a scrollable modal.
// It uses a dedicated viewport with tail-follow: new output jumps to the
// bottom while the user stays at the bottom, and scrolling up pauses the
// follow until End/G resumes it.
func (model *Model) openLogOverlay() {
	model.overlay = overlay{kind: ovLog}
	model.rawFollow = true
	model.rawDirty = true
	if model.rawVp.Height == 0 {
		// Sized on first render; jump to bottom once content is set.
		model.rawVp.GotoBottom()
	}
}

// renderFailPanel renders the decision panel shown while a step waits
// for the user's choice after a failure.
func (model *Model) renderFailPanel() string {
	var body strings.Builder
	body.WriteString(warnStyle.Render("$") + " " + helpStyle.Render(model.fail.Cmdline) + "\n")
	body.WriteString(warnStyle.Render(model.fail.Err) + "\n\n")
	var pills []string
	for idx, btn := range failButtons {
		style := pillInactiveStyle
		if idx == model.btnCur {
			style = pillActiveStyle
		}
		pills = append(pills, style.Render(btn))
	}
	body.WriteString(lipgloss.JoinHorizontal(lipgloss.Center, pills...))
	body.WriteString("\n" + helpStyle.Render("←→ choose · Enter confirm"))
	return body.String()
}

// renderInstallView draws the status card: pinned header and current
// command over the loading bar, with the step checklist scrolling up
// underneath it. The card has a constant size so streaming output never
// resizes the modal; the failure decision panel is stacked below, unchanged.
func (model *Model) renderInstallView() string {
	width, height := model.width, model.height
	if width == 0 {
		width, height = 80, 24
	}

	status, st := "running", helpStyle
	switch model.instState {
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
	switch model.instState {
	case instRunning:
		hint += "q quit (dangerous)"
	case instWaiting:
		hint += "←→ choose · Enter · r retry · a abort"
	case instDone:
		hint += "e back to tabs · q quit"
	default:
		hint += "e back to tabs · q quit"
	}

	cardW := maxInt(46, minInt(width-6, 84))
	barW := minInt(cardW-10, 56)
	if model.prog.Width != barW {
		model.prog.Width = barW
	}

	header := titleStyle.Render("Installation") +
		helpStyle.Render(" · ") + st.Render(status)

	var cmdLine string
	if model.curCmd != "" {
		// Single line: long chroot paths (e.g. devpts on
		// /tmp/gentoo-install/root/dev/pts) must ellipsize, never wrap
		// and break the fixed card height.
		cmdLine = truncateToWidth(unsetStyle.Render("$ "+model.curCmd), cardW-8)
	}

	bar := model.prog.ViewAs(model.stepProgress())

	// Data box: border 2 + padding 2 rows, then header (1), blank, cmd (1),
	// blank, bar (1), blank, checklist (rest), blank, hint (1). The
	// checklist window is bottom-anchored, so older steps scroll up under
	// the loading bar as new ones stream in. On short serial terminals
	// (e.g. 80x24 over -nographic) shrink to fit so lipgloss.Place can
	// still center the card instead of overflowing top-aligned.
	cardH := installCardH
	if height > 0 {
		cardH = minInt(installCardH, maxInt(14, height-2))
	}
	inner := cardH - 4
	checklistSlots := maxInt(1, inner-8)
	checklist := model.renderChecklist(cardW-8, checklistSlots)

	body := strings.Join([]string{
		header,
		cmdLine,
		bar,
		checklist,
		helpStyle.Render(hint),
	}, "\n\n")

	// lipgloss Height excludes the 2 border rows, so budget them out to hit
	// the fixed outer size.
	card := modalBoxStyle.Width(cardW - 6).Height(cardH - 2).Render(body)

	stack := []string{card}
	if model.fail != nil {
		panel := modalBoxStyle.Width(cardW - 6).
			BorderForeground(bad).
			Render(model.renderFailPanel())
		stack = append(stack, panel)
	}

	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center,
		lipgloss.JoinVertical(lipgloss.Center, stack...))
}

// renderChecklist renders the step list inside the available height with
// a sliding window around the newest entries.
func (model *Model) renderChecklist(maxW, maxH int) string {
	if len(model.instSteps) == 0 {
		return ""
	}
	maxH = maxInt(1, maxH)
	start := 0
	if count := len(model.instSteps); count > maxH {
		start = count - maxH
	}
	last := len(model.instSteps) - 1
	maxW = maxInt(24, maxW)
	var body strings.Builder
	for offset, step := range model.instSteps[start:] {
		idx := start + offset
		var line string
		switch {
		case step.failed:
			line = errorStyle.Render("✗ " + step.name)
		case step.done:
			line = okStyle.Render("✓ " + step.name)
		case idx == last && model.instState == instRunning:
			line = spinnerStyle.Render(model.spinner.View()) + " " +
				valueStyle.Render(step.name)
		default:
			line = unsetStyle.Render("· " + step.name)
		}
		// MaxWidth wraps; the fixed-height card needs one row per step.
		body.WriteString(truncateToWidth(line, maxW) + "\n")
	}
	return strings.TrimSuffix(body.String(), "\n")
}
