package remote

import (
	"fmt"
	"os/exec"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var windowsProcessJobs sync.Map

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
	windowsProcessJobs.Store(cmd, job)
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
		_ = windows.TerminateJobObject(value.(windows.Handle), 1)
	}
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
