package vendors

import (
	"errors"
	"io/fs"
	"log"
	"path/filepath"
	"strings"
)

func JSONLFilesUnder(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			log.Printf("transcript scan %q: %v; skipping", path, err)
			return nil
		}
		if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".jsonl") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}
