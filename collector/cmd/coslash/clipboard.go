package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

func copyToClipboard(ctx context.Context, value string) error {
	commands := [][]string{}
	switch runtime.GOOS {
	case "darwin":
		commands = [][]string{{"pbcopy"}}
	case "windows":
		commands = [][]string{{"powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "[Console]::InputEncoding = [Text.UTF8Encoding]::new($false); Set-Clipboard -Value ([Console]::In.ReadToEnd())"}}
	case "linux":
		commands = [][]string{{"wl-copy"}, {"xclip", "-selection", "clipboard"}, {"xsel", "--clipboard", "--input"}}
	}
	var lastErr error
	for _, command := range commands {
		if err := ctx.Err(); err != nil {
			return err
		}
		path, err := exec.LookPath(command[0])
		if err != nil {
			continue
		}
		runCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		cmd := exec.CommandContext(runCtx, path, command[1:]...)
		cmd.Stdin = strings.NewReader(value)
		err = cmd.Run()
		cancel()
		if err == nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		lastErr = fmt.Errorf("%s: %w", command[0], err)
	}
	if lastErr != nil {
		return lastErr
	}
	return errors.New("no clipboard command is available")
}
