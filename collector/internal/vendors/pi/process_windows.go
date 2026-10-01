//go:build windows

package pi

import (
	"errors"
	"strconv"

	"golang.org/x/sys/windows"
)

// ProcessStartIdentity matches PowerShell StartTime.ToFileTimeUtc without rounding.
func ProcessStartIdentity(pid int) (string, error) {
	if pid <= 0 || uint64(pid) > uint64(^uint32(0)) {
		return "", windows.ERROR_INVALID_PARAMETER
	}
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(process)
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(process, &created, &exited, &kernel, &user); err != nil {
		return "", err
	}
	ticks := uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime)
	return "windows:" + strconv.FormatUint(ticks, 10), nil
}

func processAbsent(pid int) bool {
	if pid <= 0 || uint64(pid) > uint64(^uint32(0)) {
		return true
	}
	process, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return errors.Is(err, windows.ERROR_INVALID_PARAMETER)
	}
	defer windows.CloseHandle(process)
	status, err := windows.WaitForSingleObject(process, 0)
	return err == nil && status == windows.WAIT_OBJECT_0
}
