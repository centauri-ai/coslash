//go:build windows

package main

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

func startDetachedUpdateProcess(executable string, args ...string) error {
	command := exec.Command(executable, args...)
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000008}
	command.Stdin, command.Stdout, command.Stderr = nil, nil, nil
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}

func replaceUpdateTarget(target, staged, backup string) error {
	if err := os.Remove(backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := copyUpdateBackup(target, backup); err != nil {
		return err
	}
	from, err := windows.UTF16PtrFromString(staged)
	if err != nil {
		_ = os.Remove(backup)
		return err
	}
	to, err := windows.UTF16PtrFromString(target)
	if err != nil {
		_ = os.Remove(backup)
		return err
	}
	if err := windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH); err != nil {
		_ = os.Remove(backup)
		return err
	}
	return nil
}

func copyUpdateBackup(source, backup string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(backup, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	syncErr := output.Sync()
	closeErr := output.Close()
	if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
		_ = os.Remove(backup)
		return err
	}
	return nil
}

func restoreUpdateTarget(target, backup string) error {
	from, err := windows.UTF16PtrFromString(backup)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

func scheduleUpdateHelperCleanup(helper string) error {
	command := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", `$path = [Environment]::GetEnvironmentVariable('COSLASH_UPDATE_HELPER'); $deadline = [DateTime]::UtcNow.AddMinutes(3); while ([DateTime]::UtcNow -lt $deadline) { if (-not (Test-Path -LiteralPath $path)) { exit 0 }; try { Remove-Item -LiteralPath $path -Force -ErrorAction Stop; exit 0 } catch { Start-Sleep -Seconds 1 } }`)
	command.Env = append(os.Environ(), "COSLASH_UPDATE_HELPER="+helper)
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000008}
	command.Stdin, command.Stdout, command.Stderr = nil, nil, nil
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}

func reexecBackground(string) error {
	return errors.New("re-executing a background process is unsupported on Windows")
}
