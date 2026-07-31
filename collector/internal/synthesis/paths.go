package synthesis

import (
	"os"
	"path/filepath"
)

func Home() string {
	if home := os.Getenv("COSLASH_HOME"); home != "" {
		return home
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".coslash"
	}
	return filepath.Join(home, ".coslash")
}

func SummariesDir() string {
	return filepath.Join(Home(), "summaries")
}

func SynthesisCwd() string {
	return filepath.Join(Home(), "synthesis")
}

func EnsureDirs() error {
	if err := os.MkdirAll(SummariesDir(), 0o700); err != nil {
		return err
	}
	return os.MkdirAll(SynthesisCwd(), 0o700)
}

func TranscriptMtime(logPath string) (int64, error) {
	info, err := os.Stat(logPath)
	if err != nil {
		return 0, err
	}
	return info.ModTime().UnixMilli(), nil
}
