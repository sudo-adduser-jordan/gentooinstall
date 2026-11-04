//go:build freebsd || linux || darwin

package installer

import "syscall"

func setUmask(mask int) {
	_ = syscall.Umask(mask)
}
