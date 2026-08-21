package remote

import (
	"errors"
	"time"
)

const (
	DefaultConnectTimeout = 8 * time.Second
	DefaultDeadline       = 30 * time.Second
	DefaultMaxFileBytes   = 32 << 20
	DefaultMaxTotalBytes  = 128 << 20
	DefaultMaxEntries     = 10_000
	DefaultMaxDepth       = 16
	DefaultMaxStderrBytes = 8 << 10

	FreshnessInterval   = 3 * time.Minute
	InitialRetryBackoff = 3 * time.Minute
	MaxRetryBackoff     = 30 * time.Minute
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
