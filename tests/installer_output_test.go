package tests

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"gentooinstall/internal/installer"
)

func TestLineTeeSplitsLines(t *testing.T) {
	var sink bytes.Buffer
	var lines []string
	w := installer.NewLineTee(&sink, func(line string) { lines = append(lines, line) })

	w.Write([]byte("first\nseco"))
	w.Write([]byte("nd\npartial"))
	if fl, ok := w.(interface{ Flush() }); ok {
		fl.Flush()
	}

	if got := sink.String(); got != "first\nsecond\npartial" {
		t.Fatalf("sink = %q", got)
	}
	if len(lines) != 3 || lines[0] != "first" || lines[1] != "second" || lines[2] != "partial" {
		t.Fatalf("lines = %q", lines)
	}
}

func TestLineTeeCarriageReturnProgress(t *testing.T) {
	var lines []string
	w := installer.NewLineTee(nil, func(line string) { lines = append(lines, line) })
	w.Write([]byte("progress 10%\rprogress 50%\rprogress 100%\n"))
	if len(lines) != 1 || lines[0] != "progress 100%" {
		t.Fatalf("lines = %q, want only the final progress frame", lines)
	}
}

func TestLineTeeCarriageReturnMirrorsSink(t *testing.T) {
	var sink bytes.Buffer
	var lines []string
	w := installer.NewLineTee(&sink, func(line string) { lines = append(lines, line) })
	w.Write([]byte("a\rb\n"))
	// The sink receives every byte unchanged, including the progress frames.
	if got := sink.String(); got != "a\rb\n" {
		t.Fatalf("sink = %q", got)
	}
	if len(lines) != 1 || lines[0] != "b" {
		t.Fatalf("lines = %q, want only the final progress frame", lines)
	}
}

func TestLineTeeSkipsBlankLines(t *testing.T) {
	var lines []string
	w := installer.NewLineTee(nil, func(line string) { lines = append(lines, line) })
	w.Write([]byte("one\n\n   \r\n\t\ntwo\n"))
	if len(lines) != 2 || lines[0] != "one" || lines[1] != "two" {
		t.Fatalf("lines = %q, want only non-blank lines", lines)
	}
}

func TestLineTeeTruncatesLongLines(t *testing.T) {
	var lines []string
	w := installer.NewLineTee(nil, func(line string) { lines = append(lines, line) })
	long := strings.Repeat("x", 5000)
	w.Write([]byte(long + "\n"))
	if len(lines) != 1 || len(lines[0]) != 4096 {
		t.Fatalf("line length = %d, want 4096", len(lines[0]))
	}
}

func TestAskYesNoNonInteractiveDefaults(t *testing.T) {
	r := &installer.Runner{Stderr: io.Discard, NonInteractive: true}
	ok, err := installer.AskYesNo(r, "Proceed?", true)
	if err != nil || !ok {
		t.Fatalf("default-true prompt returned %v/%v", ok, err)
	}
	ok, err = installer.AskYesNo(r, "Proceed?", false)
	if err != nil || ok {
		t.Fatalf("default-false prompt returned %v/%v", ok, err)
	}
}

func TestCommandLineRendering(t *testing.T) {
	if got := installer.CommandLine("emerge", "--verbose", "git"); got != "emerge --verbose git" {
		t.Fatalf("CommandLine = %q", got)
	}
}

func TestAskYesNoInteractive(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		def     bool
		want    bool
		wantErr bool
	}{
		{"explicit yes", "yes\n", false, true, false},
		{"single letter y", "y\n", false, true, false},
		{"explicit no", "no\n", true, false, false},
		{"single letter n", "n\n", true, false, false},
		{"case insensitive", "YES\n", false, true, false},
		{"empty uses default true", "\n", true, true, false},
		{"empty uses default false", "\n", false, false, false},
		{"invalid then valid retries", "maybe\ny\n", false, true, false},
		{"eof with no input", "", false, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &installer.Runner{Stderr: io.Discard, Stdin: strings.NewReader(tc.input)}
			ok, err := installer.AskYesNo(r, "Proceed?", tc.def)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got %v/%v", ok, err)
			}
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if ok != tc.want {
					t.Fatalf("got %v, want %v", ok, tc.want)
				}
			}
		})
	}
}

func TestPromptLine(t *testing.T) {
	r := &installer.Runner{Stderr: io.Discard, Stdin: strings.NewReader("hello world\n")}
	got, err := installer.PromptLine(r, "Name? ")
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello world" {
		t.Fatalf("got %q, want %q", got, "hello world")
	}
}

func TestInteractiveOnFailureMapping(t *testing.T) {
	cases := []struct {
		input string
		want  installer.FailAction
	}{
		{"s\n", installer.FailShell},
		{"shell\n", installer.FailShell},
		{"r\n", installer.FailRetry},
		{"retry\n", installer.FailRetry},
		{"a\n", installer.FailAbort},
		{"abort\n", installer.FailAbort},
		{"c\n", installer.FailContinue},
		{"continue\n", installer.FailContinue},
		{"p\n", installer.FailPrint},
		{"print\n", installer.FailPrint},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			onFail := installer.InteractiveOnFailure(&installer.Runner{
				Stderr: io.Discard,
				Stdin:  strings.NewReader(tc.input),
			})
			if got := onFail("some cmd", nil); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestInteractiveOnFailureEofAborts(t *testing.T) {
	onFail := installer.InteractiveOnFailure(&installer.Runner{
		Stderr: io.Discard,
		Stdin:  strings.NewReader(""),
	})
	if got := onFail("some cmd", nil); got != installer.FailAbort {
		t.Fatalf("got %v, want FailAbort", got)
	}
}
