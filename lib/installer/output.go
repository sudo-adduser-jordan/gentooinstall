package installer

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const maxLineTeeLine = 4096

const maxTailLines = 40

type lineTee struct {
	sink io.Writer
	fn   func(line string)
	buf  bytes.Buffer
}

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

func (tee *lineTee) Flush() {
	if tee.buf.Len() == 0 {
		return
	}
	line := tee.buf.String()
	tee.buf.Reset()
	tee.emit(line + "\n")
}

type TailWriter struct {
	sink  io.Writer
	max   int
	lines []string
	buf   string
}

func NewTailWriter(sink io.Writer, maxLines int) *TailWriter {
	if maxLines < 1 {
		maxLines = 1
	}
	return &TailWriter{sink: sink, max: maxLines}
}

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

func (tw *TailWriter) Flush() {
	if tw.buf == "" {
		return
	}
	tw.push(strings.TrimRight(tw.buf, "\r\n"))
	tw.buf = ""
}

func (tw *TailWriter) Tail() []string { return append([]string{}, tw.lines...) }

func InstallLogPath() string { return filepath.Join(TmpDir, "install.log") }

func OpenInstallLog() (io.WriteCloser, error) {
	path := InstallLogPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create install log directory: %w", err)
	}
	return os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
}
