//go:build windows

package main

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows/registry"
)

func registerProtocolHandler() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	root, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Classes\coslash`, registry.SET_VALUE|registry.CREATE_SUB_KEY)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.SetStringValue("", "URL:coSlash Local pairing"); err != nil {
		return err
	}
	if err := root.SetStringValue("URL Protocol", ""); err != nil {
		return err
	}
	icon, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Classes\coslash\DefaultIcon`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	if err := icon.SetStringValue("", fmt.Sprintf(`"%s",0`, executable)); err != nil {
		icon.Close()
		return err
	}
	if err := icon.Close(); err != nil {
		return err
	}
	command, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Classes\coslash\shell\open\command`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer command.Close()
	return command.SetStringValue("", fmt.Sprintf(`"%s" --protocol-url "%%1"`, executable))
}
