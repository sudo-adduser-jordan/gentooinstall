// Package installer implements the installation engine: environment
// preparation, disk application, stage3 handling and the chroot phases.
package installer

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

type FailAction int

const (
	FailRetry FailAction = iota
	FailShell
	FailAbort
	FailContinue
	FailPrint
)

// CommandExecutor abstracts external command execution so tests can capture
// the exact command sequences the installer would invoke without running
// anything. The concrete *Runner implements it; tests replace it with a
// recording stub. A nil Exec (the default) means commands really run.
type CommandExecutor interface {
	Run(name string, args ...string) error
	QuietRun(name string, args ...string) (string, error)
	RunWithStdin(stdin, name string, args ...string) error
	Try(name string, args ...string) error
}

// Runner executes external commands with logging and optional
// interactive failure handling.
type Runner struct {
	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader

	// Exec, when non-nil, receives every command invocation instead of the
	// real execution path. Used by tests to record/assert command sequences.
	Exec CommandExecutor

	// Log receives human readable progress lines.
	Log func(format string, args ...any)

	// OnFailure is consulted whenever a Try'd command fails.
	// Returning FailAbort aborts the installation.
	OnFailure func(cmdline string, err error) FailAction

	// Dir sets the working directory for subsequent commands.
	Dir string

	// NonInteractive disables stdin prompts: AskYesNo answers with its
	// default and spawned commands read from the null device instead of
	// the terminal. Used when the installer is driven by the TUI or
	// re-executed inside the chroot.
	NonInteractive bool

	// LookPath, when non-nil, is used by HasProgram instead of the
	// package-level exec.LookPath lookup. Tests set it to control which
	// external tools are reported as available without touching PATH.
	LookPath func(name string) bool
}

// NonInteractiveEnv marks a gentooinstall process as non-interactive; it is set
// by EnterChroot when the parent runner runs without a terminal.
const NonInteractiveEnv = "GENTOO_NONINTERACTIVE"

// NewRunner builds a runner writing to stdout/stderr.
func NewRunner(stdout, stderr io.Writer) *Runner {
	return &Runner{
		Stdout: stdout,
		Stderr: stderr,
		Log: func(format string, args ...any) {
			fmt.Fprintf(stdout, "[+] "+format+"\n", args...)
		},
	}
}

func (r *Runner) logf(format string, args ...any) {
	if r.Log != nil {
		r.Log(format, args...)
	}
}

// log is an alias used across the package.
func (r *Runner) log(format string, args ...any) { r.logf(format, args...) }

// DefaultOnFailure aborts unconditionally (non-interactive mode).
func DefaultOnFailure(cmdline string, err error) FailAction { return FailAbort }

// CommandLine renders a command for display.
func CommandLine(name string, args ...string) string {
	return strings.TrimSpace(name + " " + strings.Join(args, " "))
}

func (r *Runner) cmd(name string, args []string, stdin io.Reader, stream bool) *exec.Cmd {
	cmd := exec.Command(name, args...)
	if r.Dir != "" {
		cmd.Dir = r.Dir
	}
	if stream {
		cmd.Stdout = r.stdout()
		cmd.Stderr = r.stderr()
	}
	cmd.Stdin = r.stdinOr(stdin)
	return cmd
}

func (r *Runner) stdout() io.Writer {
	if r.Stdout != nil {
		return r.Stdout
	}
	return os.Stdout
}

func (r *Runner) stderr() io.Writer {
	if r.Stderr != nil {
		return r.Stderr
	}
	return os.Stderr
}

func (r *Runner) stdinOr(fallback io.Reader) io.Reader {
	if fallback != nil {
		return fallback
	}
	if r.NonInteractive {
		return nil // exec.Cmd: null device
	}
	if r.Stdin != nil {
		return r.Stdin
	}
	return os.Stdin
}

// Run executes a command, streaming its output to the runner's writers.
func (r *Runner) Run(name string, args ...string) error {
	if r.Exec != nil {
		return r.Exec.Run(name, args...)
	}
	r.logf("$ %s", CommandLine(name, args...))
	return r.cmd(name, args, nil, true).Run()
}

// QuietRun captures output without streaming it.
func (r *Runner) QuietRun(name string, args ...string) (string, error) {
	if r.Exec != nil {
		return r.Exec.QuietRun(name, args...)
	}
	r.logf("$ %s", CommandLine(name, args...))
	var buf bytes.Buffer
	cmd := r.cmd(name, args, nil, false)
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return strings.TrimRight(buf.String(), "\n"), err
}

// RunWithStdin feeds stdin to the command.
func (r *Runner) RunWithStdin(stdin string, name string, args ...string) error {
	if r.Exec != nil {
		return r.Exec.RunWithStdin(stdin, name, args...)
	}
	r.logf("$ %s  (stdin)", CommandLine(name, args...))
	return r.cmd(name, args, strings.NewReader(stdin), true).Run()
}

// Try runs a command with interactive failure handling (port of try()).
func (r *Runner) Try(name string, args ...string) error {
	if r.Exec != nil {
		return r.Exec.Try(name, args...)
	}
	line := CommandLine(name, args...)
	for {
		r.logf("$ %s", line)
		cmd := r.cmd(name, args, nil, true)
		err := cmd.Run()
		if err == nil {
			return nil
		}

		fmt.Fprintf(r.stderr(), " * Command failed: \x1b[1;33m$\x1b[m %s\n", line)
		var code any = "?"
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
		fmt.Fprintln(r.stderr(), "Last command failed with exit code", code)

		onFail := r.OnFailure
		if onFail == nil {
			onFail = DefaultOnFailure
		}
	prompt:
		switch onFail(line, err) {
		case FailRetry:
			continue
		case FailShell:
			_ = r.SpawnShell()
			goto prompt
		case FailPrint:
			fmt.Fprintf(r.stderr(), "\x1b[1;33m$\x1b[m %s\n", line)
			goto prompt
		case FailContinue:
			return nil
		default: // FailAbort
			return fmt.Errorf("command failed: %s: %w", line, err)
		}
	}
}

// ShellCmd builds the emergency-shell command (not yet started).
func (r *Runner) ShellCmd() *exec.Cmd {
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = "/bin/bash"
	}
	cmd := exec.Command(sh)
	cmd.Env = append(os.Environ(), "PS1=(gentooinstall emergency) \\w \\$ ")
	return cmd
}

// SpawnShell drops the user into an interactive shell.
func (r *Runner) SpawnShell() error {
	fmt.Fprintln(r.stderr(), "You will be prompted for action again after exiting this shell.")
	cmd := r.ShellCmd()
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// HasProgram reports whether an executable is on PATH.
func HasProgram(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// HasProgram reports whether a program is available, using LookPath when
// set (so tests can control host-dependent availability) and otherwise
// falling back to the package-level HasProgram.
func (r *Runner) HasProgram(name string) bool {
	if r.LookPath != nil {
		return r.LookPath(name)
	}
	return HasProgram(name)
}
