//go:build freebsd || linux || darwin

package installer

import "syscall"

// setUmask applies the given process umask on Unix platforms.
func setUmask(mask int) {
	_ = syscall.Umask(mask)
}
