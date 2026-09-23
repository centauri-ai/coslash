package main

import (
	"github.com/centauri-ai/coslash/collector/internal/windowsprivate"
)

func protectTokenDirectory(path string) error {
	return windowsprivate.ProtectDirectory(path, "token")
}

func writeTokenFile(home, token string) error {
	pending, err := windowsprivate.CreatePrivateTempFile(home, ".token-", "token")
	if err != nil {
		return err
	}
	defer pending.Close()
	if _, err := pending.File.WriteString(token + "\n"); err != nil {
		return err
	}
	return pending.Commit("token")
}
