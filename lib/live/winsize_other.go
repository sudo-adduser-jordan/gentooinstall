//go:build !linux

package live

// GetWinsize is unavailable off linux; the TUI falls back to its
// placeholder size and native WindowSizeMsg reports.
func GetWinsize() (cols, rows int, ok bool) { return 0, 0, false }

// DetectWinsize is unavailable off linux (see winsize_linux.go).
func DetectWinsize() (cols, rows int, ok bool) { return 0, 0, false }

// ApplyWinsize is a no-op off linux.
func ApplyWinsize(cols, rows int) {}

// ParseCSITextAreaReply is unavailable off linux.
func ParseCSITextAreaReply(b []byte) (cols, rows int, ok bool) { return 0, 0, false }
