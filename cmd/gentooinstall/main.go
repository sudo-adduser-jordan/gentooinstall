// Command gentooinstall is the Go rewrite of gentoo-install: a pVPN-style
// numbered-tabs configurator plus a full installation engine.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"gentooinstall/internal/config"
	"gentooinstall/internal/disklayout"
	"gentooinstall/internal/installer"
	"gentooinstall/internal/live"
	"gentooinstall/internal/sysinfo"
	"gentooinstall/internal/tui"
)

var version = "0.1.0"

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "\x1b[1;31merror:\x1b[m "+format+"\n", args...)
	os.Exit(1)
}

func requireRoot() {
	if os.Geteuid() != 0 {
		fatal("must be root")
	}
}

// defaultConfigPath returns the configuration file used when none is given.
// It prefers builds/custom.toml next to the process (the repo checkout, the
// live ISO ramfs, or the release archive, where builds/*.toml always ships)
// and falls back to the builds/default.toml template until the first save.
// A bare binary without a builds/ directory (e.g. go install) can neither
// find nor create one under <cwd>, so it falls back to the per-user config
// file ($XDG_CONFIG_HOME/gentooinstall/custom.toml), created on first save.
func defaultConfigPath() string {
	buildDir := filepath.Join(cwd(), "builds")
	if _, err := os.Stat(buildDir); err == nil {
		custom := filepath.Join(buildDir, config.CustomConfigName)
		if _, err := os.Stat(custom); err == nil {
			return custom
		}
		return filepath.Join(buildDir, config.DefaultConfigName)
	}
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "gentooinstall", config.CustomConfigName)
	}
	return filepath.Join(cwd(), ".config", "gentooinstall", config.CustomConfigName)
}

func cwd() string {
	d, err := os.Getwd()
	if err != nil {
		return "."
	}
	return d
}

const usage = `gentooinstall — gentoo installer (Go/Charm edition)

Usage:
  gentooinstall                    Open the interactive configurator (builds/custom.toml, or
                                   builds/default.toml until the first save; without a builds/
                                   directory it uses $XDG_CONFIG_HOME/gentooinstall/custom.toml).
  gentooinstall [config.toml]      Open the interactive configurator for the given file.
  gentooinstall install [CONFIG]   Run the installation as configured, streaming raw
                                   output to the terminal (no TUI). CONFIG defaults to
                                   builds/custom.toml, falling back to builds/default.toml.
                                   Example: gentooinstall install builds/desktop-systemd.toml
  gentooinstall gif [OUT]          Record the simulated install demo into the given file
                                   as an animated GIF (default ./demo.gif) using the
                                   external charmbracelet/vhs CLI (must be installed:
                                   go install github.com/charmbracelet/vhs@latest).
  gentooinstall chroot DIR [CMD...]  Chroot into an existing system mounted at DIR.
  gentooinstall help               Show this help.

Options:
  -c, --config PATH   Configuration file (default builds/custom.toml if present,
                      else builds/default.toml, else the per-user config file
                      $XDG_CONFIG_HOME/gentooinstall/custom.toml)
  -v, --version       Print version

Pre-made build configurations live in builds/ (default, openrc, musl,
desktop-systemd, bios, btrfs-efi, raid0-efi, raid1-efi, existing-efi, zfs-efi,
...). The installer performs partitioning (destructive!), downloads and
verifies a stage3 tarball and completes configuration inside a chroot.`

