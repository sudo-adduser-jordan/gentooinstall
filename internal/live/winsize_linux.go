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
	for _, f := range []*os.File{os.Stdout, os.Stdin} {
		if c, r, good := winsizeOf(int(f.Fd())); good {
			return c, r, true
		}
	}
	for _, path := range []string{"/dev/console", "/dev/ttyS0"} {
		if f, err := os.OpenFile(path, os.O_RDONLY, 0); err == nil {
			c, r, good := winsizeOf(int(f.Fd()))
			_ = f.Close()
			if good {
				return c, r, true
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
	if c, r, good := GetWinsize(); good {
		return c, r, true
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
	for _, f := range []*os.File{os.Stdin, os.Stdout} {
		_ = unix.IoctlSetWinsize(int(f.Fd()), unix.TIOCSWINSZ, ws)
	}
	for _, path := range []string{"/dev/console", "/dev/ttyS0"} {
		if f, err := os.OpenFile(path, os.O_WRONLY, 0); err == nil {
			_ = unix.IoctlSetWinsize(int(f.Fd()), unix.TIOCSWINSZ, ws)
			_ = f.Close()
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
		n, err := in.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			if c, r, good := ParseCSITextAreaReply(buf); good {
				return c, r, true
			}
		}
		if err != nil {
			break
		}
	}
	return ParseCSITextAreaReply(buf)
}

// ParseCSITextAreaReply extracts the last "ESC[8;rows;cols t" report from
// b (terminals may echo setup noise first). Exported for tests.
func ParseCSITextAreaReply(b []byte) (cols, rows int, ok bool) {
	found := false
	for i := 0; i+6 < len(b); i++ {
		if b[i] != 0x1b || b[i+1] != '[' || b[i+2] != '8' || b[i+3] != ';' {
			continue
		}
		j := i + 4
		r := 0
		for j < len(b) && b[j] >= '0' && b[j] <= '9' {
			r = r*10 + int(b[j]-'0')
			j++
		}
		if j >= len(b) || b[j] != ';' {
			continue
		}
		j++
		c := 0
		for j < len(b) && b[j] >= '0' && b[j] <= '9' {
			c = c*10 + int(b[j]-'0')
			j++
		}
		if j >= len(b) || b[j] != 't' {
			continue
		}
		if clampedSize(c, r) {
			cols, rows, found = c, r, true
		}
	}
	return cols, rows, found
}
