package persistence

import "os"

// mkdirAllPrivate creates the store root with private permissions.
//
// This is the one unscoped filesystem call in the package: a scope can only be
// opened on a directory that already exists. Everything below the root is
// reached through the scope.
func mkdirAllPrivate(root string) error {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return os.Chmod(root, 0o700)
	}
	return nil
}
