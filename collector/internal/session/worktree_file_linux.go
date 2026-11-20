package session

import (
	"fmt"
	"golang.org/x/sys/unix"
)

func fileIdentity(path string) (string, error) {
	var s unix.Statx_t
	if err := unix.Statx(unix.AT_FDCWD, path, 0, unix.STATX_INO|unix.STATX_BTIME, &s); err != nil {
		var fallback unix.Stat_t
		if err = unix.Stat(path, &fallback); err != nil {
			return "", err
		}
		return fmt.Sprintf("%d:%d", fallback.Dev, fallback.Ino), nil
	}
	return fmt.Sprintf("%d:%d:%d:%d:%d", s.Dev_major, s.Dev_minor, s.Ino, s.Btime.Sec, s.Btime.Nsec), nil
}
