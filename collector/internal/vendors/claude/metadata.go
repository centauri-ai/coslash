package claude

import (
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

const idleStatusTTL = time.Hour

type liveSessionFile struct {
	PID             int    `json:"pid"`
	SessionID       string `json:"sessionId"`
	Status          string `json:"status"`
	Name            string `json:"name"`
	NameSource      string `json:"nameSource"`
	StatusUpdatedAt *int64 `json:"statusUpdatedAt"`
}

type jobStateFile struct {
	SessionID  string `json:"sessionId"`
	Name       string `json:"name"`
	NameSource string `json:"nameSource"`
}

type desktopSessionFile struct {
	CLISessionID string `json:"cliSessionId"`
	Title        string `json:"title"`
}

type metadataPaths struct {
	sessions string
	jobs     string
	desktop  string
}

type metadataName struct {
	name       string
	nameSource string
}

// LoadMetadata reads live sessions (~/.claude/sessions, pid-validated), background
// jobs (~/.claude/jobs), and desktop titles into the shared metadata shape.
// Names holds the metadata-side winner of the name precedence — live user-name
// → job user-name → desktop title → job name → live name; the transcript name
// (Parsed.Name) is resolveNames' fallback.
func LoadMetadata() (*vendors.SessionMetadata, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return loadMetadata(metadataPaths{
		sessions: filepath.Join(home, ".claude", "sessions"),
		jobs:     filepath.Join(home, ".claude", "jobs"),
		desktop: filepath.Join(
			home,
			"Library",
			"Application Support",
			"Claude",
			"claude-code-sessions",
		),
	}, time.Now(), session.IsProcessAlive)
}

func LoadRemoteMetadata(
	source vendors.ReadSource,
	home string,
	now time.Time,
	processAlive func(int) bool,
) (*vendors.SessionMetadata, error) {
	return loadRemoteMetadata(source, metadataPaths{
		sessions: filepath.Join(home, ".claude", "sessions"),
		jobs:     filepath.Join(home, ".claude", "jobs"),
	}, now, processAlive)
}

func loadRemoteMetadata(
	source vendors.ReadSource,
	paths metadataPaths,
	now time.Time,
	processAlive func(int) bool,
) (*vendors.SessionMetadata, error) {
	metadata, live, jobs, err := loadCoreMetadata(source, paths, now, processAlive)
	if err != nil {
		return nil, err
	}
	resolveMetadataNames(metadata, live, jobs, nil)
	return metadata, nil
}

func loadMetadata(
	paths metadataPaths,
	now time.Time,
	processAlive func(int) bool,
) (*vendors.SessionMetadata, error) {
	desktopTitles := map[string]string{}
	metadata, live, jobs, err := loadCoreMetadata(
		vendors.LocalReadSource, paths, now, processAlive,
	)
	if err != nil {
		return nil, err
	}

	err = filepath.WalkDir(
		paths.desktop,
		func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				if errors.Is(walkErr, fs.ErrNotExist) {
					return nil
				}
				if path == paths.desktop {
					return walkErr
				}
				log.Printf("%s: skipping unreadable desktop entry: %v", path, walkErr)
				return nil
			}
			relative, err := filepath.Rel(paths.desktop, path)
			if err != nil {
				return err
			}
			depth := strings.Count(relative, string(filepath.Separator))
			if entry.IsDir() && depth >= 2 {
				return fs.SkipDir
			}
			if !entry.Type().IsRegular() || depth != 2 ||
				!strings.HasSuffix(entry.Name(), ".json") {
				return nil
			}
			var desktop desktopSessionFile
			ok, err := session.ReadJSONIfValid(path, &desktop)
			if err != nil {
				log.Printf("%s: skipping unreadable desktop metadata: %v", path, err)
				return nil
			}
			if ok && desktop.CLISessionID != "" && desktop.Title != "" {
				desktopTitles[desktop.CLISessionID] = desktop.Title
			}
			return nil
		},
	)
	if err != nil {
		return nil, err
	}

	resolveMetadataNames(metadata, live, jobs, desktopTitles)
	return metadata, nil
}

func loadCoreMetadata(
	source vendors.ReadSource,
	paths metadataPaths,
	now time.Time,
	processAlive func(int) bool,
) (*vendors.SessionMetadata, map[string]metadataName, map[string]metadataName, error) {
	live := map[string]metadataName{}
	jobs := map[string]metadataName{}
	metadata := vendors.EmptySessionMetadata()
	entries, err := readSourceDirIfExists(source, paths.sessions)
	if err != nil {
		return nil, nil, nil, err
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		var record liveSessionFile
		path := filepath.Join(paths.sessions, entry.Name())
		ok, err := vendors.ReadJSONSource(source, path, &record)
		if err != nil {
			log.Printf("%s: skipping unreadable session metadata: %v", path, err)
			continue
		}
		if !ok || record.SessionID == "" {
			continue
		}
		status, ok := validatedLiveStatus(record, processAlive(record.PID), now)
		if !ok {
			continue
		}
		metadata.Live[record.SessionID] = status
		live[record.SessionID] = metadataName{name: record.Name, nameSource: record.NameSource}
	}

	entries, err = readSourceDirIfExists(source, paths.jobs)
	if err != nil {
		return nil, nil, nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		var job jobStateFile
		path := filepath.Join(paths.jobs, entry.Name(), "state.json")
		ok, err := vendors.ReadJSONSource(source, path, &job)
		if err != nil {
			log.Printf("%s: skipping unreadable job metadata: %v", path, err)
			continue
		}
		if ok && job.SessionID != "" && job.Name != "" {
			jobs[job.SessionID] = metadataName{name: job.Name, nameSource: job.NameSource}
		}
	}
	return metadata, live, jobs, nil
}

func resolveMetadataNames(
	metadata *vendors.SessionMetadata,
	live map[string]metadataName,
	jobs map[string]metadataName,
	desktopTitles map[string]string,
) {
	ids := map[string]struct{}{}
	for id := range live {
		ids[id] = struct{}{}
	}
	for id := range jobs {
		ids[id] = struct{}{}
	}
	for id := range desktopTitles {
		ids[id] = struct{}{}
	}
	for id := range ids {
		liveEntry, jobEntry := live[id], jobs[id]
		var name string
		switch {
		case liveEntry.nameSource == "user" && liveEntry.name != "":
			name = liveEntry.name
		case jobEntry.nameSource == "user" && jobEntry.name != "":
			name = jobEntry.name
		case desktopTitles[id] != "":
			name = desktopTitles[id]
		case jobEntry.name != "":
			name = jobEntry.name
		default:
			name = liveEntry.name
		}
		if name != "" {
			metadata.Names[id] = name
		}
	}
}

func validatedLiveStatus(record liveSessionFile, alive bool, now time.Time) (string, bool) {
	if !alive {
		return "", false
	}
	status := record.Status
	if status == "" {
		status = "interactive"
	}
	if status == "idle" && record.StatusUpdatedAt != nil &&
		now.Sub(time.UnixMilli(*record.StatusUpdatedAt)) > idleStatusTTL {
		return "", false
	}
	return status, true
}

func readSourceDirIfExists(source vendors.ReadSource, dir string) ([]fs.DirEntry, error) {
	entries, err := source.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return entries, err
}
