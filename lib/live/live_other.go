//go:build !linux

package live

import "errors"

func Init() error {
	return errors.New("live init is only supported on linux")
}
