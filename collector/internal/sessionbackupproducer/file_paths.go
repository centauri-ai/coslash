package sessionbackupproducer

import (
	"path"
	"regexp"
	"strings"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
)

var windowsDrivePath = regexp.MustCompile(`^[A-Za-z]:[/\\]`)

func sanitizeRecordFilePaths(records []fullsessionv1.Record, home string) error {
	for index := range records {
		record := &records[index]
		for editIndex := range record.Session.FileEdits {
			edit := &record.Session.FileEdits[editIndex]
			edit.Path = safeFileEditPath(edit.Path, record.Session.WorkingDirectory, home)
		}
		frozen, err := fullsessionv1.Freeze(*record)
		if err != nil {
			return err
		}
		*record = frozen
	}
	return nil
}

func safeFileEditPath(file, workingDirectory, home string) string {
	filePath := cleanPortablePath(file)
	if !filePath.absolute {
		return filePath.value
	}
	if relative, ok := portableRelative(workingDirectory, file); ok {
		if relative == "." {
			return safePathLabel(filePath.value)
		}
		return relative
	}
	if relative, ok := portableRelative(home, file); ok {
		if relative == "." {
			return "~"
		}
		return "~/" + relative
	}
	return safePathLabel(filePath.value)
}

type portablePath struct {
	value    string
	absolute bool
	windows  bool
}

func cleanPortablePath(value string) portablePath {
	windows := windowsDrivePath.MatchString(value) || strings.HasPrefix(value, `\\`) || strings.HasPrefix(value, "//")
	normalized := strings.ReplaceAll(value, `\`, "/")
	abs := strings.HasPrefix(normalized, "/") || windowsDrivePath.MatchString(value)
	cleaned := path.Clean(normalized)
	if windows && windowsDrivePath.MatchString(value) && len(cleaned) == 2 {
		cleaned += "/"
	}
	return portablePath{value: cleaned, absolute: abs, windows: windows}
}

func portableRelative(root, candidate string) (string, bool) {
	rootPath, candidatePath := cleanPortablePath(root), cleanPortablePath(candidate)
	if !rootPath.absolute || !candidatePath.absolute || rootPath.windows != candidatePath.windows {
		return "", false
	}
	rootValue, candidateValue := rootPath.value, candidatePath.value
	if rootPath.windows {
		rootValue, candidateValue = strings.ToLower(rootValue), strings.ToLower(candidateValue)
	}
	if rootValue == candidateValue {
		return ".", true
	}
	prefix := strings.TrimSuffix(rootValue, "/") + "/"
	if rootValue == "/" || rootValue == "c:/" || (rootPath.windows && strings.HasSuffix(rootValue, ":/")) {
		prefix = rootValue
	}
	if !strings.HasPrefix(candidateValue, prefix) {
		return "", false
	}
	return candidatePath.value[len(prefix):], true
}

func safePathLabel(value string) string {
	label := path.Base(value)
	if label == "." || label == "/" || windowsDrivePath.MatchString(label) || strings.HasSuffix(label, ":") {
		return "file"
	}
	return label
}