func main() {
	args := os.Args[1:]

	cfgPath := ""
	mode := ""
	var rest []string

	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "-h", "--help", "help":
			fmt.Println(usage)
			return
		case "-v", "--version":
			fmt.Println("gentooinstall", version)
			return
		case "-c", "--config":
			if i+1 >= len(args) {
				fatal("--config requires a path")
			}
			i++
			cfgPath = args[i]
		case "install":
			if mode == "gif" {
				fatal("invalid argument '%s'", a)
			}
			mode = "install"
		case "gif":
			if mode == "install" {
				fatal("invalid argument '%s'", a)
			}
			mode = "gif"
			rest = args[i+1:]
			i = len(args)
		case "chroot":
			mode = "chroot"
			rest = args[i+1:]
			i = len(args)
		case "--in-chroot":
			mode = "in-chroot"
		default:
			if cfgPath == "" && !strings.HasPrefix(a, "-") {
				cfgPath = a // positional config for TUI/install mode
			} else {
				fatal("invalid argument '%s'", a)
			}
		}
		i++
	}

	if mode == "gif" {
		runGIF(rest)
		return
	}

	if cfgPath == "" {
		cfgPath = defaultConfigPath()
	}
	cfgAbs, err := filepath.Abs(cfgPath)
	if err != nil {
		fatal("%v", err)
	}

	// When run as PID 1 the binary is the live-ISO init (rdinit=/init). Emit
	// a deterministic startup banner so the boot is observable on a headless
	// serial console (e.g. the QEMU e2e test) before the TUI opens.
	if os.Getpid() == 1 {
		// The banner goes to /dev/console, which on a graphical boot is tty0.
		// Mirror it to the serial port so headless serial consoles still see it.
		msg := fmt.Sprintf("gentooinstall init: PID 1, version %s\n", version)
		fmt.Print(msg)
		mirrorSerialBanner(msg)
		// Bootstrap the live environment (mount proc/sys/dev, load the bundled
		// storage drivers so disks appear, bring up DHCP). Best-effort: any
		// failure is logged but never keeps the TUI from starting.
		if err := live.Init(); err != nil {
			fmt.Fprintf(os.Stderr, "live init: %v\n", err)
		}
		// Probe the default mirror in the background and mirror the result to
		// the serial console so headless e2e tests can assert that DNS + outbound
		// HTTPS + CA certs all work inside the ISO (the things the tarball fetch
		// depends on). This never blocks the TUI from opening.
		go probeMirrorSerial()
		setupFBTty()
	}

	switch mode {
	case "":
		runTUI(cfgAbs)
	case "install":
		runInstall(cfgAbs)
	case "chroot":
		runChroot(rest)
	case "in-chroot":
		runInChroot(cfgAbs)
	}
}

// setupFBTty claims the first virtual terminal for the interactive live-ISO
// boot. The initramfs ships no device nodes beyond /dev/console, and the
// kernel hands PID1 a console whose writes do not reach the fbcon screen on a
// GRUB+gfxpayload boot. Gettys solve the same problem by opening the VT
// directly: recreate the missing /dev/tty1 node, make it the controlling
// terminal and rebind stdio to it so the framebuffer console (fbcon displays
// VT1 once it takes over) receives the TUI and the keyboard. Failures are
// ignored; headless serial boots simply keep /dev/console.
func setupFBTty() {
	if _, err := os.Stat("/dev/tty1"); err != nil {
		if err := syscall.Mknod("/dev/tty1", syscall.S_IFCHR|0o600, int(4<<8|1)); err != nil {
			return
		}
	}
	f, err := os.OpenFile("/dev/tty1", os.O_RDWR, 0)
	if err != nil {
		return
	}
	defer f.Close()
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TIOCSCTTY, 0); e != 0 {
		return
	}
	_ = syscall.Dup2(int(f.Fd()), 0)
	_ = syscall.Dup2(int(f.Fd()), 1)
	_ = syscall.Dup2(int(f.Fd()), 2)
}

// mirrorSerialBanner writes msg to the first serial port (ttyS0) so headless
// serial consoles observe the boot even when /dev/console is a graphical tty0.
// The initramfs ships no device nodes, so as PID 1 (root) we create a missing
// /dev/ttyS0 on the fly (char major 4, minor 64); any failure is ignored.
func mirrorSerialBanner(msg string) {
	const path = "/dev/ttyS0"
	if _, err := os.Stat(path); err != nil {
		dev := uint32(4)<<8 | 64
		if err := syscall.Mknod(path, syscall.S_IFCHR|0o600, int(dev)); err != nil {
			return
		}
	}
	if f, err := os.OpenFile(path, os.O_WRONLY, 0); err == nil {
		_, _ = f.WriteString(msg)
		_ = f.Close()
	}
}

