// Output writers, prompts and failure-handling helpers.
package tests

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"gentooinstall/lib/installer"
)

func TestLineTeeSplitsLines(testingT *testing.T) {
	var sink bytes.Buffer
	var lines []string
	tee := installer.NewLineTee(&sink, func(line string) { lines = append(lines, line) })

	tee.Write([]byte("first\nseco"))
	tee.Write([]byte("nd\npartial"))
	if fl, ok := tee.(interface{ Flush() }); ok {
		fl.Flush()
	}

	if got := sink.String(); got != "first\nsecond\npartial" {
		testingT.Fatalf("sink = %q", got)
	}
	if len(lines) != 3 || lines[0] != "first" || lines[1] != "second" || lines[2] != "partial" {
		testingT.Fatalf("lines = %q", lines)
	}
}

func TestLineTeeCarriageReturnProgress(testingT *testing.T) {
	var lines []string
	tee := installer.NewLineTee(nil, func(line string) { lines = append(lines, line) })
	tee.Write([]byte("progress 10%\rprogress 50%\rprogress 100%\n"))
	if len(lines) != 1 || lines[0] != "progress 100%" {
		testingT.Fatalf("lines = %q, want only the final progress frame", lines)
	}
}

func TestLineTeeCarriageReturnMirrorsSink(testingT *testing.T) {
	var sink bytes.Buffer
	var lines []string
	tee := installer.NewLineTee(&sink, func(line string) { lines = append(lines, line) })
	tee.Write([]byte("a\rb\n"))
	// The sink receives every byte unchanged, including the progress frames.
	if got := sink.String(); got != "a\rb\n" {
		testingT.Fatalf("sink = %q", got)
	}
	if len(lines) != 1 || lines[0] != "b" {
		testingT.Fatalf("lines = %q, want only the final progress frame", lines)
	}
}

func TestLineTeeSkipsBlankLines(testingT *testing.T) {
	var lines []string
	tee := installer.NewLineTee(nil, func(line string) { lines = append(lines, line) })
	tee.Write([]byte("one\n\n   \r\n\t\ntwo\n"))
	if len(lines) != 2 || lines[0] != "one" || lines[1] != "two" {
		testingT.Fatalf("lines = %q, want only non-blank lines", lines)
	}
}

func TestLineTeeTruncatesLongLines(testingT *testing.T) {
	var lines []string
	tee := installer.NewLineTee(nil, func(line string) { lines = append(lines, line) })
	long := strings.Repeat("x", 5000)
	tee.Write([]byte(long + "\n"))
	if len(lines) != 1 || len(lines[0]) != 4096 {
		testingT.Fatalf("line length = %d, want 4096", len(lines[0]))
	}
}

func TestTailWriterKeepsLastLines(testingT *testing.T) {
	var sink bytes.Buffer
	tw := installer.NewTailWriter(&sink, 3)
	tw.Write([]byte("one\ntwo\nthree\n"))
	tw.Write([]byte("four\n"))
	if got := tw.Tail(); len(got) != 3 || got[0] != "two" || got[2] != "four" {
		testingT.Fatalf("tail = %q, want the last 3 lines", got)
	}
	if got := sink.String(); got != "one\ntwo\nthree\nfour\n" {
		testingT.Fatalf("sink = %q", got)
	}
}

func TestTailWriterFlushCapturesTrailingPartial(testingT *testing.T) {
	tw := installer.NewTailWriter(nil, 5)
	tw.Write([]byte("error: something bad"))
	tw.Flush()
	if got := tw.Tail(); len(got) != 1 || got[0] != "error: something bad" {
		testingT.Fatalf("tail = %q", got)
	}
}

func TestInstallLogRoundTrip(testingT *testing.T) {
	path := installer.InstallLogPath()
	defer os.Remove(path)
	file, err := installer.OpenInstallLog()
	if err != nil {
		testingT.Fatal(err)
	}
	if _, err := file.Write([]byte("hello install log\n")); err != nil {
		testingT.Fatal(err)
	}
	if err := file.Close(); err != nil {
		testingT.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		testingT.Fatalf("read install log: %v", err)
	}
	if !strings.Contains(string(data), "hello install log") {
		testingT.Fatalf("install log missing expected content: %q", data)
	}
}

func TestAskYesNoNonInteractiveDefaults(testingT *testing.T) {
	runner := &installer.Runner{Stderr: io.Discard, NonInteractive: true}
	ok, err := installer.AskYesNo(runner, "Proceed?", true)
	if err != nil || !ok {
		testingT.Fatalf("default-true prompt returned %v/%v", ok, err)
	}
	ok, err = installer.AskYesNo(runner, "Proceed?", false)
	if err != nil || ok {
		testingT.Fatalf("default-false prompt returned %v/%v", ok, err)
	}
}

func TestCommandLineRendering(testingT *testing.T) {
	if got := installer.CommandLine("emerge", "--verbose", "git"); got != "emerge --verbose git" {
		testingT.Fatalf("CommandLine = %q", got)
	}
}

func TestAskYesNoInteractive(testingT *testing.T) {
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
		testingT.Run(tc.name, func(testingT *testing.T) {
			runner := &installer.Runner{Stderr: io.Discard, Stdin: strings.NewReader(tc.input)}
			ok, err := installer.AskYesNo(runner, "Proceed?", tc.def)
			if tc.wantErr && err == nil {
				testingT.Fatalf("expected error, got %v/%v", ok, err)
			}
			if !tc.wantErr {
				if err != nil {
					testingT.Fatalf("unexpected error: %v", err)
				}
				if ok != tc.want {
					testingT.Fatalf("got %v, want %v", ok, tc.want)
				}
			}
		})
	}
}

func TestPromptLine(testingT *testing.T) {
	runner := &installer.Runner{Stderr: io.Discard, Stdin: strings.NewReader("hello world\n")}
	got, err := installer.PromptLine(runner, "Name? ")
	if err != nil {
		testingT.Fatal(err)
	}
	if got != "hello world" {
		testingT.Fatalf("got %q, want %q", got, "hello world")
	}
}

func TestInteractiveOnFailureMapping(testingT *testing.T) {
	cases := []struct {
		input string
		want  installer.FailAction
	}{
		{"\n", installer.FailRetry},
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
		testingT.Run(tc.input, func(testingT *testing.T) {
			onFail := installer.InteractiveOnFailure(&installer.Runner{
				Stderr: io.Discard,
				Stdin:  strings.NewReader(tc.input),
			})
			if got := onFail("some cmd", nil); got != tc.want {
				testingT.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestInteractiveOnFailureEofAborts(testingT *testing.T) {
	onFail := installer.InteractiveOnFailure(&installer.Runner{
		Stderr: io.Discard,
		Stdin:  strings.NewReader(""),
	})
	if got := onFail("some cmd", nil); got != installer.FailAbort {
		testingT.Fatalf("got %v, want FailAbort", got)
	}
}
