package tui

import (
	"errors"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// ANSI color codes matching real emerge/portage output.
const (
	demoReset  = "\x1b[m"
	demoBold   = "\x1b[1m"
	demoDim    = "\x1b[2m"
	demoRed    = "\x1b[1;31m"
	demoGreen  = "\x1b[1;32m"
	demoYellow = "\x1b[1;33m"
	demoCyan   = "\x1b[1;36m"
)

// demoDelay scales every sleep in the simulated install. Tests can slow
// nothing down; the defaults keep the whole demo around 15 seconds.
var (
	demoStepDelay = 700 * time.Millisecond
	demoCmdDelay  = 350 * time.Millisecond
)

// startDemo launches the simulated installation from the Install tab.
func (m *Model) startDemo() (tea.Model, tea.Cmd) {
	if m.instState != instIdle {
		return m, nil
	}
	m.installing = true
	m.instState = instRunning
	m.instDemo = true
	m.vpInitReset()
	go runDemoInstall()
	return m, m.startSpinner()
}

type demoPhase struct {
	name string
	cmds []string
	// emerge groups one emerge invocation per inner slice; each emits the
	// command line followed by a "category/name;" progress line per atom.
	emerge [][]string
}

// demoPhases mirrors the real host+chroot step sequence closely enough to
// exercise the progress checklist and failure panel.
var demoPhases = []demoPhase{
	{name: "Preparing installation environment", cmds: []string{
		"$ checking required programs (parted, zfs, cryptsetup…)",
		"$ timedatectl set-ntp true",
	}},
	{name: "Applying disk configuration", cmds: []string{
		"$ sgdisk --zap-all /dev/sda",
		"$ parted -s /dev/sda mklabel gpt",
		"$ mkfs.vfat -F32 /dev/sda1",
		"$ mkswap /dev/sda2",
	}},
	{name: "Downloading stage3", cmds: []string{
		"$ wget -q https://distfiles.gentoo.org/releases/amd64/…/stage3.tar.xz",
	}},
	{name: "Extracting stage3", cmds: []string{
		"$ tar xpf stage3.tar.xz --xattrs-headers --numeric-owner -C /mnt/gentoo",
	}},
	{name: "Configuring base system", cmds: []string{
		"$ mount --types proc /proc /mnt/gentoo/proc",
		"$ echo 'C.UTF-8 UTF-8' >> /etc/locale.gen",
	}},
	{name: "Configuring portage", cmds: []string{
		"$ emerge-webrsync",
		"$ eselect news read all",
	}},
	{name: "Enabling repositories", cmds: []string{
		"$ emerge --quiet app-eselect/eselect-repository",
		"$ eselect repository enable guru",
		"$ emaint sync -r guru",
	}},
	{name: "Installing kernel", cmds: []string{
		"$ emerge --verbose sys-kernel/gentoo-kernel-bin",
	}},
	{name: "Installing packages", emerge: [][]string{
		{"gnome-base/gnome"},                           // profile packages
		{"app-editors/vim", "net-misc/networkmanager"}, // additional + custom
	}},
	{name: "Configuring bootloader", cmds: []string{
		"$ grub-install --target=x86_64-efi --efi-directory=/boot/efi",
		"$ grub-mkconfig -o /boot/grub/grub.cfg",
	}},
	{name: "Finalizing system", cmds: []string{
		"$ systemctl enable sshd",
		"$ passwd -d root",
	}},
}

// demoFailAfter is the phase index that fails once (stage3 download).
const demoFailAfter = 2

// colorStep wraps a [+] step marker with bold cyan.
func colorStep(name string) string {
	return demoCyan + "[+]" + demoReset + " " + demoBold + name + demoReset
}

// colorCmd wraps a "$ cmd" line with a yellow prompt and dim arguments,
// highlighting known package names in green.
func colorCmd(line string) string {
	if !strings.HasPrefix(line, "$ ") {
		return line
	}
	cmd := strings.TrimPrefix(line, "$ ")
	out := demoYellow + "$" + demoReset + " " + demoDim
	// Highlight package atoms in emerge lines.
	if rest, ok := strings.CutPrefix(cmd, "emerge "); ok {
		out += "emerge " + demoReset + colorPackages(rest)
	} else {
		out += cmd + demoReset
	}
	return out
}

// colorPackages highlights space-separated package atoms in green.
func colorPackages(args string) string {
	parts := strings.Split(args, " ")
	for i, p := range parts {
		if strings.Contains(p, "/") || strings.HasPrefix(p, "sys-") ||
			strings.HasPrefix(p, "dev-") || strings.HasPrefix(p, "virtual") ||
			strings.HasPrefix(p, ">=<") {
			parts[i] = demoGreen + p + demoReset
		}
	}
	return strings.Join(parts, " ")
}

// colorErr wraps an error/failure line in red.
func colorErr(line string) string {
	return demoRed + line + demoReset
}

// colorPkgAtom renders a "category/name;" package atom for the install stream.
func colorPkgAtom(atom string) string {
	return demoGreen + atom + demoReset + demoCyan + ";"
}

// emitEmerge simulates one emerge invocation: the command line is printed,
// then a "category/name;" output line is streamed for each atom.
func emitEmerge(atoms ...string) {
	EmitInstallLine(colorCmd("$ emerge --verbose --autounmask-continue=y -- " +
		strings.Join(atoms, " ")))
	time.Sleep(demoCmdDelay)
	for _, a := range atoms {
		EmitInstallLine("  " + colorPkgAtom(a))
		time.Sleep(demoCmdDelay)
	}
}

// runDemoInstall walks the phases in a goroutine, streams the same kinds
// of lines a real install emits, fails exactly once so the recovery panel
// can be exercised, then finishes successfully.
func runDemoInstall() {
	for i, ph := range demoPhases {
		EmitInstallLine(colorStep(ph.name))
		time.Sleep(demoStepDelay)
		if i == demoFailAfter && !emitDemoFailure(ph) {
			return
		}
		for _, c := range ph.cmds {
			EmitInstallLine(colorCmd(c))
			time.Sleep(demoCmdDelay)
		}
		for _, group := range ph.emerge {
			emitEmerge(group...)
		}
	}
	EmitInstallDone(nil)
}

// emitDemoFailure reports a failed command and blocks until the user
// decides. It reports whether the install should continue.
func emitDemoFailure(ph demoPhase) bool {
	EmitInstallLine(colorCmd("$ wget -q https://distfiles.gentoo.org/…/stage3.tar.xz"))
	decide := make(chan InstallDecision)
	EmitInstallFailed(InstallFailedMsg{
		Cmdline: "wget https://distfiles.gentoo.org/releases/stage3.tar.xz",
		Err:     colorErr("simulated network failure (demo)"),
		Decide:  func(d InstallDecision) { decide <- d },
	})
	switch d := <-decide; d {
	case DecideAbort:
		EmitInstallDone(errors.New("aborted by user"))
		return false
	default:
		// Retry (and Editor, after saving) resumes the demo.
		return true
	}
}
