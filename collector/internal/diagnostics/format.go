package diagnostics

import (
	"fmt"
	"strings"
	"time"
)

// FormatForCopy renders a shareable diagnostics report without sensitive remote details.
func FormatForCopy(snapshot *Snapshot) string {
	if snapshot == nil {
		return ""
	}
	lines := []string{
		fmt.Sprintf("coSlash %s diagnostics", snapshot.Version),
		fmt.Sprintf("Generated: %s", time.UnixMilli(snapshot.GeneratedAt).UTC().Format(time.RFC3339)),
		fmt.Sprintf(
			"Platform: %s/%s; terminal launch=%t",
			snapshot.Platform.OS,
			snapshot.Platform.Arch,
			snapshot.Platform.TerminalLaunchSupported,
		),
		fmt.Sprintf("Storage: %s; writable=%t", snapshot.Storage.Home, snapshot.Storage.Writable),
		fmt.Sprintf(
			"Synthesis: enabled=%t; model=%s; CLI found=%t",
			snapshot.Synthesis.Enabled,
			orUnknown(snapshot.Synthesis.Model),
			snapshot.Synthesis.CLIFound,
		),
		"",
		"Sources:",
	}
	for _, source := range snapshot.Sources {
		lines = append(lines,
			fmt.Sprintf(
				"- %s: %s; root=%s; entries=%d; sessions=%d; skipped=%d",
				source.Label,
				source.State,
				source.Root,
				source.Entries,
				source.Sessions,
				source.SkippedTotal,
			),
			fmt.Sprintf(
				"  CLI: found=%t; path=%s; version=%s",
				source.CLI.Found,
				orUnknown(source.CLI.Path),
				orUnknown(source.CLI.Version),
			),
		)
		for _, skipped := range source.Skipped {
			lines = append(lines, "  Skipped: "+skipped.Error)
		}
	}
	if snapshot.Remote != nil {
		lines = append(lines, "", "Remote host:")
		lines = append(lines, formatRemoteFactLines(snapshot.Remote)...)
	}
	lines = append(lines, "", "Checks:")
	for _, check := range snapshot.Checks {
		lines = append(lines, fmt.Sprintf("- [%s] %s: %s", check.Status, check.Title, check.Detail))
		if check.Fix != "" {
			lines = append(lines, "  Fix: "+check.Fix)
		}
	}
	return strings.Join(lines, "\n")
}

func formatRemoteFactLines(remote *Remote) []string {
	lines := []string{
		fmt.Sprintf("- alias=%s; state=%s; complete=%t", remote.Label, remote.State, remote.Complete),
	}
	if remote.Reason != nil && *remote.Reason != "" {
		lines = append(lines, "  reason="+*remote.Reason)
	}
	if remote.CollectorVersion != "" || remote.SchemaVersion != "" {
		lines = append(lines, fmt.Sprintf(
			"  collector=%s; schema=%s",
			orUnknown(remote.CollectorVersion),
			orUnknown(remote.SchemaVersion),
		))
	}
	if len(remote.Capabilities) > 0 {
		lines = append(lines, "  capabilities="+strings.Join(remote.Capabilities, ","))
	}
	if len(remote.LaunchableAgents) > 0 {
		lines = append(lines, "  launchableAgents="+strings.Join(remote.LaunchableAgents, ","))
	}
	if remote.HostOS != "" || remote.HostArch != "" {
		lines = append(lines, fmt.Sprintf("  platform=%s/%s", orUnknown(remote.HostOS), orUnknown(remote.HostArch)))
	}
	if remote.LastSuccessAtMs != nil {
		lines = append(lines, "  lastSuccessAtMs="+fmt.Sprintf("%d", *remote.LastSuccessAtMs))
	}
	if remote.CoverageSinceMs != nil {
		lines = append(lines, "  coverageSinceMs="+fmt.Sprintf("%d", *remote.CoverageSinceMs))
	}
	if remote.ClockOffsetMs != nil {
		lines = append(lines, "  clockOffsetMs="+fmt.Sprintf("%d", *remote.ClockOffsetMs))
	}
	if remote.RoundTripMs != nil {
		lines = append(lines, "  roundTripMs="+fmt.Sprintf("%d", *remote.RoundTripMs))
	}
	if remote.NextRetryAtMs != nil {
		lines = append(lines, "  nextRetryAtMs="+fmt.Sprintf("%d", *remote.NextRetryAtMs))
	}
	if remote.Error != "" {
		lines = append(lines, "  error="+remote.Error)
	}
	if remote.DiagnosticStderr != "" {
		lines = append(lines, "  diagnosticStderr="+remote.DiagnosticStderr)
	}
	return lines
}

func orUnknown(value string) string {
	if value == "" {
		return "unknown"
	}
	return value
}
