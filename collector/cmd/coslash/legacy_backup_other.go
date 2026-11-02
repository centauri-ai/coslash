//go:build !windows

package main

import (
	"errors"
	"os"
)

// retireLegacyBackup removes the legacy helper's only backup. A Unix rollback is
// an atomic rename of a hard link, so removing the link already has one winner;
// settleLegacyTarget's single post-removal check detects a rollback that won.
// verify is only needed on Windows, where it must run under an exclusive hold.
func retireLegacyBackup(backup string, _ func() error) error {
	if err := os.Remove(backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
