package cursor

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

var transcriptIDPattern = regexp.MustCompile(`^(?:agent-)?[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

func Root() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return ProjectsRoot(home), nil
}

func ProjectsRoot(home string) string {
	return filepath.Join(home, ".cursor", "projects")
}

func Files() ([]string, error) {
	root, err := Root()
	if err != nil {
		return nil, err
	}
	return FilesSource(vendors.LocalReadSource, root)
}

func FilesSource(source vendors.ReadSource, root string) ([]string, error) {
	files, err := vendors.JSONLFilesUnderSource(source, root)
	if err != nil {
		return nil, err
	}
	return filterTranscripts(files), nil
}

func Scan() (*vendors.SourceScan, error) {
	root, err := Root()
	if err != nil {
		return nil, err
	}
	return ScanSource(vendors.LocalReadSource, root)
}

func ScanSource(source vendors.ReadSource, root string) (*vendors.SourceScan, error) {
	scan, err := vendors.ScanSource(source, root)
	if err != nil {
		return nil, err
	}
	scan.Files = filterTranscripts(scan.Files)
	return scan, nil
}

func filterTranscripts(files []string) []string {
	result := make([]string, 0, len(files))
	for _, path := range files {
		if IsTranscript(path) {
			result = append(result, path)
		}
	}
	return result
}

func IsTranscript(path string) bool {
	if filepath.Ext(path) != ".jsonl" {
		return false
	}
	id := IDFromPath(path)
	if !transcriptIDPattern.MatchString(id) {
		return false
	}
	filenameID := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if !strings.EqualFold(filenameID, id) {
		return false
	}

	directory := filepath.Dir(path)
	if filepath.Base(filepath.Dir(directory)) == "agent-transcripts" {
		return strings.EqualFold(filepath.Base(directory), id) &&
			transcriptIDPattern.MatchString(filepath.Base(directory))
	}
	return ParentIDFromPath(path) != ""
}

func IDFromPath(path string) string {
	return strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
}

func ParentIDFromPath(path string) string {
	if filepath.Base(filepath.Dir(path)) != "subagents" {
		return ""
	}
	parentDir := filepath.Dir(filepath.Dir(path))
	if filepath.Base(filepath.Dir(parentDir)) != "agent-transcripts" {
		return ""
	}
	parentID := filepath.Base(parentDir)
	if !transcriptIDPattern.MatchString(parentID) {
		return ""
	}
	return parentID
}
