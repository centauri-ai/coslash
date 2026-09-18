//go:build unix

package remote

import (
	"errors"
	"syscall"
)

const processGroupTestSupported = true

func processExited(pid int) bool {
	return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}
