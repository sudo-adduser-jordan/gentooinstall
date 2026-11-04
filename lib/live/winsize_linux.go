//go:build linux

package live

import (
	"os"
	"time"

	"golang.org/x/sys/unix"
)

const (
	minDetectCols = 20
	maxDetectCols = 1000
	minDetectRows = 10
	maxDetectRows = 500
)

const csiTextAreaSize = "\x1b[18t"

const csiQueryTimeout = 1200 * time.Millisecond

func GetWinsize() (cols, rows int, ok bool) {
	for _, file := range []*os.File{os.Stdout, os.Stdin} {
		if width, height, valid := winsizeOf(int(file.Fd())); valid {
			return width, height, true
		}
	}
	for _, path := range []string{"/dev/console", "/dev/ttyS0"} {
		if file, err := os.OpenFile(path, os.O_RDONLY, 0); err == nil {
			width, height, valid := winsizeOf(int(file.Fd()))
			_ = file.Close()
			if valid {
				return width, height, true
			}
		}
	}
	return 0, 0, false
}

func DetectWinsize() (cols, rows int, ok bool) {
	if width, height, valid := GetWinsize(); valid {
		return width, height, true
	}
	return queryTerminalSize(os.Stdin, os.Stdout)
}

func ApplyWinsize(cols, rows int) {
	if !clampedSize(cols, rows) {
		return
	}
	ws := &unix.Winsize{Row: uint16(rows), Col: uint16(cols)}
	for _, file := range []*os.File{os.Stdin, os.Stdout} {
		_ = unix.IoctlSetWinsize(int(file.Fd()), unix.TIOCSWINSZ, ws)
	}
	for _, path := range []string{"/dev/console", "/dev/ttyS0"} {
		if file, err := os.OpenFile(path, os.O_WRONLY, 0); err == nil {
			_ = unix.IoctlSetWinsize(int(file.Fd()), unix.TIOCSWINSZ, ws)
			_ = file.Close()
		}
	}
}

func winsizeOf(fd int) (int, int, bool) {
	ws, err := unix.IoctlGetWinsize(fd, unix.TIOCGWINSZ)
	if err != nil {
		return 0, 0, false
	}
	if !clampedSize(int(ws.Col), int(ws.Row)) {
		return 0, 0, false
	}
	return int(ws.Col), int(ws.Row), true
}

func clampedSize(cols, rows int) bool {
	return cols >= minDetectCols && cols <= maxDetectCols &&
		rows >= minDetectRows && rows <= maxDetectRows
}

func queryTerminalSize(in, out *os.File) (int, int, bool) {
	inFd := int(in.Fd())
	old, err := unix.IoctlGetTermios(inFd, unix.TCGETS)
	if err != nil {
		return 0, 0, false
	}
	raw := *old
	raw.Lflag &^= unix.ECHO | unix.ICANON
	raw.Cc[unix.VMIN] = 0
	raw.Cc[unix.VTIME] = 5
	if err := unix.IoctlSetTermios(inFd, unix.TCSETS, &raw); err != nil {
		return 0, 0, false
	}
	defer unix.IoctlSetTermios(inFd, unix.TCSETS, old)

	if _, err := out.WriteString(csiTextAreaSize); err != nil {
		return 0, 0, false
	}
	var buf []byte
	tmp := make([]byte, 64)
	deadline := time.Now().Add(csiQueryTimeout)
	for time.Now().Before(deadline) {
		length, err := in.Read(tmp)
		if length > 0 {
			buf = append(buf, tmp[:length]...)
			if width, height, valid := ParseCSITextAreaReply(buf); valid {
				return width, height, true
			}
		}
		if err != nil {
			break
		}
	}
	return ParseCSITextAreaReply(buf)
}

func ParseCSITextAreaReply(data []byte) (cols, rows int, ok bool) {
	found := false
	for index := 0; index+6 < len(data); index++ {
		if data[index] != 0x1b || data[index+1] != '[' || data[index+2] != '8' || data[index+3] != ';' {
			continue
		}
		pos := index + 4
		rowsNum := 0
		for pos < len(data) && data[pos] >= '0' && data[pos] <= '9' {
			rowsNum = rowsNum*10 + int(data[pos]-'0')
			pos++
		}
		if pos >= len(data) || data[pos] != ';' {
			continue
		}
		pos++
		colsNum := 0
		for pos < len(data) && data[pos] >= '0' && data[pos] <= '9' {
			colsNum = colsNum*10 + int(data[pos]-'0')
			pos++
		}
		if pos >= len(data) || data[pos] != 't' {
			continue
		}
		if clampedSize(colsNum, rowsNum) {
			cols, rows, found = colsNum, rowsNum, true
		}
	}
	return cols, rows, found
}
