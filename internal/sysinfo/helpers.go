package sysinfo

import (
	"bytes"
	"os/exec"
	"runtime"
)

func runtime_numCPU() int { return runtime.NumCPU() }

func bytes_equal(a, b []byte) bool { return bytes.Equal(a, b) }

func runCapture(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	return cmd.Output()
}
