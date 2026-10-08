//go:build windows

package main

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"unicode/utf16"

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
	pathLiteral := "'" + strings.ReplaceAll(helper, "'", "''") + "'"
	script := "$ErrorActionPreference = 'Stop'; $path = " + pathLiteral + `; $deadline = [DateTime]::UtcNow.AddMinutes(3); while ([DateTime]::UtcNow -lt $deadline) { try { [IO.File]::Delete($path) } catch { }; if (-not [IO.File]::Exists($path)) { exit 0 }; Start-Sleep -Milliseconds 500 }; Write-Error 'timed out deleting update helper'; exit 1`
	encoded := utf16.Encode([]rune(script))
	bytes := make([]byte, len(encoded)*2)
	for i, unit := range encoded {
		binary.LittleEndian.PutUint16(bytes[i*2:], unit)
	}
	command := exec.Command("powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-WindowStyle", "Hidden", "-EncodedCommand", base64.StdEncoding.EncodeToString(bytes))
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	command.Stdin, command.Stdout, command.Stderr = nil, os.Stdout, os.Stderr
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}

func reexecBackground(string) error {
	return errors.New("re-executing a background process is unsupported on Windows")
}
