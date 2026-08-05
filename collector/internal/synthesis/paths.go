package synthesis

import (
	"os"
	"path/filepath"

	"github.com/centauri-ai/coslash/collector/internal/settings"
)

func SummariesDir() string {
	return filepath.Join(settings.Home(), "summaries")
}

func SynthesisCwd() string {
	return filepath.Join(settings.Home(), "synthesis")
}

func EnsureDirs() error {
	for _, directory := range []string{settings.Home(), SummariesDir(), SynthesisCwd()} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return err
		}
		if err := os.Chmod(directory, 0o700); err != nil {
			return err
		}
	}
	return nil
}

func TranscriptMtime(logPath string) (int64, error) {
	info, err := os.Stat(logPath)
	if err != nil {
		return 0, err
	}
	return info.ModTime().UnixMilli(), nil
}
