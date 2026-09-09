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

// FailAction is the user's answer to a failed command when the runner
// runs interactively.
type FailAction int

const (
	// FailRetry reruns the failed command.
	FailRetry FailAction = iota
	// FailAbort aborts the installation.
	FailAbort
	// FailContinue ignores the failure and continues.
	FailContinue
	// FailPrint prints the full output and re-prompts.
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

func (runner *Runner) logf(format string, args ...any) {
	if runner.Log != nil {
		runner.Log(format, args...)
	}
}

// log is an alias used across the package.
func (runner *Runner) log(format string, args ...any) { runner.logf(format, args...) }

// DefaultOnFailure aborts unconditionally (non-interactive mode).
func DefaultOnFailure(cmdline string, err error) FailAction { return FailAbort }

// CommandLine renders a command for display.
func CommandLine(name string, args ...string) string {
	return strings.TrimSpace(name + " " + strings.Join(args, " "))
}

func (runner *Runner) cmd(name string, args []string, stdin io.Reader, stream bool) *exec.Cmd {
	cmd := exec.Command(name, args...)
	if runner.Dir != "" {
		cmd.Dir = runner.Dir
	}
	if stream {
		cmd.Stdout = runner.stdout()
		cmd.Stderr = runner.stderr()
	}
	cmd.Stdin = runner.stdinOr(stdin)
	return cmd
}

func (runner *Runner) stdout() io.Writer {
	if runner.Stdout != nil {
		return runner.Stdout
	}
	return os.Stdout
}

func (runner *Runner) stderr() io.Writer {
	if runner.Stderr != nil {
		return runner.Stderr
	}
	return os.Stderr
}

func (runner *Runner) stdinOr(fallback io.Reader) io.Reader {
	if fallback != nil {
		return fallback
	}
	if runner.NonInteractive {
		return nil // exec.Cmd: null device
	}
	if runner.Stdin != nil {
		return runner.Stdin
	}
	return os.Stdin
}

// Run executes a command, streaming its output to the runner's writers.
func (runner *Runner) Run(name string, args ...string) error {
	if runner.Exec != nil {
		return runner.Exec.Run(name, args...)
	}
	runner.logf("$ %s", CommandLine(name, args...))
	return runner.cmd(name, args, nil, true).Run()
}

// QuietRun captures output without streaming it.
func (runner *Runner) QuietRun(name string, args ...string) (string, error) {
	if runner.Exec != nil {
		return runner.Exec.QuietRun(name, args...)
	}
	runner.logf("$ %s", CommandLine(name, args...))
	var buf bytes.Buffer
	cmd := runner.cmd(name, args, nil, false)
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return strings.TrimRight(buf.String(), "\n"), err
}

// RunWithStdin feeds stdin to the command.
func (runner *Runner) RunWithStdin(stdin string, name string, args ...string) error {
	if runner.Exec != nil {
		return runner.Exec.RunWithStdin(stdin, name, args...)
	}
	runner.logf("$ %s  (stdin)", CommandLine(name, args...))
	return runner.cmd(name, args, strings.NewReader(stdin), true).Run()
}

// Try runs a command with interactive failure handling (port of try()).
func (runner *Runner) Try(name string, args ...string) error {
	if runner.Exec != nil {
		return runner.Exec.Try(name, args...)
	}
	line := CommandLine(name, args...)
	for {
		runner.logf("$ %s", line)
		cmd := runner.cmd(name, args, nil, true)
		err := cmd.Run()
		if err == nil {
			return nil
		}

		fmt.Fprintf(runner.stderr(), " * Command failed: \x1b[1;33m$\x1b[m %s\n", line)
		var code any = "?"
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
		fmt.Fprintln(runner.stderr(), "Last command failed with exit code", code)

		onFail := runner.OnFailure
		if onFail == nil {
			onFail = DefaultOnFailure
		}
	prompt:
		switch onFail(line, err) {
		case FailRetry:
			continue
		case FailPrint:
			fmt.Fprintf(runner.stderr(), "\x1b[1;33m$\x1b[m %s\n", line)
			goto prompt
		case FailContinue:
			return nil
		default: // FailAbort
			return fmt.Errorf("command failed: %s: %w", line, err)
		}
	}
}

// HasProgram reports whether an executable is on PATH.
func HasProgram(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// HasProgram reports whether a program is available, using LookPath when
// set (so tests can control host-dependent availability) and otherwise
// falling back to the package-level HasProgram.
func (runner *Runner) HasProgram(name string) bool {
	if runner.LookPath != nil {
		return runner.LookPath(name)
	}
	return HasProgram(name)
}
