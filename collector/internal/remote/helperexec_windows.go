package remote

import (
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var windowsProcessJobs sync.Map
var ntResumeProcess = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")

const windowsProcessGroupTerminationExit = 0x5a5a0001

func startProcessGroup(cmd *exec.Cmd) error {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return err
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)),
		uint32(unsafe.Sizeof(limits)),
	); err != nil {
		windows.CloseHandle(job)
		return err
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	if err := cmd.Start(); err != nil {
		windows.CloseHandle(job)
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
		windows.CloseHandle(job)
		return fmt.Errorf("assign process job: %w", err)
	}
	if err := resumeProcess(cmd.Process.Pid); err != nil {
		_ = windows.TerminateJobObject(job, 1)
		_ = cmd.Wait()
		windows.CloseHandle(job)
		return fmt.Errorf("resume process: %w", err)
	}
	windowsProcessJobs.Store(cmd, job)
	return nil
}

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

func waitProcessGroup(cmd *exec.Cmd) error {
	err := cmd.Wait()
	if value, ok := windowsProcessJobs.LoadAndDelete(cmd); ok {
		windows.CloseHandle(value.(windows.Handle))
	}
	return err
}

func terminateProcessGroup(cmd *exec.Cmd) {
	if value, ok := windowsProcessJobs.Load(cmd); ok {
		_ = windows.TerminateJobObject(value.(windows.Handle), windowsProcessGroupTerminationExit)
		return
	}
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

func processGroupTerminationExit(exitCode int) bool {
	return exitCode == windowsProcessGroupTerminationExit
}
