//go:build linux

package live

import (
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// Winsize bounds for detected terminal sizes. Anything outside is treated
// as unknown (a zero reply from a terminal that cannot report its size,
// garbage, or overflow) rather than a layout input.
const (
	minDetectCols = 20
	maxDetectCols = 1000
	minDetectRows = 10
	maxDetectRows = 500
)

// csiTextAreaSize is the query a terminal answers with its size in
// characters (DECRQM-style report travels transparently over
// qemu -serial stdio to the host terminal emulator).
const csiTextAreaSize = "\x1b[18t"

// csiQueryTimeout bounds the whole blocking read for the reply so boot
// never stalls on a terminal that stays silent (dumb terms, pipes, CI).
const csiQueryTimeout = 1200 * time.Millisecond

// GetWinsize reports the kernel-known size of the terminal behind stdout
// (falling back to stdin, /dev/console and /dev/ttyS0). It never queries
// the terminal itself, so it is safe to call while the TUI owns the
// screen. ok is false when no usable size is known.
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

// DetectWinsize returns the real terminal size: the kernel value when one
// is known, otherwise a live query of the terminal through the serial
// pipe (qemu -nographic never forwards host resizes, so the guest kernel
// reports 0x0 there). Must run before the TUI enters the alt-screen: the
// reply arrives on stdin and would otherwise land in the TUI input.
func DetectWinsize() (cols, rows int, ok bool) {
	if width, height, valid := GetWinsize(); valid {
		return width, height, true
	}
	return queryTerminalSize(os.Stdin, os.Stdout)
}

// ApplyWinsize publishes cols x rows to the console devices so later
// TIOCGWINSZ reads (bubbletea, the TUI poll) observe the true size.
// Best-effort: every target is independent.
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

// queryTerminalSize asks the terminal on out for its size and reads the
// reply from in. in must support termios (a tty/serial port); pipes and
// files are skipped immediately. The terminal is briefly switched to
// non-canonical no-echo mode so the reply (which ends in 't', not a
// newline) can be read, then restored.
func queryTerminalSize(in, out *os.File) (int, int, bool) {
	inFd := int(in.Fd())
	old, err := unix.IoctlGetTermios(inFd, unix.TCGETS)
	if err != nil {
		return 0, 0, false
	}
	raw := *old
	raw.Lflag &^= unix.ECHO | unix.ICANON
	raw.Cc[unix.VMIN] = 0
	raw.Cc[unix.VTIME] = 5 // 0.5s per read; total bounded below
	if err := unix.IoctlSetTermios(inFd, unix.TCSETS, &raw); err != nil {
		return 0, 0, false
	}
	defer unix.IoctlSetTermios(inFd, unix.TCSETS, old) //nolint:errcheck

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

// ParseCSITextAreaReply extracts the last "ESC[8;rows;cols t" report from
// data (terminals may echo setup noise first). Exported for tests.
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
