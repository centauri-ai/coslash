package launch

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/centauri-ai/coslash/collector/internal/agentexec"
	"golang.org/x/sys/unix"
)

// HeadlessMain supervises the agent independently of Local's process group.
// EOF on the inherited pipe also cancels tasks when Local dies unexpectedly.
func HeadlessMain(target, access string) int {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if (target != "claude" && target != "codex") || (access != "read-only" && access != "workspace-write") || unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0) != nil {
		return 125
	}
	syscall.CloseOnExec(3)
	parent := os.NewFile(3, "send-parent")
	defer parent.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _, _ = io.Copy(io.Discard, parent); cancel() }()
	cwd, err := os.Getwd()
	if err != nil || !utf8.ValidString(cwd) {
		return 125
	}
	cmd := agentexec.CommandContext(ctx, target, headlessArgs(target, access, cwd)...)
	configureReviewProcess(cmd)
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
	cmd.Env = append(cmd.Env, "PWD="+cwd)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return 125
	}
	err = cmd.Wait()
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		pid, waitErr := syscall.Wait4(-1, nil, syscall.WNOHANG, nil)
		if waitErr == syscall.ECHILD {
			break
		}
		if pid > 0 || waitErr == syscall.EINTR {
			continue
		}
		if waitErr != nil {
			return 125
		}
		if err := killSendChildren(); err != nil {
			return 125
		}
		select {
		case <-deadline.C:
			return 125
		case <-tick.C:
		}
	}
	if err == nil {
		return 0
	}
	if code := cmd.ProcessState.ExitCode(); code >= 0 {
		return code
	}
	return 125
}

// Only this dedicated subreaper can reap these children, so their PIDs cannot
// be reused between reading the kernel's child list and signalling them.
func killSendChildren() error {
	paths, err := filepath.Glob("/proc/self/task/*/children")
	if err != nil {
		return err
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		for _, field := range strings.Fields(string(data)) {
			pid, err := strconv.Atoi(field)
			if err != nil || pid <= 0 {
				return syscall.EINVAL
			}
			if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
				return err
			}
		}
	}
	return nil
}