// probeMirrorSerial resolves the default Gentoo mirror over the live network
// and mirrors the outcome to the serial console (`live: mirror <host>: ok` /
// `fail: <note>`). It only takes effect when running as the live-ISO init.
// It waits briefly for the concurrent DHCP bring-up to populate /etc/resolv.conf
// so the probe exercises real DNS + outbound HTTPS + CA certs end-to-end.
func probeMirrorSerial() {
	if os.Getpid() != 1 {
		return
	}
	mirror := config.Default(false).Gentoo.Mirror
	for i := 0; i < 6; i++ {
		if live.NetworkReady() {
			break
		}
		time.Sleep(2 * time.Second)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	host := sysinfo.MirrorHost(mirror)
	st := sysinfo.MirrorProbe(ctx, mirror)
	// Serial-only: this runs in a goroutine after the TUI owns the terminal, so
	// a stdout write here would smear across the TUI's framebuffer tty.
	mirrorSerialBanner(fmt.Sprintf("live: mirror %s: %s\n", host, noteToSerial(st)))
}

// noteToSerial maps a probe status to the serial-friendly ok/fail summary used
// by the headless mirror self-check.
func noteToSerial(st sysinfo.MirrorStatus) string {
	if st.OK {
		return "ok"
	}
	if st.Note == "" {
		return "fail: unreachable"
	}
	return "fail: " + st.Note
}

// runGIF records the simulated install demo as an animated GIF by driving
// the real TUI inside the external charmbracelet/vhs virtual terminal. The
// demo is non-destructive (it never touches disks), so it is safe for any
// user. The `vhs` binary must be installed separately.
func runGIF(args []string) {
	out := "demo.gif"
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-o" || a == "--output":
			if i+1 >= len(args) {
				fatal("--output requires a path")
			}
			i++
			out = args[i]
		case !strings.HasPrefix(a, "-"):
			out = a
		default:
			fatal("invalid argument '%s'", a)
		}
	}

	if _, err := exec.LookPath("vhs"); err != nil {
		fatal("vhs is required to record GIFs. Install it with:\n" +
			"    go install github.com/charmbracelet/vhs@latest")
	}

	tape, err := os.CreateTemp("", "gentooinstall-demo-*.tape")
	if err != nil {
		fatal("tape: %v", err)
	}
	tapePath := tape.Name()
	defer os.Remove(tapePath)

	if _, err := tape.WriteString(gifTape(out)); err != nil {
		tape.Close()
		fatal("tape: %v", err)
	}
	if err := tape.Close(); err != nil {
		fatal("tape: %v", err)
	}

	cmd := exec.Command("vhs", tapePath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fatal("vhs: %v", err)
	}
	fmt.Println("[+] wrote", out)
}

