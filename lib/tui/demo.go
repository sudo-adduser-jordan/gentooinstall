package tui

import (
	"errors"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	demoReset  = "\x1b[m"
	demoBold   = "\x1b[1m"
	demoDim    = "\x1b[2m"
	demoRed    = "\x1b[1;31m"
	demoGreen  = "\x1b[1;32m"
	demoYellow = "\x1b[1;33m"
	demoCyan   = "\x1b[1;36m"
)

var (
	demoStepDelay = 700 * time.Millisecond
	demoCmdDelay  = 350 * time.Millisecond
)

func (model *Model) startDemo() (tea.Model, tea.Cmd) {
	if model.instState != instIdle {
		return model, nil
	}
	model.installing = true
	model.instState = instRunning
	model.instDemo = true
	model.vpInitReset()
	go runDemoInstall()
	return model, model.startSpinner()
}

type demoPhase struct {
	name   string
	cmds   []string
	emerge [][]string
}

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
		{"gnome-base/gnome"},
		{"app-editors/vim", "net-misc/networkmanager"},
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

const demoFailAfter = 2

func colorStep(name string) string {
	return demoCyan + "[+]" + demoReset + " " + demoBold + name + demoReset
}

func colorCmd(line string) string {
	if !strings.HasPrefix(line, "$ ") {
		return line
	}
	cmd := strings.TrimPrefix(line, "$ ")
	out := demoYellow + "$" + demoReset + " " + demoDim
	if rest, ok := strings.CutPrefix(cmd, "emerge "); ok {
		out += "emerge " + demoReset + colorPackages(rest)
	} else {
		out += cmd + demoReset
	}
	return out
}

func colorPackages(args string) string {
	parts := strings.Split(args, " ")
	for idx, pkg := range parts {
		if strings.Contains(pkg, "/") || strings.HasPrefix(pkg, "sys-") ||
			strings.HasPrefix(pkg, "dev-") || strings.HasPrefix(pkg, "virtual") ||
			strings.HasPrefix(pkg, ">=<") {
			parts[idx] = demoGreen + pkg + demoReset
		}
	}
	return strings.Join(parts, " ")
}

func colorErr(line string) string {
	return demoRed + line + demoReset
}

func colorPkgAtom(atom string) string {
	return demoGreen + atom + demoReset + demoCyan + ";"
}

func emitEmerge(atoms ...string) {
	EmitInstallLine(colorCmd("$ emerge --verbose --autounmask-continue=y -- " +
		strings.Join(atoms, " ")))
	time.Sleep(demoCmdDelay)
	for _, atom := range atoms {
		EmitInstallLine("  " + colorPkgAtom(atom))
		time.Sleep(demoCmdDelay)
	}
}

func runDemoInstall() {
	for idx, ph := range demoPhases {
		EmitInstallLine(colorStep(ph.name))
		time.Sleep(demoStepDelay)
		if idx == demoFailAfter && !emitDemoFailure(ph) {
			return
		}
		for _, cmd := range ph.cmds {
			EmitInstallLine(colorCmd(cmd))
			time.Sleep(demoCmdDelay)
		}
		for _, group := range ph.emerge {
			emitEmerge(group...)
		}
	}
	EmitInstallDone(nil)
}

func emitDemoFailure(ph demoPhase) bool {
	EmitInstallLine(colorCmd("$ wget -q https://distfiles.gentoo.org/…/stage3.tar.xz"))
	decide := make(chan InstallDecision)
	EmitInstallFailed(InstallFailedMsg{
		Cmdline: "wget https://distfiles.gentoo.org/releases/stage3.tar.xz",
		Err:     colorErr("simulated network failure (demo)"),
		Decide:  func(decision InstallDecision) { decide <- decision },
	})
	switch decision := <-decide; decision {
	case DecideRetry:
		return true
	default:
		EmitInstallDone(errors.New("aborted by user"))
		return false
	}
}
