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

const EncryptionKeyEnv = "GENTOO_INSTALL_ENCRYPTION_KEY"

type InstallDecision int

const (
	DecideRetry InstallDecision = iota
	DecideAbort
)

type InstallFunc func() error

type InstallStartMsg struct{}

type InstallLineMsg struct{ Line string }

type InstallDoneMsg struct{ Err error }

type InstallFailedMsg struct {
	Cmdline string
	Err     string
	Decide  func(InstallDecision)
}

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

const installCardH = 24

var reAnsi = regexp.MustCompile(
	"\x1b\\[[0-9;?]*[ -/]*[@-~]|\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)?")

func stripAnsi(str string) string { return reAnsi.ReplaceAllString(str, "") }

var globalProgram *tea.Program

func sendInstall(msg tea.Msg) {
	if globalProgram != nil {
		globalProgram.Send(msg)
	}
}

func SetProgram(prog *tea.Program) { globalProgram = prog }

func EmitInstallLine(line string) { sendInstall(InstallLineMsg{Line: line}) }

func EmitInstallDone(err error) { sendInstall(InstallDoneMsg{Err: err}) }

func EmitInstallFailed(failedMsg InstallFailedMsg) { sendInstall(failedMsg) }

func (model *Model) SetInstallFunc(fn InstallFunc) { model.instFn = fn }

func (model *Model) InstallState() string { return instStateNames[model.instState] }

func (model *Model) InstallActive() bool { return model.installing }

func (model *Model) InstallLines() []string {
	out := make([]string, len(model.instLines))
	copy(out, model.instLines)
	return out
}

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
	raw := line
	line = strings.TrimRight(stripAnsi(line), "\r \t")
	if line == "" {
		return
	}
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

func (model *Model) markCurrentStepDone() {
	for idx := range model.instSteps {
		if !model.instSteps[idx].done {
			model.instSteps[idx].done = true
			model.instSteps[idx].failed = false
		}
	}
}

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

func (model *Model) vpInitReset() {
	model.instSteps = nil
	model.prog = newProgressModel(40)
	model.appendInstLine("[+] Starting installation")
}

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
			return
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

func (model *Model) openLogOverlay() {
	model.overlay = overlay{kind: ovLog}
	model.rawFollow = true
	model.rawDirty = true
	if model.rawVp.Height == 0 {
		model.rawVp.GotoBottom()
	}
}

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
		cmdLine = truncateToWidth(unsetStyle.Render("$ "+model.curCmd), cardW-8)
	}

	bar := model.prog.ViewAs(model.stepProgress())

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
		body.WriteString(truncateToWidth(line, maxW) + "\n")
	}
	return strings.TrimSuffix(body.String(), "\n")
}