// gifTape returns a VHS tape that boots the gentooinstall configurator, walks
// the numbered tabs, starts the simulated install demo on the Install tab,
// lets it run to completion (retrying the single simulated failure), then
// quits. We spawn the real binary (this process's own argv[0]) so the
// recording captures the actual TUI rather than a replayed message stream.
// The Output path is quoted: VHS's lexer otherwise treats a leading "/" of an
// absolute path as a Wait-regex delimiter. Instead of fixed sleeps, the demo
// is synchronized with `Wait+Screen /regex/` commands that poll the rendered
// terminal rows, so the retry and quit presses land exactly when the
// corresponding screens are visible (line-scoped `Wait` sees no output from
// full-screen TUIs); `Set WaitTimeout 30s` covers the whole demo.
func gifTape(out string) string {
	b := argv0()
	return strings.Join([]string{
		`Output "` + out + `"`,
		"Set Width 2000",
		"Set Height 700",
		"Set FontSize 16",
		// VHS's default font stack (vhs.go) lists only coding monospaces and
		// ends in "Apple Symbols"; none render colour emoji, so the tab icons
		// fall back to a glyph with an inconsistent width and the tab boxes'
		// right borders shift. Appending "Noto Color Emoji" (uniform 2-cell
		// advance per glyph) makes the canvas cascade render emoji at exactly
		// the width lipgloss/xterm.js assume, keeping the borders aligned.
		`Set FontFamily "JetBrains Mono, DejaVu Sans Mono, Noto Sans Mono, Noto Color Emoji, monospace"`,
		"Set Padding 12",
		// The demo takes >15s to complete after the retry, so give the
		// screen-scoped waits a larger budget than the 15s default.
		"Set WaitTimeout 30s",
		"",
		`Type "` + b + `"`,
		"Enter",
		"Sleep 1.2s",
		"",
		// Walk the numbered tabs so the recording shows each section.
		"Type \"2\"",
		"Sleep 700ms",
		"Type \"3\"",
		"Sleep 700ms",
		"Type \"4\"",
		"Sleep 700ms",
		"Type \"5\"",
		"Sleep 700ms",
		"Type \"6\"",
		"Sleep 700ms",
		"Type \"1\"",
		"Sleep 700ms",
		"",
		// Open the Install tab and start the simulated demo.
		"Type \"6\"",
		"Sleep 400ms",
		"Type \"d\"",
		// The demo fails once (stage3 download) and shows the decision
		// panel; Retry resumes it. The Wait+Screen polls land on the exact
		// frame the panel appears; hold briefly so viewers can read it,
		// then retry, then hold again on the success screen before quitting.
		"Wait+Screen /action required/",
		"Sleep 2s",
		"Type \"r\"",
		"Wait+Screen /finished successfully/",
		"Sleep 2s",
		"Type \"q\"",
		"Sleep 500ms",
		"",
	}, "\n")
}

// argv0 returns the absolute path of the running binary so the VHS tape can
// relaunch it. fallbacks to a bare "gentooinstall" on PATH if that fails.
func argv0() string {
	if exe, err := os.Executable(); err == nil {
		if abs, err := filepath.Abs(exe); err == nil {
			return abs
		}
		return exe
	}
	return "gentooinstall"
}

func runTUI(cfgPath string) {
	// Under CI (e.g. the GitHub Actions demo-GIF job), termenv/lipgloss
	// detect no color support and render monochrome, even though the VHS
	// recording terminal supports truecolor. Force a color profile so the
	// recorded TUI keeps its colours.
	if lipgloss.ColorProfile() == termenv.Ascii {
		lipgloss.SetColorProfile(termenv.TrueColor)
	}

	hasEFI := sysinfo.HasEFI()
	cfg, _, err := config.LoadOrDefault(cfgPath, hasEFI)
	if err != nil {
		fatal("%v", err)
	}
	fillSystemDefaults(cfg)

	model := tui.New(cfg, cfgPath)
	model.SetInstallFunc(func() error {
		return runInstallTUI(cfg, cfgPath)
	})

	// As the live-ISO init we render to the virtual terminal created by
	// setupFBTty instead of /dev/console: Bubble Tea only reaches the fbcon
	// screen when handed a freshly opened /dev/tty1 file, and stdin (bound to
	// tty1 by setupFBTty) carries the keyboard. Everywhere else the program
	// renders to the caller's stdio.
	opts := []tea.ProgramOption{tea.WithAltScreen()}
	if os.Getpid() == 1 {
		if out, err := os.OpenFile("/dev/tty1", os.O_WRONLY, 0); err == nil {
			opts = append(opts, tea.WithOutput(out))
		}
	}
	p := tea.NewProgram(model, opts...)
	tui.SetProgram(p)
	if _, err := p.Run(); err != nil {
		mirrorSerialBanner(fmt.Sprintf("tui: error: %v\n", err))
		fatal("tui: %v", err)
	}
}

// fillSystemDefaults detects timezone/keymap when unset.
func fillSystemDefaults(cfg *config.Config) {
	if strings.TrimSpace(cfg.System.Timezone) == "" {
		cfg.System.Timezone = sysinfo.CurrentTimezone()
	}
	if strings.TrimSpace(cfg.System.Keymap) == "" {
		kms := sysinfo.Keymaps()
		if len(kms) == 0 {
			kms = sysinfo.FallbackKeymaps
		}
		cfg.System.Keymap = sysinfo.DefaultKeymap(kms)
	}
	if strings.TrimSpace(cfg.System.KeymapInitramfs) == "" {
		cfg.System.KeymapInitramfs = cfg.System.Keymap
	}
}

