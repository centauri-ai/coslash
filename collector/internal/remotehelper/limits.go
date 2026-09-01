// Package remotehelper implements the Linux-side collector the Mac drives over
// SSH exec. It parses transcripts beside the data and writes bounded protocol
// records to stdout. It keeps no state between runs, persists no transcript,
// opens no listener, and never resolves a path supplied by the Mac request.
package remotehelper

import (
	"errors"
	"time"
)

const (
	// MaxEntries bounds one collection's directory entries across both vendors.
	MaxEntries = 200_000
	// MaxDepth bounds traversal beneath an allowlisted root.
	MaxDepth = 16
	// MaxFileBytes bounds one transcript read. Local reads cost disk and CPU
	// rather than SSH bandwidth, so this is far above the SFTP per-file limit.
	MaxFileBytes = 512 << 20
	// CollectDeadline bounds one collect command end to end.
	CollectDeadline = 4 * time.Minute
	// UnstableRetries bounds re-parsing a family whose files moved under it.
	UnstableRetries = 2
)

// Capabilities is the uniquely sorted capability set the handshake advertises.
var Capabilities = []string{"claude", "codex", "inventory", "tombstones", "unchanged"}

var (
	ErrPathDenied  = errors.New("path is outside the helper read allowlist")
	ErrSymlink     = errors.New("helper does not follow symlinks")
	ErrNotRegular  = errors.New("path is not a regular file")
	ErrFileLimit   = errors.New("file exceeds the helper byte limit")
	ErrEntryLimit  = errors.New("collection exceeds the directory entry limit")
	ErrDepthLimit  = errors.New("path exceeds the directory depth limit")
	ErrRecordLimit = errors.New("record exceeds the response byte limit")
	ErrDeadline    = errors.New("collection deadline exceeded")
)

// Limits are the helper-owned traversal bounds. Response bounds arrive in the
// request; traversal bounds are never negotiable from the Mac side.
type Limits struct {
	MaxEntries   int64
	MaxDepth     int
	MaxFileBytes int64
}

func (limits Limits) withDefaults() Limits {
	if limits.MaxEntries <= 0 {
		limits.MaxEntries = MaxEntries
	}
	if limits.MaxDepth <= 0 {
		limits.MaxDepth = MaxDepth
	}
	if limits.MaxFileBytes <= 0 {
		limits.MaxFileBytes = MaxFileBytes
	}
	return limits
}
