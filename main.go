package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"gentooinstall/lib/cli"
	"gentooinstall/lib/config"
	"gentooinstall/lib/disklayout"
	"gentooinstall/lib/installer"
	"gentooinstall/lib/live"
	"gentooinstall/lib/sysinfo"
	"gentooinstall/lib/tui"
)

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "\x1b[1;31merror:\x1b[m "+format+"\n", args...)
	os.Exit(1)
}

func requireRoot() {
	if os.Geteuid() != 0 {
		fatal("must be root")
	}
}

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
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	return dir
}

var VERSION = "0.1.0"

const USAGE = `gentooinstall — gentoo installer (Go/Charm edition)

Usage:
  gentooinstall                    Open the interactive configurator (builds/custom.toml, or
                                   builds/default.toml until the first save; without a builds/
                                   directory it uses $XDG_CONFIG_HOME/gentooinstall/custom.toml).
  gentooinstall [config.toml]      Open the interactive configurator for the given file.
  gentooinstall install [CONFIG]   Run the installation as configured, streaming raw
                                   output to the terminal (no TUI). CONFIG defaults to
                                   builds/custom.toml, falling back to builds/default.toml.
                                   Example: gentooinstall install builds/desktop-systemd.toml
  gentooinstall demo [OUT]         Record the simulated install demo into the given file
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
desktop-systemd, bios, btrfs-efi, existing-efi, ...). The installer performs
partitioning (destructive!), downloads and verifies a stage3 tarball and
completes configuration inside a chroot.`

func main() {

	args := os.Args[1:]
	parsed, err := cli.ParseArgs(args)

	if err != nil {
		fatal("%v", err)
	}
	if parsed.ShowHelp {
		fmt.Println(USAGE)
		return
	}
	if parsed.ShowVersion {
		fmt.Println("gentooinstall", VERSION)
		return
	}

	cfgPath := parsed.CfgPath
	mode := parsed.Mode
	rest := parsed.Rest

	if mode == "demo" {
		runDemo(rest)
		return
	}
	if cfgPath == "" {
		cfgPath = defaultConfigPath()
	}
	cfgAbs, err := filepath.Abs(cfgPath)
	if err != nil {
		fatal("%v", err)
	}

	if os.Getpid() == 1 {
		msg := fmt.Sprintf("gentooinstall init: PID 1, version %s\n", VERSION)
		fmt.Print(msg)
		printHeadlessBanner(msg)
		if err := live.Init(); err != nil {
			fmt.Fprintf(os.Stderr, "live init: %v\n", err)
		}
		if cfg, key := headlessInstallRequest(); cfg != "" {
			runHeadlessInstall(cfg, key)
		}
		go pingSelectedMirror()
	}

	switch mode {
	case "":
		runTUI(cfgAbs, parsed.Demo)
	case "install":
		runInstall(cfgAbs)
	case "chroot":
		runChroot(rest)
	case "in-chroot":
		runInChroot(cfgAbs)
	}
}

func headlessInstallRequest() (cfg, key string) {
	data, err := os.ReadFile("/proc/cmdline")
	if err != nil {
		return "", ""
	}
	for _, field := range strings.Fields(string(data)) {
		switch {
		case strings.HasPrefix(field, "gentooinstall.install="):
			cfg = strings.TrimPrefix(field, "gentooinstall.install=")
		case strings.HasPrefix(field, "gentooinstall.install-key="):
			key = strings.TrimPrefix(field, "gentooinstall.install-key=")
		}
	}
	return cfg, key
}

func runHeadlessInstall(cfg, key string) {
	if !filepath.IsAbs(cfg) {
		cfg = filepath.Join("/builds", cfg)
	}
	report := func(status string) {
		msg := "gentooinstall install: " + status + "\n"
		fmt.Print(msg)
		printHeadlessBanner(msg)
		powerOff()
		os.Exit(0)
	}
	if _, err := os.Stat(cfg); err != nil {
		report("failed (" + cfg + " not found)")
	}
	cmd := exec.Command("/proc/self/exe", "install", cfg)
	cmd.Env = append(os.Environ(),
		"GENTOOINSTALL_ASSUME_YES=1",
		"GENTOO_NONINTERACTIVE=1")
	if key != "" {
		cmd.Env = append(cmd.Env, "GENTOO_INSTALL_ENCRYPTION_KEY="+key)
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			report(fmt.Sprintf("failed (exit %d)", ee.ExitCode()))
		}
		report("failed (" + err.Error() + ")")
	}
	report("success")
}

