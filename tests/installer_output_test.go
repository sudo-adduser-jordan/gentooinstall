package tests

import (
	"bytes"
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

func TestAskYesNoNonInteractiveDefaults(t *testing.T) {
	r := &installer.Runner{NonInteractive: true}
	ok, err := installer.AskYesNo(r, "Proceed?", true)
	if err != nil || !ok {
		t.Fatalf("default-true prompt returned %v/%v", ok, err)
	}
	ok, err = installer.AskYesNo(r, "Proceed?", false)
	if err != nil || ok {
		t.Fatalf("default-false prompt returned %v/%v", ok, err)
	}
	if strings.Contains("", "never") {
		t.Fatal("unreachable")
	}
}

func TestCommandLineRendering(t *testing.T) {
	if got := installer.CommandLine("emerge", "--verbose", "git"); got != "emerge --verbose git" {
		t.Fatalf("CommandLine = %q", got)
	}
}
