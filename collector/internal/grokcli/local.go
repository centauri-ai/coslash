package grokcli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

func Home() string {
	if home := os.Getenv("GROK_HOME"); home != "" {
		return home
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".grok")
}

func Executable() string {
	if path, err := exec.LookPath("grok"); err == nil {
		return path
	}
	if runtime.GOOS == "windows" {
		userHome, _ := os.UserHomeDir()
		for _, root := range []string{Home(), filepath.Join(userHome, ".grok")} {
			path, err := filepath.Abs(filepath.Join(root, "bin", "grok.exe"))
			if err == nil {
				if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
					return path
				}
			}
		}
	}
	return "grok"
}

// PrepareHome isolates generated sessions while retaining the existing login.
// Windows copies the login because file symlinks require additional privileges.
func PrepareHome(scratch string) (string, error) {
	scratch, err := filepath.Abs(scratch)
	if err != nil {
		return "", err
	}
	if err := protectDirectory(scratch); err != nil {
		return "", err
	}
	home := filepath.Join(scratch, "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		return "", err
	}
	source, err := filepath.Abs(filepath.Join(Home(), "auth.json"))
	if err != nil {
		return "", err
	}
	file, err := os.Open(source)
	if os.IsNotExist(err) {
		return home, nil
	}
	if err != nil {
		return "", fmt.Errorf("open Grok login: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return "", fmt.Errorf("Grok login must be a regular file under 1 MiB")
	}
	target := filepath.Join(home, "auth.json")
	if runtime.GOOS != "windows" {
		return home, os.Symlink(source, target)
	}
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil {
		return "", err
	}
	if len(data) > 1<<20 {
		return "", fmt.Errorf("Grok login exceeds 1 MiB")
	}
	return home, os.WriteFile(target, data, 0o600)
}
