package diagnostics

import "fmt"

func derive(snapshot *Snapshot) []Check {
	checks := make([]Check, 0, 8)
	if snapshot.homeError != "" {
		checks = append(checks, Check{
			ID:     "home",
			Title:  "Home directory",
			Status: StatusFail,
			Detail: "coSlash could not resolve the home directory: " + snapshot.homeError,
			Fix:    "Check that the current user has a valid home directory.",
		})
	}
	noEntries := true
	scanFailed := false
	for _, source := range snapshot.Sources {
		checks = append(checks, sourceCheck(source))
		if source.Entries > 0 {
			noEntries = false
		}
		if source.State == SourceUnreadable {
			scanFailed = true
		}
		if source.Entries > 0 && !source.CLI.Found {
			checks = append(checks, Check{
				ID:     "cli." + source.Agent,
				Title:  source.Label + " CLI",
				Status: StatusWarn,
				Detail: source.CLI.Name + " is not on PATH; sessions remain browsable.",
				Fix:    "Install the CLI or add it to PATH to resume sessions.",
			})
		}
	}
	if noEntries && !scanFailed && snapshot.homeError == "" {
		checks = append(checks, Check{
			ID:     "sources.none",
			Title:  "Agent sessions",
			Status: StatusFail,
			Detail: "No supported agent sessions were found on this machine.",
			Fix:    "Run a supported agent in a repo for one turn, then re-run these checks.",
		})
	}
	storage := Check{ID: "storage", Title: "coSlash storage", Status: StatusOK, Detail: "Writable at " + snapshot.Storage.Home}
	if !snapshot.Storage.Writable {
		storage.Status = StatusFail
		storage.Detail = "coSlash cannot write to " + snapshot.Storage.Home + ": " + snapshot.Storage.Error
		storage.Fix = "Check that the directory exists and is owned by your user."
	}
	checks = append(checks, storage)

	synthesis := Check{ID: "synthesis", Title: "Session debriefs", Status: StatusOK}
	if snapshot.Synthesis.Error != "" {
		synthesis.Status = StatusFail
		synthesis.Detail = snapshot.Synthesis.Error
		synthesis.Fix = "Open Settings and repair settings.json."
	} else if !snapshot.Synthesis.Enabled {
		synthesis.Detail = "Disabled; coSlash will show deterministic transcript details only."
	} else if !snapshot.Storage.Writable {
		synthesis.Status = StatusFail
		synthesis.Detail = "Enabled, but coSlash storage is not writable."
		synthesis.Fix = "Repair coSlash storage permissions, then re-run these checks."
	} else if !snapshot.Synthesis.CLIFound {
		synthesis.Status = StatusWarn
		synthesis.Detail = "Enabled, but " + snapshot.Synthesis.Reason
		synthesis.Fix = "Install the selected synthesis CLI or add it to PATH."
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

func sourceCheck(source Source) Check {
	check := Check{ID: "source." + source.Agent, Title: source.Label + " sessions", Status: StatusOK}
	switch {
	case source.State == SourceUnreadable:
		check.Status = StatusFail
		if source.Root == "" {
			check.Detail = "Could not locate the session root: " + source.Error
			check.Fix = "Check that the home directory is available and readable."
		} else {
			check.Detail = fmt.Sprintf("Could not inspect %s: %s", source.Root, source.Error)
			check.Fix = "Run ls -la " + source.Root + " and check ownership."
		}
	case source.State == SourceMissing:
		check.Status = StatusWarn
		check.Detail = "No session source at " + source.Root + "; " + source.Label + " sessions will not appear."
		check.Fix = "Install " + source.Label + " and run it once in a repo, then re-run these checks."
	case source.State == SourceEmpty:
		check.Status = StatusWarn
		check.Detail = source.Root + " exists, but no sessions have been recorded yet."
		check.Fix = "Run " + source.CLI.Name + " in a repo for one turn, then re-run these checks."
	case source.Entries > 0 && source.Sessions == 0:
		check.Status = StatusFail
		check.Detail = fmt.Sprintf("Found %d source entries in %s, but no root sessions.", source.Entries, source.Root)
		check.Fix = "Check source formats and whether child sessions still have their parent sessions."
	case source.SkippedTotal > 0:
		check.Status = StatusWarn
		check.Detail = fmt.Sprintf("%d sessions; skipped %d unreadable entries in %s.", source.Sessions, source.SkippedTotal, source.Root)
		check.Fix = "Run ls -la " + source.Root + " and check ownership."
	default:
		check.Detail = fmt.Sprintf("%d sessions in %s", source.Sessions, source.Root)
	}
	return check
}
