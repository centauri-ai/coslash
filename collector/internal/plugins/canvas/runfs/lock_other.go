//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd

package runfs

import (
	"context"
	"os"
)

// The process-wide keyed lock still serializes writers on platforms without a
// standard-library advisory file lock. Supported release platforms use flock.
func lockFile(ctx context.Context, _ *os.File) error { return context.Cause(ctx) }
func unlockFile(_ *os.File)                          {}
