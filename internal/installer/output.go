package installer

import (
	"bytes"
	"io"
	"strings"
)

// maxLineTeeLine caps the length of a single forwarded line.
const maxLineTeeLine = 4096

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
