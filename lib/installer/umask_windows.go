//go:build windows

package installer

// setUmask is a no-op on Windows, where the concept of a process umask does
// not exist.
func setUmask(mask int) {}
