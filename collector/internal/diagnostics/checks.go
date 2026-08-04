package diagnostics

import "fmt"

func derive(snapshot *Snapshot) []Check {
	checks := make([]Check, 0, 8)
	noSessions := true
	for _, source := range snapshot.Sources {
		checks = append(checks, sourceCheck(source, snapshot.countsError))
		if source.Sessions > 0 {
			noSessions = false
		}
	}
	if noSessions {
		checks = append(checks, Check{
			ID:     "sources.none",
			Title:  "Agent sessions",
			Status: StatusFail,
			Detail: "No Claude Code or Codex sessions were found on this machine.",
			Fix:    "Run claude or codex in a repo for one turn, then re-run these checks.",
		})
	}
	for _, source := range snapshot.Sources {
		if source.Transcripts > 0 && !source.CLI.Found {
			checks = append(checks, Check{
				ID:     "cli." + source.Agent,
				Title:  source.Label + " CLI",
				Status: StatusWarn,
				Detail: source.CLI.Name + " is not on PATH; sessions remain browsable.",
				Fix:    "Install the CLI or add it to PATH to resume sessions.",
			})
		}
	}
	storage := Check{ID: "storage", Title: "coSlash storage", Status: StatusOK, Detail: "Writable at " + snapshot.Storage.Home}
	if !snapshot.Storage.Writable {
		storage.Status = StatusFail
		storage.Detail = "coSlash cannot write to " + snapshot.Storage.Home + ": " + snapshot.Storage.Error
		storage.Fix = "Check that the directory exists and is owned by your user."
	} else if snapshot.Storage.Error != "" {
		storage.Status = StatusWarn
		storage.Detail = "Writable at " + snapshot.Storage.Home + ", but summaries could not be counted: " + snapshot.Storage.Error
	}
	checks = append(checks, storage)

	synthesis := Check{ID: "synthesis", Title: "Session debriefs", Status: StatusOK}
	if !snapshot.Synthesis.Enabled {
		synthesis.Detail = "Disabled; coSlash will show deterministic transcript details only."
	} else if !snapshot.Synthesis.CLIFound {
		synthesis.Status = StatusWarn
		synthesis.Detail = "Enabled, but the Claude CLI is not on PATH."
		synthesis.Fix = "Install Claude Code or add claude to PATH."
	} else {
		synthesis.Detail = "Enabled with " + snapshot.Synthesis.Model + "."
	}
	checks = append(checks, synthesis)

	if !snapshot.Platform.TerminalLaunchSupported {
		checks = append(checks, Check{
			ID:     "platform.terminal",
			Title:  "Resume in terminal",
			Status: StatusWarn,
			Detail: "Browsing works on " + snapshot.Platform.OS + ", but opening a terminal is not supported.",
			Fix:    "Copy a handoff and resume the session manually.",
		})
	}
	return checks
}

func sourceCheck(source Source, countsError string) Check {
	check := Check{ID: "source." + source.Agent, Title: source.Label + " sessions", Status: StatusOK}
	switch {
	case source.State == SourceUnreadable:
		check.Status = StatusFail
		check.Detail = fmt.Sprintf("Could not fully scan %s: %s", source.Root, source.Error)
		check.Fix = "Run ls -la " + source.Root + " and check ownership."
	case source.State == SourceMissing:
		check.Status = StatusWarn
		check.Detail = "No " + source.Root + " directory; " + source.Label + " sessions will not appear."
		check.Fix = "Install " + source.Label + " and run it once in a repo, then re-run these checks."
	case source.State == SourceEmpty:
		check.Status = StatusWarn
		check.Detail = source.Root + " exists, but no sessions have been recorded yet."
		check.Fix = "Run " + source.CLI.Name + " in a repo for one turn, then re-run these checks."
	case countsError != "" && source.Transcripts > 0:
		check.Status = StatusFail
		check.Detail = fmt.Sprintf("Found %d transcripts, but session collection failed: %s", source.Transcripts, countsError)
		check.Fix = "Check the transcript scan errors and re-run these checks."
	case source.Transcripts > 0 && source.Sessions == 0:
		check.Status = StatusFail
		check.Detail = fmt.Sprintf("Found %d transcripts in %s, but no sessions could be displayed.", source.Transcripts, source.Root)
		check.Fix = "Check transcript formats and whether subagent transcripts still have their parent sessions."
	case source.SkippedTotal > 0:
		check.Status = StatusWarn
		check.Detail = fmt.Sprintf("%d sessions from %d transcripts; skipped %d unreadable paths in %s.", source.Sessions, source.Transcripts, source.SkippedTotal, source.Root)
		if len(source.Skipped) > 0 {
			check.Detail += fmt.Sprintf(" First failure: %s: %s", source.Skipped[0].Path, source.Skipped[0].Error)
		}
		check.Fix = "Run ls -la " + source.Root + " and check ownership."
	default:
		check.Detail = fmt.Sprintf("%d sessions from %d transcripts in %s", source.Sessions, source.Transcripts, source.Root)
	}
	return check
}