func loadConfigForInstall(cfgPath string) *config.Config {
	cfg, existed, err := config.LoadOrDefault(cfgPath, sysinfo.HasEFI())
	if err != nil {
		fatal("%v", err)
	}
	if !existed {
		fatal("configuration file '%s' does not exist. Run 'gentooinstall %s' first.",
			cfgPath, cfgPath)
	}
	fillSystemDefaults(cfg)
	if errs := cfg.Validate(); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, " - "+e.Error())
		}
		fatal("configuration is invalid")
	}
	return cfg
}

func newRunner(interactive bool) *installer.Runner {
	r := installer.NewRunner(os.Stdout, os.Stderr)
	if interactive {
		r.OnFailure = installer.InteractiveOnFailure(r)
	}
	return r
}

// assumeYes reports whether automated (non-interactive) confirmation is
// requested via GENTOOINSTALL_ASSUME_YES=1. Every yes/no prompt answers with
// its intended default and the pre-apply countdown is skipped; used by the
// headless QEMU test harness.
func assumeYes() bool {
	return os.Getenv("GENTOOINSTALL_ASSUME_YES") == "1"
}

func buildLayoutE(cfg *config.Config) (*disklayout.Layout, error) {
	l, err := disklayout.BuildFromConfig(cfg, installer.UUIDStorageDir)
	if err != nil {
		return nil, fmt.Errorf("disk layout: %w", err)
	}
	if l.RootID == "" {
		return nil, errors.New("you must assign DISK_ID_ROOT (no root device in layout)")
	}
	if l.EFIID == "" && l.BIOSID == "" {
		return nil, errors.New("you must assign DISK_ID_EFI or DISK_ID_BIOS")
	}
	return l, nil
}

func buildLayout(cfg *config.Config) *disklayout.Layout {
	l, err := buildLayoutE(cfg)
	if err != nil {
		fatal("%v", err)
	}
	return l
}

func runInstall(cfgPath string) {
	requireRoot()
	cfg := loadConfigForInstall(cfgPath)

	if err := installer.EnsureTmpDirs(); err != nil {
		fatal("%v", err)
	}

	layout := buildLayout(cfg)
	resolver := &disklayout.Resolver{Layout: layout}
	nonInteractive := assumeYes()
	r := newRunner(!nonInteractive)
	r.NonInteractive = nonInteractive
	c := &installer.Context{
		R:            r,
		Cfg:          cfg,
		Layout:       layout,
		Resolver:     resolver,
		SourceConfig: cfgPath,
	}

	if err := installer.PrepareEnvironment(c); err != nil {
		fatal("%v", err)
	}
	if err := installer.EnsureEncryptionKey(c, os.Stdin); err != nil {
		fatal("%v", err)
	}

	summarizeAndConfirm(c)

	fmt.Println("[+] Applying disk configuration")
	if err := installer.ApplyDiskActions(c); err != nil {
		fatal("%v", err)
	}
	fmt.Println("[+] Disk configuration was applied successfully")

	stage3, err := installer.DownloadStage3(c)
	if err != nil {
		fatal("%v", err)
	}
	if err := installer.ExtractStage3(c, stage3); err != nil {
		fatal("%v", err)
	}

	if c.IsEFI() {
		if err := installer.MountEfiVars(c); err != nil {
			fatal("%v", err)
		}
	}

	if err := installer.PrepareChrootEnv(c, installer.RootMountpoint); err != nil {
		fatal("%v", err)
	}
	if err := installer.EnterChroot(c, installer.RootMountpoint); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			os.Exit(ee.ExitCode())
		}
		fatal("%v", err)
	}
}

