//go:build windows

package main

import "github.com/centauri-ai/coslash/collector/internal/windowsprivate"

func protectHubRegistryDirectory(dir string) error {
	return windowsprivate.ProtectDirectory(dir, "Hub connection registry")
}

func syncHubRegistryDirectory(string) error { return nil }

func writeHubRegistryFile(dir string, data []byte) (bool, error) {
	pending, err := windowsprivate.CreatePrivateTempFile(dir, ".registry-", "Hub connection registry")
	if err != nil {
		return false, err
	}
	defer pending.Close()
	if _, err := pending.File.Write(data); err != nil {
		return false, err
	}
	if err := pending.Commit("registry.json"); err != nil {
		return false, err
	}
	return true, nil
}
