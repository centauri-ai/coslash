//go:build !windows

package pi

import "os"

func protectExtensionDirectories(string) error { return nil }
func protectExtensionFile(*os.File) error      { return nil }
