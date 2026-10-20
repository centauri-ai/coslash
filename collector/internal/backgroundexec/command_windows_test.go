//go:build windows

package backgroundexec

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

func TestDetachedBackgroundCommandsHaveNoConsole(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestDetachedConsoleParent$")
	cmd.Env = append(os.Environ(), "COSLASH_CONSOLE_PARENT=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("detached parent: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "verified detached parent and child consoles") {
		t.Fatalf("missing verification: %s", out)
	}
}

func TestDetachedConsoleParent(t *testing.T) {
	if os.Getenv("COSLASH_CONSOLE_PARENT") != "1" {
		t.Skip("subprocess helper")
	}
	getConsoleWindow := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleWindow")
	if hwnd, _, _ := getConsoleWindow.Call(); hwnd != 0 {
		t.Fatal("parent has a console")
	}
	for _, noWindow := range []bool{false, true} {
		var cmd *exec.Cmd
		if noWindow {
			cmd = CommandContext(context.Background(), os.Args[0], "-test.run=^TestConsoleChild$")
		} else {
			cmd = exec.Command(os.Args[0], "-test.run=^TestConsoleChild$")
			// Keep the control's allocated console hidden on the developer's desktop.
			cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		}
		cmd.Env = append(os.Environ(), "COSLASH_CONSOLE_CHILD=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("child: %v %s", err, out)
		}
		var hwnd uint64
		if _, err := fmt.Sscanf(string(out), "console=%d", &hwnd); err != nil {
			t.Fatalf("missing child console handle: %v: %s", err, out)
		}
		if (hwnd == 0) != noWindow {
			t.Fatalf("noWindow=%t: %s", noWindow, out)
		}
	}
	fmt.Println("verified detached parent and child consoles")
}

func TestConsoleChild(t *testing.T) {
	if os.Getenv("COSLASH_CONSOLE_CHILD") != "1" {
		t.Skip("subprocess helper")
	}
	hwnd, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleWindow").Call()
	fmt.Printf("console=%d\n", hwnd)
}

func TestCanceledBackgroundCommand(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd := CommandContext(ctx, os.Args[0], "-test.run=^TestConsoleChild$")
	if err := cmd.Run(); err != context.Canceled {
		t.Fatalf("Run = %v", err)
	}
}
