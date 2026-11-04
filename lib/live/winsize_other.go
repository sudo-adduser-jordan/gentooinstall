//go:build !linux

package live

func GetWinsize() (cols, rows int, ok bool) { return 0, 0, false }

func DetectWinsize() (cols, rows int, ok bool) { return 0, 0, false }

func ApplyWinsize(cols, rows int) {}

func ParseCSITextAreaReply(data []byte) (cols, rows int, ok bool) { return 0, 0, false }
