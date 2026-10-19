//go:build !darwin && !windows && !linux

package main

import (
	"errors"
	"fmt"
)

type unsupportedProtocolHandlerBackend struct{}

func newProtocolHandlerBackend() protocolHandlerBackend { return unsupportedProtocolHandlerBackend{} }

func (unsupportedProtocolHandlerBackend) Register(string, string, protocolHandlerRecord) (string, error) {
	return "", errors.New("desktop activation is not supported on this platform")
}

func (unsupportedProtocolHandlerBackend) Status(string, string) (protocolHandlerRegistration, error) {
	return protocolHandlerRegistration{}, nil
}

func (unsupportedProtocolHandlerBackend) StaleHandlers(string, string) ([]string, error) {
	return nil, errors.New("coslash doctor --fix-links is only available on macOS")
}

func (unsupportedProtocolHandlerBackend) Unregister(path string) error {
	return fmt.Errorf("desktop activation is not supported on this platform: %s", path)
}
