//go:build !linux

package remoteinstall

import "errors"

var ErrNoExec = errors.New("helper install directory is not executable")

func Install(string, string, string) error {
	return errors.New("secure helper installation requires linux")
}