func powerOff() {
	if err := exec.Command("poweroff", "-f").Run(); err == nil {
		return
	}
	time.Sleep(time.Second)
	os.Exit(0)
}

func printHeadlessBanner(msg string) {
	if live.TuiActive() {
		return
	}
	const path = "/dev/ttyS0"
	if _, err := os.Stat(path); err != nil {
		dev := uint32(4)<<8 | 64
		if err := syscall.Mknod(path, syscall.S_IFCHR|0o600, int(dev)); err != nil {
			return
		}
	}
	if file, err := os.OpenFile(path, os.O_WRONLY, 0); err == nil {
		_, _ = file.WriteString(msg)
		_ = file.Close()
	}
}

func pingSelectedMirror() {
	if os.Getpid() != 1 {
		return
	}
	mirror := config.Default(false).Gentoo.Mirror
	for attempt := 0; attempt < 6; attempt++ {
		if live.NetworkReady() {
			break
		}
		time.Sleep(2 * time.Second)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	host := sysinfo.MirrorHost(mirror)
	st := sysinfo.MirrorProbe(ctx, mirror)
	printHeadlessBanner(fmt.Sprintf("live: mirror %s: %s\n", host, noteToSerial(st)))
}

func noteToSerial(st sysinfo.MirrorStatus) string {
	if st.OK {
		return "ok"
	}
	if st.Note == "" {
		return "fail: unreachable"
	}
	return "fail: " + st.Note
}

func runDemo(args []string) {
	out := "demo.gif"
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "-o" || arg == "--output":
			if index+1 >= len(args) {
				fatal("--output requires a path")
			}
			index++
			out = args[index]
		case !strings.HasPrefix(arg, "-"):
			out = arg
		default:
			fatal("invalid argument '%s'", arg)
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

	if _, err := tape.WriteString(demoTape(out)); err != nil {
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

func demoTape(out string) string {
	binPath := argv0()
	return strings.Join([]string{
		`Output "` + out + `"`,
		"Set Width 2000",
		"Set Height 700",
		"Set FontSize 16",
		`Set FontFamily "Noto Color Emoji, JetBrains Mono, DejaVu Sans Mono, Noto Sans Mono, monospace"`,
		"Set Padding 12",
		"Set WaitTimeout 30s",
		"",
		`Type "` + binPath + ` --demo"`,
		"Enter",
		"Sleep 1.2s",
		"",
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
		"Type \"6\"",
		"Sleep 400ms",
		"Type \"d\"",
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

func argv0() string {
	if exe, err := os.Executable(); err == nil {
		if abs, err := filepath.Abs(exe); err == nil {
			return abs
		}
		return exe
	}
	return "gentooinstall"
}

func runTUI(cfgPath string, demo bool) {
	if lipgloss.ColorProfile() == termenv.Ascii {
		lipgloss.SetColorProfile(termenv.TrueColor)
	}

	cfg, _, err := config.LoadOrDefault(cfgPath, sysinfo.HasEFI())
	if err != nil {
		fatal("%v", err)
	}
	fillSystemDefaults(cfg)

	model := tui.New(cfg, cfgPath)
	model.SetInstallFunc(func() error {
		return runInstallTUI(cfg, cfgPath)
	})
	model.SetRoot(os.Geteuid() == 0 || demo)
	model.SetPrereq(func() tui.Prereq {
		runner := installer.NewRunner(io.Discard, io.Discard)
		ctx := &installer.Context{Runner: runner}
		if layout, err := disklayout.BuildFromConfig(cfg, ""); err == nil {
			ctx.Layout = layout
		}
		return tui.Prereq{
			RootOK:          os.Geteuid() == 0,
			MissingPrograms: installer.MissingPrograms(ctx),
		}
	})

	if cols, rows, ok := live.DetectWinsize(); ok {
		live.ApplyWinsize(cols, rows)
	}
	opts := []tea.ProgramOption{tea.WithAltScreen()}
	program := tea.NewProgram(model, opts...)
	tui.SetProgram(program)
	if os.Getpid() == 1 {
		printHeadlessBanner("live: tui starting\n")
	}
	live.SetTuiActive(true)
	_, runErr := program.Run()
	live.SetTuiActive(false)
	if runErr != nil {
		printHeadlessBanner(fmt.Sprintf("tui: error: %v\n", runErr))
		fatal("tui: %v", runErr)
	}
}

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
		for _, verr := range errs {
			fmt.Fprintln(os.Stderr, " - "+verr.Error())
		}
		fatal("configuration is invalid")
	}
	return cfg
}

func newRunner(interactive bool) *installer.Runner {
	runner := installer.NewRunner(os.Stdout, os.Stderr)
	if interactive {
		runner.OnFailure = installer.InteractiveOnFailure(runner)
	}
	return runner
}

func assumeYes() bool {
	return os.Getenv("GENTOOINSTALL_ASSUME_YES") == "1"
}

func buildLayoutE(cfg *config.Config) (*disklayout.Layout, error) {
	layout, err := disklayout.BuildFromConfig(cfg, installer.UUIDStorageDir)
	if err != nil {
		return nil, fmt.Errorf("disk layout: %w", err)
	}
	if layout.RootID == "" {
		return nil, errors.New("you must assign DISK_ID_ROOT (no root device in layout)")
	}
	if layout.EFIID == "" && layout.BIOSID == "" {
		return nil, errors.New("you must assign DISK_ID_EFI or DISK_ID_BIOS")
	}
	if err := disklayout.CheckBootTypeConsistency(cfg, layout); err != nil {
		return nil, err
	}
	return layout, nil
}

func buildLayout(cfg *config.Config) *disklayout.Layout {
	layout, err := buildLayoutE(cfg)
	if err != nil {
		fatal("%v", err)
	}
	return layout
}

func unmountStale(ctx *installer.Context, dir string) {
	prev := ctx.Runner.OnFailure
	ctx.Runner.OnFailure = installer.DefaultOnFailure
	defer func() { ctx.Runner.OnFailure = prev }()
	if err := installer.UnmountChroot(ctx, dir); err != nil {
		msg := fmt.Sprintf("[!] warning: could not unmount stale filesystems: %v", err)
		if ctx.Runner.Log != nil {
			ctx.Runner.Log("%s", msg)
		} else {
			fmt.Fprintln(os.Stderr, msg)
		}
	}
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
	runner := newRunner(!nonInteractive)
	runner.NonInteractive = nonInteractive
	if file, err := installer.OpenInstallLog(); err == nil {
		runner.Stdout = io.MultiWriter(runner.Stdout, file)
		runner.Stderr = io.MultiWriter(runner.Stderr, file)
		defer file.Close()
	}
	ctx := &installer.Context{
		Runner:       runner,
		Cfg:          cfg,
		Layout:       layout,
		Resolver:     resolver,
		SourceConfig: cfgPath,
	}

	unmountStale(ctx, installer.RootMountpoint)

	fmt.Printf("[+] Live firmware: %s; configured boot type: %s (EFIID=%s BIOSID=%s)\n",
		ctx.HostBootMode(), ctx.Cfg.Disk.BootType, ctx.Layout.EFIID, ctx.Layout.BIOSID)
	if err := installer.CheckHostBootMode(ctx); err != nil {
		fatal("%v", err)
	}
	if err := installer.CheckFilesystemSupport(ctx); err != nil {
		fatal("%v", err)
	}
	if missing := installer.MissingPrograms(ctx); len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "[!] Missing required programs: %s\n", strings.Join(missing, " "))
		ok, err := installer.AskYesNo(ctx.Runner, "Install them now via apk?", true)
		if err != nil {
			fatal("%v", err)
		}
		if ok {
			if err := installer.InstallMissingPrograms(ctx); err != nil {
				fatal("%v", err)
			}
			fmt.Println("[+] All required programs are present")
		} else {
			fatal("missing required programs: %s", strings.Join(missing, " "))
		}
	}
	if err := installer.PrepareEnvironment(ctx); err != nil {
		fatal("%v", err)
	}
	if err := installer.EnsureEncryptionKey(ctx, os.Stdin); err != nil {
		fatal("%v", err)
	}

	summarizeAndConfirm(ctx)

	fmt.Println("[+] Applying disk configuration")
	if err := installer.ApplyDiskActions(ctx); err != nil {
		fatal("%v", err)
	}
	fmt.Println("[+] Disk configuration was applied successfully")

	fmt.Println("[+] Mounting root filesystem")
	if err := installer.MountRoot(ctx); err != nil {
		fatal("%v", err)
	}
	stage3, err := installer.DownloadStage3(ctx)
	if err != nil {
		fatal("%v", err)
	}
	if err := installer.ExtractStage3(ctx, stage3); err != nil {
		fatal("%v", err)
	}

	if ctx.IsEFI() {
		if err := installer.MountEfiVars(ctx); err != nil {
			fatal("%v", err)
		}
	}

	if err := installer.PrepareChrootEnv(ctx, installer.RootMountpoint); err != nil {
		installer.UnmountChroot(ctx, installer.RootMountpoint)
		fatal("%v", err)
	}
	if err := installer.EnterChroot(ctx, installer.RootMountpoint); err != nil {
		installer.UnmountChroot(ctx, installer.RootMountpoint)
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(ee.ExitCode())
		}
		fatal("%v", err)
	}
}

func summarizeAndConfirm(ctx *installer.Context) {
	out, _ := ctx.Runner.QuietRun("lsblk")
	fmt.Println("[+] \x1b[1mCurrent lsblk output:\x1b[m")
	fmt.Println(out)
	fmt.Println()
	fmt.Printf("[+] Live firmware: %s; configured boot type: %s (EFIID=%s BIOSID=%s)\n",
		ctx.HostBootMode(), ctx.Cfg.Disk.BootType, ctx.Layout.EFIID, ctx.Layout.BIOSID)
	fmt.Println("[+] \x1b[1mConfigured disk layout:\x1b[m")
	fmt.Println(ctx.Layout.SummaryPlain())
	fmt.Println()

	if ctx.Layout.Flags.NoPartitioningOrFormatting {
		fmt.Println("[+] You have chosen an existing disk configuration. No devices will")
		fmt.Println("    actually be re-partitioned or formatted.")
	} else {
		fmt.Fprintln(os.Stderr, "[!] All filesystems and swap on the selected devices will be released")
		fmt.Fprintln(os.Stderr, "    (unmounted / swapoff'd) automatically before partitioning. Please")
		fmt.Fprintln(os.Stderr, "    ensure the devices are otherwise not in use (e.g. stop mdadm arrays")
		fmt.Fprintln(os.Stderr, "    and close opened luks volumes if applicable). Otherwise, automatic")
		fmt.Fprintln(os.Stderr, "    partitioning may fail.")
	}

	ok, err := installer.AskYesNo(ctx.Runner,
		"Do you really want to apply this disk configuration?", assumeYes())
	if err != nil {
		fatal("%v", err)
	}
	if !ok {
		fatal("aborted")
	}
	if !assumeYes() {
		countdown(ctx, "Applying in ", 5)
	}
}

func countdown(ctx *installer.Context, msg string, seconds int) {
	fmt.Fprint(os.Stderr, msg)
	for remaining := seconds; remaining > 0; remaining-- {
		fmt.Fprintf(os.Stderr, "\x1b[1;31m%d\x1b[m ", remaining)
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

	cfg := &config.Config{}
	_ = cfg
	ctx := &installer.Context{
		Runner:   newRunner(true),
		Resolver: &disklayout.Resolver{},
	}
	if err := installer.PrepareChrootEnv(ctx, dir); err != nil {
		fatal("%v", err)
	}
	if err := installer.ChrootShell(ctx, dir, args[1:]...); err != nil {
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
	runner := newRunner(!nonInteractive)
	runner.NonInteractive = nonInteractive

	layout := buildLayout(cfg)
	resolver := &disklayout.Resolver{Layout: layout}
	if value := os.Getenv("GENTOO_CACHED_LSBLK"); value != "" {
		resolver.SetCachedLsblk(value)
	}

	ctx := &installer.Context{
		Runner:   runner,
		Cfg:      cfg,
		Layout:   layout,
		Resolver: resolver,
		InChroot: true,
	}
	installer.SetupChrootEnv(ctx)

	if err := installer.MainInstallGentooInChroot(ctx); err != nil {
		fatal("%v", err)
	}
}

type tuiInstaller struct {
	cfg    *config.Config
	path   string
	runner *installer.Runner
	decide chan tui.InstallDecision

	logFile io.Closer

	decided bool
}

func (inst *tuiInstaller) awaitDecision(cmdline string, err error) tui.InstallDecision {
	return inst.awaitDecisionCore(cmdline, err)
}

func (inst *tuiInstaller) awaitPhaseDecision(cmdline string, err error) tui.InstallDecision {
	return inst.awaitDecisionCore(cmdline, err)
}

func (inst *tuiInstaller) ensureHostPrograms(ctx *installer.Context) error {
	for {
		missing := installer.MissingPrograms(ctx)
		if len(missing) == 0 {
			return nil
		}
		cmdline := installer.InstallProgramsCmdline(missing)
		errMsg := fmt.Sprintf("The live system is missing required programs: %s. "+
			"Choose Retry to install them now (needs network), or Abort to stop the installation.",
			strings.Join(missing, " "))
		switch inst.awaitDecisionCore(cmdline, errors.New(errMsg)) {
		case tui.DecideAbort:
			return fmt.Errorf("missing required programs: %s", strings.Join(missing, " "))
		default:
			if err := installer.InstallMissingPrograms(ctx); err != nil {
				return err
			}
		}
	}
}

func (inst *tuiInstaller) awaitDecisionCore(cmdline string, err error) tui.InstallDecision {
	inst.decided = true
	for {
		tui.EmitInstallFailed(tui.InstallFailedMsg{
			Cmdline: cmdline,
			Err:     err.Error(),
			Decide:  func(decision tui.InstallDecision) { inst.decide <- decision },
		})
		switch decision := <-inst.decide; decision {
		case tui.DecideRetry, tui.DecideAbort:
			return decision
		default:
			return tui.DecideAbort
		}
	}
}

func (inst *tuiInstaller) logf(format string, args ...any) {
	if inst.runner != nil && inst.runner.Log != nil {
		inst.runner.Log(format, args...)
	}
}

func (inst *tuiInstaller) newRunner() *installer.Runner {
	var sink io.Writer
	if file, err := installer.OpenInstallLog(); err == nil {
		inst.logFile = file
		sink = file
	}
	out := installer.NewLineTee(sink, tui.EmitInstallLine)
	runner := installer.NewRunner(out, out)
	runner.NonInteractive = true
	runner.OnFailure = func(cmdline string, err error) installer.FailAction {
		if inst.awaitDecision(cmdline, err) == tui.DecideRetry {
			inst.decided = false
			return installer.FailRetry
		}
		return installer.FailAbort
	}
	inst.runner = runner
	return runner
}

func runInstallTUI(cfg *config.Config, cfgPath string) error {
	resolved := config.ResolveSavePath(cfgPath)
	if err := cfg.Save(resolved); err != nil {
		return fmt.Errorf("save config before install: %w", err)
	}
	if err := installer.EnsureTmpDirs(); err != nil {
		return err
	}
	layout, err := buildLayoutE(cfg)
	if err != nil {
		return err
	}

	inst := &tuiInstaller{cfg: cfg, path: cfgPath,
		decide: make(chan tui.InstallDecision, 1)}
	runner := inst.newRunner()
	defer func() {
		if inst.logFile != nil {
			_ = inst.logFile.Close()
			inst.logFile = nil
		}
	}()

	ctx := &installer.Context{
		Runner:       runner,
		Cfg:          cfg,
		Layout:       layout,
		Resolver:     &disklayout.Resolver{Layout: layout},
		SourceConfig: resolved,
	}

	inst.logf("Live firmware: %s; configured boot type: %s (EFIID=%s BIOSID=%s)",
		ctx.HostBootMode(), ctx.Cfg.Disk.BootType, ctx.Layout.EFIID, ctx.Layout.BIOSID)
	if err := installer.CheckHostBootMode(ctx); err != nil {
		return err
	}
	if err := installer.CheckFilesystemSupport(ctx); err != nil {
		return err
	}

	inst.logf("Checking required programs")
	if err := inst.ensureHostPrograms(ctx); err != nil {
		return err
	}
	inst.logf("Preparing installation environment")
	inst.logf("Configured disk layout:")
	inst.logf("%s", ctx.Layout.SummaryPlain())
	if err := installer.PrepareEnvironment(ctx); err != nil {
		return err
	}
	if layout.Flags.UsedEncryption && os.Getenv(installer.EncryptionKeyEnv) == "" {
		return fmt.Errorf("encryption enabled but %s is missing; restart the installation from the configurator",
			installer.EncryptionKeyEnv)
	}
	if err := installer.EnsureEncryptionKey(ctx, nil); err != nil {
		return err
	}

	out, _ := ctx.Runner.QuietRun("lsblk")
	inst.logf("Current lsblk output:")
	inst.logf("%s", out)
	inst.logf("Configured disk layout:")
	inst.logf("%s", ctx.Layout.SummaryPlain())

	for remaining := 5; remaining > 0; remaining-- {
		inst.logf("Applying disk configuration in %d…", remaining)
		time.Sleep(time.Second)
	}

	phases := []struct {
		name      string
		cleanRoot bool
		fn        func() error
	}{
		{"Unmounting stale filesystems", false,
			func() error { unmountStale(ctx, installer.RootMountpoint); return nil }},
		{"Applying disk configuration", false,
			func() error { return installer.ApplyDiskActions(ctx) }},
		{"Mounting root filesystem", false,
			func() error { return installer.MountRoot(ctx) }},
		{"Downloading stage3", false,
			func() error {
				_, err := installer.DownloadStage3(ctx)
				return err
			}},
		{"Extracting stage3", true,
			func() error {
				return installer.ExtractStage3(ctx,
					installer.Stage3Info{Path: ctx.Stage3File})
			}},
		{"Mounting efivars", false,
			func() error { return installer.MountEfiVars(ctx) }},
		{"Preparing chroot environment", false,
			func() error { return installer.PrepareChrootEnv(ctx, installer.RootMountpoint) }},
	}
	for _, phase := range phases {
		if phase.name == "Mounting efivars" && !ctx.IsEFI() {
			continue
		}
		if err := inst.runPhase(ctx, phase.name, phase.cleanRoot, phase.fn); err != nil {
			return err
		}
	}
	for {
		err := installer.EnterChroot(ctx, installer.RootMountpoint)
		if err == nil {
			return nil
		}
		switch inst.awaitPhaseDecision("gentooinstall --in-chroot (chroot phase)", err) {
		case tui.DecideRetry:
			tui.EmitInstallLine("Re-entering chroot phase…")
		default:
			return fmt.Errorf("aborted after chroot phase failure: %w", err)
		}
	}
}

func (inst *tuiInstaller) runPhase(ctx *installer.Context, name string, cleanRoot bool, fn func() error) error {
	inst.decided = false
	inst.logf(name)
	for {
		err := fn()
		if err == nil {
			inst.decided = false
			return nil
		}
		if inst.decided {
			inst.decided = false
			return fmt.Errorf("%s failed: %w", name, err)
		}
		switch inst.awaitPhaseDecision(name, err) {
		case tui.DecideRetry:
			inst.decided = false
			if fresh, ferr := buildLayoutE(ctx.Cfg); ferr != nil {
				return fmt.Errorf("%s: refreshed layout invalid, abort instead of retrying stale layout: %w", name, ferr)
			} else {
				ctx.Layout = fresh
				if ctx.Resolver != nil {
					ctx.Resolver.Layout = fresh
				} else {
					ctx.Resolver = &disklayout.Resolver{Layout: fresh}
				}
				inst.logf("Refreshed layout: boot type %s (EFIID=%s BIOSID=%s)",
					ctx.Cfg.Disk.BootType, fresh.EFIID, fresh.BIOSID)
			}
			if name == "Mounting efivars" && !ctx.IsEFI() {
				tui.EmitInstallLine("Skipping Mounting efivars (BIOS layout) …")
				return nil
			}
			if cleanRoot {
				if ce := installer.ClearRoot(ctx); ce != nil {
					return fmt.Errorf("%s: could not clean root: %w", name, ce)
				}
			}
			tui.EmitInstallLine("Retrying " + name + " …")
		case tui.DecideAbort:
			return fmt.Errorf("%s failed: %w", name, err)
		default:
			return fmt.Errorf("%s failed: %w", name, err)
		}
	}
}
