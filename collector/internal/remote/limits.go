package remote

import (
	"errors"
	"time"
)

const (
	// A host that cannot complete an SSH handshake promptly is unavailable for
	// the board. Collection itself retains DefaultDeadline after it connects.
	DefaultConnectTimeout = 7 * time.Second
	// Helper activation is a small fixed command, unlike collection.
	DefaultHelperInstallTimeout = 15 * time.Second
	// A previously approved helper can update in the background after a new
	// coSlash release. Its SSH handshake remains bounded by DefaultConnectTimeout.
	DefaultHelperAutoUpdateTimeout = 2 * time.Minute
	// Covers Claude and Codex over one SFTP session; large remote trees need headroom.
	DefaultDeadline       = 3 * time.Minute
	DefaultMaxFileBytes   = 32 << 20
	DefaultMaxTotalBytes  = 128 << 20
	DefaultMaxEntries     = 10_000
	DefaultMaxDepth       = 16
	DefaultMaxStderrBytes = 8 << 10

	FreshnessInterval   = 3 * time.Minute
	RemoteRetryInterval = 3 * time.Minute
	ManualRetryCooldown = 2 * time.Second
	ActivityWindowMs    = 2 * 60_000
	MaxDiagnosticBytes  = 2 << 10
	MaxErrorCopyBytes   = 256
)

var (
	ErrInvalidAlias = errors.New("invalid SSH alias")
	ErrPathDenied   = errors.New("remote path is outside the read allowlist")
	ErrSymlink      = errors.New("remote symlinks are not followed")
	ErrFileLimit    = errors.New("remote file exceeds the byte limit")
	ErrTotalLimit   = errors.New("remote refresh exceeds the byte limit")
	ErrEntryLimit   = errors.New("remote refresh exceeds the directory entry limit")
	ErrDepthLimit   = errors.New("remote path exceeds the directory depth limit")
	ErrStderrLimit  = errors.New("SSH stderr exceeds the diagnostic limit")
)

type Limits struct {
	Deadline       time.Duration
	ConnectTimeout time.Duration
	MaxFileBytes   int64
	MaxTotalBytes  int64
	MaxEntries     int64
	MaxDepth       int
	MaxStderrBytes int
}

func (limits Limits) withDefaults() Limits {
	if limits.Deadline <= 0 {
		limits.Deadline = DefaultDeadline
	}
	if limits.ConnectTimeout <= 0 {
		limits.ConnectTimeout = DefaultConnectTimeout
	}
	if limits.MaxFileBytes <= 0 {
		limits.MaxFileBytes = DefaultMaxFileBytes
	}
	if limits.MaxTotalBytes <= 0 {
		limits.MaxTotalBytes = DefaultMaxTotalBytes
	}
	if limits.MaxEntries <= 0 {
		limits.MaxEntries = DefaultMaxEntries
	}
	if limits.MaxDepth <= 0 {
		limits.MaxDepth = DefaultMaxDepth
	}
	if limits.MaxStderrBytes <= 0 {
		limits.MaxStderrBytes = DefaultMaxStderrBytes
	}
	return limits
}