func summarizeAndConfirm(c *installer.Context) {
	out, _ := c.R.QuietRun("lsblk")
	fmt.Println("[+] \x1b[1mCurrent lsblk output:\x1b[m")
	fmt.Println(out)
	fmt.Println()
	fmt.Println("[+] \x1b[1mConfigured disk layout:\x1b[m")
	fmt.Println(c.Layout.SummaryPlain())
	fmt.Println()

	if c.Layout.Flags.NoPartitioningOrFormatting {
		fmt.Println("[+] You have chosen an existing disk configuration. No devices will")
		fmt.Println("    actually be re-partitioned or formatted.")
	} else {
		fmt.Fprintln(os.Stderr, "[!] Please ensure that all selected devices are fully unmounted and are")
		fmt.Fprintln(os.Stderr, "    not otherwise in use by the system. This includes stopping mdadm arrays")
		fmt.Fprintln(os.Stderr, "    and closing opened luks volumes if applicable for all relevant devices.")
		fmt.Fprintln(os.Stderr, "    Otherwise, automatic partitioning may fail.")
	}

	ok, err := installer.AskYesNo(c.R,
		"Do you really want to apply this disk configuration?", assumeYes())
	if err != nil {
		fatal("%v", err)
	}
	if !ok {
		fatal("aborted")
	}
	if !assumeYes() {
		countdown(c, "Applying in ", 5)
	}
}

func countdown(c *installer.Context, msg string, n int) {
	fmt.Fprint(os.Stderr, msg)
	for i := n; i > 0; i-- {
		fmt.Fprintf(os.Stderr, "\x1b[1;31m%d\x1b[m ", i)
		time.Sleep(time.Second)
	}
	fmt.Fprintln(os.Stderr)
}

func runChroot(args []string) {
	requireRoot()
	if len(args) < 1 {
		fatal("usage: gentooinstall chroot DIR [CMD...]")
	}
	dir := args[0]
	if _, err := os.Stat(dir); err != nil {
		fatal("chroot directory not found: '%s'", dir)
	}
	if !installer.IsMountpoint(dir) {
		fatal("'%s' is not a mountpoint", dir)
	}

	cfg := &config.Config{} // minimal context; chroot shell needs no config
	_ = cfg
	c := &installer.Context{
		R:        newRunner(true),
		Resolver: &disklayout.Resolver{},
	}
	if err := installer.PrepareChrootEnv(c, dir); err != nil {
		fatal("%v", err)
	}
	if err := installer.ChrootShell(c, dir, args[1:]...); err != nil {
		fatal("%v", err)
	}
}

func runInChroot(cfgPath string) {
	requireRoot()
	cfg, _, err := config.LoadOrDefault(cfgPath, true)
	if err != nil {
		fatal("%v", err)
	}
	fillSystemDefaults(cfg)

	nonInteractive := os.Getenv(installer.NonInteractiveEnv) == "1"
	r := newRunner(!nonInteractive)
	r.NonInteractive = nonInteractive

	layout := buildLayout(cfg)
	resolver := &disklayout.Resolver{Layout: layout}
	if v := os.Getenv("GENTOO_CACHED_LSBLK"); v != "" {
		resolver.SetCachedLsblk(v)
	}

	c := &installer.Context{
		R:        r,
		Cfg:      cfg,
		Layout:   layout,
		Resolver: resolver,
		InChroot: true,
	}
	installer.SetupChrootEnv(c)

	if err := installer.MainInstallGentooInChroot(c); err != nil {
		fatal("%v", err)
	}
}

// tuiInstaller drives an installation from inside the live TUI. All
// output is streamed into the install window and every interactive
// prompt is answered through the window instead of the terminal.
type tuiInstaller struct {
	cfg    *config.Config
	path   string
	r      *installer.Runner
	decide chan tui.InstallDecision
}

