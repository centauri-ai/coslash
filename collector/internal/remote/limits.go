package remote

import (
	"time"

	remoteviewv1 "github.com/centauri-ai/coslash/collector/remoteview/v1"
)

const (
	SnapshotDeadline    = 30 * time.Second
	SSHConnectTimeout   = 8 * time.Second
	MaxPrefixNoise      = 8 << 10
	MaxStderrBytes      = 8 << 10
	MaxFrameHeader      = 64
	MaxProbePayload     = 256 << 10
	MaxHandoffPayload   = 64 << 10
	FreshnessInterval   = 3 * time.Minute
	InitialRetryBackoff = 3 * time.Minute
	MaxRetryBackoff     = 30 * time.Minute
	ActivityWindowMs    = 2 * 60_000
	MaxDiagnosticBytes  = 2 << 10
	MaxErrorCopyBytes   = 256
)

// SnapshotStdoutCap is payload plus bounded prefix noise and frame header.
func SnapshotStdoutCap() int {
	return remoteviewv1.MaxPayloadBytes + MaxPrefixNoise + MaxFrameHeader
}

// ProbeStdoutCap is the probe payload budget plus framing and noise.
func ProbeStdoutCap() int {
	return MaxProbePayload + MaxPrefixNoise + MaxFrameHeader
}

// HandoffStdoutCap is the handoff response budget plus framing and noise.
func HandoffStdoutCap() int {
	return MaxHandoffPayload + MaxPrefixNoise + MaxFrameHeader
}
