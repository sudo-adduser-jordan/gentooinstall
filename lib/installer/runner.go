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
	FailAbort
	FailContinue
	FailPrint
)

type CommandExecutor interface {
	Run(name string, args ...string) error
	QuietRun(name string, args ...string) (string, error)
	RunWithStdin(stdin, name string, args ...string) error
	Try(name string, args ...string) error
}

type Runner struct {
	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader

	Exec CommandExecutor

	Log func(format string, args ...any)

	OnFailure func(cmdline string, err error) FailAction

	Dir string

	NonInteractive bool

	LookPath func(name string) bool
}

const NonInteractiveEnv = "GENTOO_NONINTERACTIVE"

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

func (runner *Runner) log(format string, args ...any) { runner.logf(format, args...) }

func DefaultOnFailure(cmdline string, err error) FailAction { return FailAbort }

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
		return nil
	}
	if runner.Stdin != nil {
		return runner.Stdin
	}
	return os.Stdin
}

func (runner *Runner) Run(name string, args ...string) error {
	if runner.Exec != nil {
		return runner.Exec.Run(name, args...)
	}
	runner.logf("$ %s", CommandLine(name, args...))
	return runner.cmd(name, args, nil, true).Run()
}

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

func (runner *Runner) RunWithStdin(stdin string, name string, args ...string) error {
	if runner.Exec != nil {
		return runner.Exec.RunWithStdin(stdin, name, args...)
	}
	runner.logf("$ %s  (stdin)", CommandLine(name, args...))
	return runner.cmd(name, args, strings.NewReader(stdin), true).Run()
}

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
		default:
			return fmt.Errorf("command failed: %s: %w", line, err)
		}
	}
}

func HasProgram(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func (runner *Runner) HasProgram(name string) bool {
	if runner.LookPath != nil {
		return runner.LookPath(name)
	}
	return HasProgram(name)
}