// awaitDecision reports a failure to the install window and blocks
// until the user answers. Choosing Editor saves the (validated) config
// and keeps waiting so edits apply to subsequent steps.
func (t *tuiInstaller) awaitDecision(cmdline string, err error) tui.InstallDecision {
	for {
		tui.EmitInstallFailed(tui.InstallFailedMsg{
			Cmdline: cmdline,
			Err:     err.Error(),
			Decide:  func(d tui.InstallDecision) { t.decide <- d },
			Shell:   func() *exec.Cmd { return t.r.ShellCmd() },
		})
		switch d := <-t.decide; d {
		case tui.DecideRetry:
			return d
		case tui.DecideAbort:
			return d
		case tui.DecideEditor:
			if errs := t.cfg.Validate(); len(errs) > 0 {
				tui.EmitInstallLine("[!] Configuration is invalid; edits not saved.")
				continue
			}
			if e := t.cfg.Save(config.ResolveSavePath(t.path)); e != nil {
				tui.EmitInstallLine("[!] Could not save configuration: " + e.Error())
				continue
			}
			tui.EmitInstallLine("[+] Configuration saved; retry when ready.")
		default:
			return tui.DecideAbort
		}
	}
}

// logf emits a progress line through the runner's Log hook.
func (t *tuiInstaller) logf(format string, args ...any) {
	if t.r != nil && t.r.Log != nil {
		t.r.Log(format, args...)
	}
}

// newTUIRunner builds a non-interactive runner streaming everything
// into the TUI install window (nothing goes to the real terminal).
func (t *tuiInstaller) newRunner() *installer.Runner {
	out := installer.NewLineTee(nil, tui.EmitInstallLine)
	r := installer.NewRunner(out, out)
	r.NonInteractive = true
	r.OnFailure = func(cmdline string, err error) installer.FailAction {
		if t.awaitDecision(cmdline, err) == tui.DecideRetry {
			return installer.FailRetry
		}
		return installer.FailAbort
	}
	t.r = r
	return r
}

// runInstallTUI performs the host-side installation while the TUI stays
// alive, mirroring runInstall but without any terminal interactivity.
func runInstallTUI(cfg *config.Config, cfgPath string) error {
	if err := cfg.Save(config.ResolveSavePath(cfgPath)); err != nil {
		return fmt.Errorf("save config before install: %w", err)
	}
	if err := installer.EnsureTmpDirs(); err != nil {
		return err
	}
	layout, err := buildLayoutE(cfg)
	if err != nil {
		return err
	}

	t := &tuiInstaller{cfg: cfg, path: cfgPath,
		decide: make(chan tui.InstallDecision, 1)}
	r := t.newRunner()

	c := &installer.Context{
		R:            r,
		Cfg:          cfg,
		Layout:       layout,
		Resolver:     &disklayout.Resolver{Layout: layout},
		SourceConfig: cfgPath,
	}

	t.logf("Preparing installation environment")
	if err := installer.PrepareEnvironment(c); err != nil {
		return err
	}
	if layout.Flags.UsedEncryption && os.Getenv(installer.EncryptionKeyEnv) == "" {
		return fmt.Errorf("encryption enabled but %s is missing; restart the installation from the configurator",
			installer.EncryptionKeyEnv)
	}
	if err := installer.EnsureEncryptionKey(c, nil); err != nil {
		return err
	}

	out, _ := c.R.QuietRun("lsblk")
	t.logf("Current lsblk output:")
	t.logf("%s", out)
	t.logf("Configured disk layout:")
	t.logf("%s", c.Layout.SummaryPlain())

	for i := 5; i > 0; i-- {
		t.logf("Applying disk configuration in %d…", i)
		time.Sleep(time.Second)
	}

	t.logf("Applying disk configuration")
	if err := installer.ApplyDiskActions(c); err != nil {
		return err
	}
	t.logf("Disk configuration was applied successfully")

	stage3, err := installer.DownloadStage3(c)
	if err != nil {
		return err
	}
	if err := installer.ExtractStage3(c, stage3); err != nil {
		return err
	}

	if c.IsEFI() {
		if err := installer.MountEfiVars(c); err != nil {
			return err
		}
	}

	if err := installer.PrepareChrootEnv(c, installer.RootMountpoint); err != nil {
		return err
	}
	for {
		err := installer.EnterChroot(c, installer.RootMountpoint)
		if err == nil {
			return nil
		}
		if t.awaitDecision("gentooinstall --in-chroot (chroot phase)", err) == tui.DecideAbort {
			return fmt.Errorf("aborted after chroot phase failure: %w", err)
		}
		t.logf("Re-entering chroot phase…")
	}
}
