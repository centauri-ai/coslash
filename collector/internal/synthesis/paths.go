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
		if err := protectSynthesisDirectory(directory); err != nil {
			return err
		}
	}
	return nil
}

func protectSynthesisDirectories(directory string) error {
	if err := protectSynthesisDirectory(settings.Home()); err != nil {
		return err
	}
	if err := protectSynthesisDirectory(filepath.Dir(directory)); err != nil {
		return err
	}
	return protectSynthesisDirectory(directory)
}
