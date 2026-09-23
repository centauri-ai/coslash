package cursor

import (
	"crypto/md5"
	"encoding/hex"
	"os"
	"path/filepath"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

func ResumeDirectory(value *session.Session) string {
	home, _ := os.UserHomeDir()
	stores, _ := filepath.Glob(filepath.Join(home, ".cursor", "chats", "*", canonicalCursorID(value.ID), "store.db"))
	if len(stores) == 1 {
		wanted := filepath.Base(filepath.Dir(filepath.Dir(stores[0])))
		candidates := []string{value.WorkingDirectory}
		for _, edit := range value.FileEdits {
			path := edit.Path
			if path != "" && !filepath.IsAbs(path) {
				path = filepath.Join(value.WorkingDirectory, path)
			}
			candidates = append(candidates, filepath.Dir(path))
		}
		for _, candidate := range candidates {
			for dir := candidate; filepath.IsAbs(dir); dir = filepath.Dir(dir) {
				if sum := md5.Sum([]byte(dir)); hex.EncodeToString(sum[:]) == wanted {
					return dir
				}
				if dir == filepath.Dir(dir) {
					break
				}
			}
		}
	}
	return value.WorkingDirectory
}
