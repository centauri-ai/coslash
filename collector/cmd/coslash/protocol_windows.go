//go:build windows

package main

import (
	"errors"
	"fmt"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const windowsProtocolHandlerKey = `Software\Classes\coslash`

type windowsProtocolHandlerBackend struct{}

func newProtocolHandlerBackend() protocolHandlerBackend { return windowsProtocolHandlerBackend{} }

func (windowsProtocolHandlerBackend) Register(executable, _ string, _ protocolHandlerRecord) (string, error) {
	root, _, err := registry.CreateKey(registry.CURRENT_USER, windowsProtocolHandlerKey, registry.SET_VALUE|registry.CREATE_SUB_KEY)
	if err != nil {
		return "", err
	}
	defer root.Close()
	if err := root.SetStringValue("", "URL:coSlash Local pairing"); err != nil {
		return "", err
	}
	if err := root.SetStringValue("URL Protocol", ""); err != nil {
		return "", err
	}
	icon, _, err := registry.CreateKey(registry.CURRENT_USER, windowsProtocolHandlerKey+`\DefaultIcon`, registry.SET_VALUE)
	if err != nil {
		return "", err
	}
	if err := icon.SetStringValue("", fmt.Sprintf(`"%s",0`, executable)); err != nil {
		icon.Close()
		return "", err
	}
	if err := icon.Close(); err != nil {
		return "", err
	}
	command, _, err := registry.CreateKey(registry.CURRENT_USER, windowsProtocolHandlerKey+`\shell\open\command`, registry.SET_VALUE)
	if err != nil {
		return "", err
	}
	defer command.Close()
	if err := command.SetStringValue("", fmt.Sprintf(`"%s" --protocol-url "%%1"`, executable)); err != nil {
		return "", err
	}
	return executable, nil
}

func (windowsProtocolHandlerBackend) Status(_, _ string) (protocolHandlerRegistration, error) {
	root, err := registry.OpenKey(registry.CURRENT_USER, windowsProtocolHandlerKey, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return protocolHandlerRegistration{}, nil
	}
	if err != nil {
		return protocolHandlerRegistration{}, err
	}
	if _, _, err := root.GetStringValue("URL Protocol"); err != nil {
		root.Close()
		return protocolHandlerRegistration{}, nil
	}
	root.Close()
	command, err := registry.OpenKey(registry.CURRENT_USER, windowsProtocolHandlerKey+`\shell\open\command`, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return protocolHandlerRegistration{}, nil
	}
	if err != nil {
		return protocolHandlerRegistration{}, err
	}
	value, _, err := command.GetStringValue("")
	command.Close()
	if err != nil {
		return protocolHandlerRegistration{}, err
	}
	executable := executableFromWindowsProtocolCommand(value)
	if executable == "" {
		return protocolHandlerRegistration{}, nil
	}
	return protocolHandlerRegistration{Registered: true, Executable: executable, Path: windowsProtocolHandlerKey}, nil
}

func executableFromWindowsProtocolCommand(command string) string {
	command = strings.TrimSpace(command)
	if !strings.HasPrefix(command, `"`) {
		return ""
	}
	command = strings.TrimPrefix(command, `"`)
	end := strings.IndexByte(command, '"')
	if end <= 0 {
		return ""
	}
	return command[:end]
}

func (windowsProtocolHandlerBackend) StaleHandlers(_, _ string) ([]string, error) {
	return nil, errors.New("coslash doctor --fix-links is only available on macOS")
}

func (windowsProtocolHandlerBackend) Unregister(path string) error {
	if path != windowsProtocolHandlerKey {
		return fmt.Errorf("refusing to unregister unexpected protocol handler path %q", path)
	}
	return registry.DeleteKey(registry.CURRENT_USER, windowsProtocolHandlerKey)
}
