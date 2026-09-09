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

func (tee *lineTee) Write(data []byte) (int, error) {
	count := len(data)
	if tee.sink != nil {
		_, _ = tee.sink.Write(data)
	}
	tee.buf.Write(data)
	for {
		line, err := tee.buf.ReadString('\n')
		if err != nil || line == "" {
			if line != "" {
				tee.buf.Reset()
				tee.buf.WriteString(line)
			}
			break
		}
		tee.emit(line)
	}
	return count, nil
}

func (tee *lineTee) emit(line string) {
	line = strings.TrimRight(line, "\n")
	// Keep only the portion after the last carriage return so progress
	// bars do not accumulate into one giant line.
	if index := strings.LastIndexByte(line, '\r'); index >= 0 {
		line = line[index+1:]
	}
	if strings.TrimSpace(line) == "" {
		return
	}
	if len(line) > maxLineTeeLine {
		line = line[:maxLineTeeLine]
	}
	if tee.fn != nil {
		tee.fn(line)
	}
}

// Flush emits a trailing partial line if one is buffered.
func (tee *lineTee) Flush() {
	if tee.buf.Len() == 0 {
		return
	}
	line := tee.buf.String()
	tee.buf.Reset()
	tee.emit(line + "\n")
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

// Write mirrors data to the sink and retains complete lines for Tail.
func (tw *TailWriter) Write(data []byte) (int, error) {
	count := len(data)
	if tw.sink != nil {
		_, _ = tw.sink.Write(data)
	}
	tw.buf += string(data)
	for {
		index := strings.IndexByte(tw.buf, '\n')
		if index < 0 {
			break
		}
		line := strings.TrimRight(tw.buf[:index], "\r")
		tw.buf = tw.buf[index+1:]
		if line != "" {
			tw.push(line)
		}
	}
	return count, nil
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
