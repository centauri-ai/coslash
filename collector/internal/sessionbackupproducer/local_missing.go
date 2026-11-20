package sessionbackupproducer

import (
	"context"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/claude"
	"github.com/centauri-ai/coslash/collector/internal/vendors/codex"
	sessionbackupv1 "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
)

// MissingLocalFamilies checks queued Codex and Claude identities against one
// complete source scan per agent. An uncertain scan never proves absence.
func (manager *Manager) MissingLocalFamilies(ctx context.Context, selections []Selection) (map[Selection]bool, error) {
	missing := make(map[Selection]bool)
	for _, agent := range []string{vendors.AgentCodex, vendors.AgentClaude} {
		var selected []Selection
		for _, selection := range selections {
			if selection.SourceKind == sessionbackupv1.SourceLocal && selection.Agent == agent {
				selected = append(selected, selection)
			}
		}
		if len(selected) == 0 {
			continue
		}
		handle, err := manager.openSource(ctx, selected[0])
		if err != nil {
			return nil, err
		}
		if handle.Close != nil {
			defer handle.Close()
		}
		if handle.Source == nil || handle.Home == "" {
			return nil, vendors.ErrInvalidData
		}
		present := make(map[string]bool)
		if agent == vendors.AgentCodex {
			var files []string
			for _, root := range []string{codex.SessionsRoot(handle.Home), codex.ArchivedDir(handle.Home)} {
				scan, err := codex.ScanSourceContext(ctx, handle.Source, root)
				if err != nil {
					return nil, err
				}
				if scan.SkippedTotal != 0 {
					return nil, vendors.ErrInvalidData
				}
				files = append(files, scan.Files...)
			}
			headers, err := codex.HeadersSourceContext(ctx, handle.Source, files)
			if err != nil {
				return nil, err
			}
			for _, header := range headers {
				if header.Err != nil {
					return nil, header.Err
				}
			}
			for _, id := range codex.FamilyRoots(headers) {
				present[id] = true
			}
		} else {
			scan, err := claude.Scan(ctx, handle.Source, claude.ProjectsRoot(handle.Home))
			if err != nil {
				return nil, err
			}
			if scan.SkippedTotal != 0 {
				return nil, vendors.ErrInvalidData
			}
			for _, file := range scan.Files {
				present[claude.FamilyIDFromPath(file)] = true
			}
		}
		for _, selection := range selected {
			if !present[selection.SessionID] {
				missing[selection] = true
			}
		}
	}
	return missing, ctx.Err()
}
