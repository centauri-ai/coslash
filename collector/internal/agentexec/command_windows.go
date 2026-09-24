package agentexec

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func CommandContext(ctx context.Context, bin string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, bin, args...)
}

func Run(cmd *exec.Cmd) error {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(job)
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return err
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	cmd.Cancel = func() error { return windows.TerminateJobObject(job, 1) }
	if err := cmd.Start(); err != nil {
		return err
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err == nil {
		err = windows.AssignProcessToJobObject(job, process)
		windows.CloseHandle(process)
	}
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("assign agent process job: %w", err)
	}
	if err := resumeProcess(cmd.Process.Pid); err != nil {
		_ = windows.TerminateJobObject(job, 1)
		_ = cmd.Wait()
		return fmt.Errorf("resume agent process: %w", err)
	}
	return cmd.Wait()
}

var ntResumeProcess = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")

func resumeProcess(pid int) error {
	process, err := windows.OpenProcess(windows.PROCESS_SUSPEND_RESUME, false, uint32(pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(process)
	status, _, _ := ntResumeProcess.Call(uintptr(process))
	if status != 0 {
		return windows.NTStatus(status)
	}
	return nil
}

func Output(cmd *exec.Cmd) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := Run(cmd)
	if exit, ok := err.(*exec.ExitError); ok {
		exit.Stderr = stderr.Bytes()
	}
	return stdout.Bytes(), err
}
