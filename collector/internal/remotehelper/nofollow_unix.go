//go:build unix

package remotehelper

import (
	"errors"
	"syscall"
)

// openNoFollow refuses a final path component that became a symlink after it
// was inspected.
const openNoFollow = syscall.O_NOFOLLOW

// isSymlinkOpenErr recognises the refusal. Linux reports ELOOP; the BSDs report
// EMLINK for O_NOFOLLOW.
func isSymlinkOpenErr(err error) bool {
	return errors.Is(err, syscall.ELOOP) || errors.Is(err, syscall.EMLINK)
}
