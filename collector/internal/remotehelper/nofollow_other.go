//go:build !unix

package remotehelper

// The helper ships for Linux. Elsewhere the open flag is unavailable, and the
// lstat check plus the confined root handle remain the symlink boundary.
const openNoFollow = 0

func isSymlinkOpenErr(error) bool { return false }
