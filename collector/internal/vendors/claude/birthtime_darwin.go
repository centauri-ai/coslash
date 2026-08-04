package claude

import (
	"os"
	"syscall"
)

// birthtime reports the file's creation time in Unix milliseconds. Only darwin
// exposes it on Stat_t, so other platforms get the fallback in birthtime_other.go.
func birthtime(info os.FileInfo) (int64, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return stat.Birthtimespec.Sec*1000 + stat.Birthtimespec.Nsec/1_000_000, true
}
