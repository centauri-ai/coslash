//go:build !darwin && !windows

package main

import "errors"

func registerProtocolHandler() error {
	return errors.New("desktop activation is supported on macOS and Windows")
}
