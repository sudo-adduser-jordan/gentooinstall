package installer

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// maxLineTeeLine caps the length of a single forwarded line.
const maxLineTeeLine = 4096

// maxTailLines caps how many child-output lines are kept for error reports.
const maxTailLines = 40

// lineTee is an io.Writer that optionally mirrors bytes to a sink writer
// and forwards every complete line to a callback. It is used to stream
// child-process output into the TUI install window while keeping the
// original destination (or discarding it when sink is nil).
type lineTee struct {
	sink io.Writer
	fn   func(line string)
	buf  bytes.Buffer
}

// NewLineTee returns a writer that copies everything written to it into
// sink (which may be nil to discard) and invokes fn once per complete
// newline-terminated line.
func NewLineTee(sink io.Writer, fn func(line string)) io.Writer {
	return &lineTee{sink: sink, fn: fn}
}

func (t *lineTee) Write(p []byte) (int, error) {
	n := len(p)
	if t.sink != nil {
		_, _ = t.sink.Write(p)
	}
	t.buf.Write(p)
	for {
		line, err := t.buf.ReadString('\n')
		if err != nil || line == "" {
			if line != "" {
				t.buf.Reset()
				t.buf.WriteString(line)
			}
			break
		}
		t.emit(line)
	}
	return n, nil
}

func (t *lineTee) emit(line string) {
	line = strings.TrimRight(line, "\n")
	// Keep only the portion after the last carriage return so progress
	// bars do not accumulate into one giant line.
	if i := strings.LastIndexByte(line, '\r'); i >= 0 {
		line = line[i+1:]
	}
	if strings.TrimSpace(line) == "" {
		return
	}
	if len(line) > maxLineTeeLine {
		line = line[:maxLineTeeLine]
	}
	if t.fn != nil {
		t.fn(line)
	}
}

// Flush emits a trailing partial line if one is buffered.
func (t *lineTee) Flush() {
	if t.buf.Len() == 0 {
		return
	}
	line := t.buf.String()
	t.buf.Reset()
	t.emit(line + "\n")
}

// TailWriter mirrors every write to sink while retaining the last maxLines
// newline-terminated lines, so a failing child's final output can be
// surfaced in errors instead of being lost in scrollback.
type TailWriter struct {
	sink  io.Writer
	max   int
	lines []string
	buf   string
}

// NewTailWriter returns a writer that forwards raw bytes to sink (nil to
// discard) and keeps the last maxLines complete non-blank lines.
func NewTailWriter(sink io.Writer, maxLines int) *TailWriter {
	if maxLines < 1 {
		maxLines = 1
	}
	return &TailWriter{sink: sink, max: maxLines}
}

func (tw *TailWriter) Write(p []byte) (int, error) {
	n := len(p)
	if tw.sink != nil {
		_, _ = tw.sink.Write(p)
	}
	tw.buf += string(p)
	for {
		i := strings.IndexByte(tw.buf, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(tw.buf[:i], "\r")
		tw.buf = tw.buf[i+1:]
		if line != "" {
			tw.push(line)
		}
	}
	return n, nil
}

func (tw *TailWriter) push(line string) {
	tw.lines = append(tw.lines, line)
	if len(tw.lines) > tw.max {
		tw.lines = tw.lines[len(tw.lines)-tw.max:]
	}
}

// Flush records a trailing partial line (prompt-style output that ends
// without a newline), mirroring lineTee.Flush.
func (tw *TailWriter) Flush() {
	if tw.buf == "" {
		return
	}
	tw.push(strings.TrimRight(tw.buf, "\r\n"))
	tw.buf = ""
}

// Tail returns the retained lines in their original order.
func (tw *TailWriter) Tail() []string { return append([]string{}, tw.lines...) }

// InstallLogPath is the rolling log of an installation run, kept on the
// live system so failures stay greppable after the TUI window scrolls.
func InstallLogPath() string { return filepath.Join(TmpDir, "install.log") }

// OpenInstallLog appends to InstallLogPath, creating it and its parents.
func OpenInstallLog() (io.WriteCloser, error) {
	path := InstallLogPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create install log directory: %w", err)
	}
	return os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
}
