//go:build !windows

package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const scriptInstallMarker = ".coslash-install-channel"
const scriptInstallMarkerContents = "coslash-script-install/v1\n"

// The marker grants the updater permission to replace this executable, so a
// custom installation must be inside the owner's private, non-symlink tree.
func trustedCustomScriptInstall(executable, home string) bool {
	if !filepath.IsAbs(executable) || !filepath.IsAbs(home) || filepath.Base(executable) != "coslash" {
		return false
	}
	home, err := filepath.EvalSymlinks(home)
	if err != nil {
		return false
	}
	executable = filepath.Clean(executable)
	rel, err := filepath.Rel(home, executable)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return false
	}
	owner := uint32(os.Geteuid())
	owned := func(info os.FileInfo) bool {
		stat, ok := info.Sys().(*syscall.Stat_t)
		return ok && stat.Uid == owner && info.Mode().Perm()&0o022 == 0
	}
	dir := home
	parts := strings.Split(rel, string(filepath.Separator))
	for _, part := range append([]string{""}, parts[:len(parts)-1]...) {
		dir = filepath.Join(dir, part)
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() || !owned(info) {
			return false
		}
	}
	info, err := os.Lstat(executable)
	if err != nil || !info.Mode().IsRegular() || !owned(info) {
		return false
	}
	marker := filepath.Join(dir, scriptInstallMarker)
	info, err = os.Lstat(marker)
	if err != nil || !info.Mode().IsRegular() || !owned(info) || info.Size() != int64(len(scriptInstallMarkerContents)) {
		return false
	}
	file, err := os.Open(marker)
	if err != nil {
		return false
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return false
	}
	contents, err := io.ReadAll(io.LimitReader(file, int64(len(scriptInstallMarkerContents)+1)))
	return err == nil && string(contents) == scriptInstallMarkerContents
}
