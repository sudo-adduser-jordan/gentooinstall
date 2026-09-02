package sysinfo

import (
	"bytes"
	"os/exec"
)

func bytes_equal(a, b []byte) bool { return bytes.Equal(a, b) }

func runCapture(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	return cmd.Output()
}
