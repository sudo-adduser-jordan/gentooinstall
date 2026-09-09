//go:build !linux

package live

import "errors"

// Init is a no-op on platforms that are never booted as the live-ISO init;
// the PID-1 path is linux-only by construction.
func Init() error {
	return errors.New("live init is only supported on linux")
}
