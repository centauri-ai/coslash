package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/centauri-ai/coslash/collector/internal/settings"
)

const (
	backgroundLogName  = "coslash.log"
	backgroundLogSize  = 5 << 20
	backgroundLogFiles = 5
)

func configureBackgroundLog() error {
	logDir := filepath.Join(settings.Home(), "logs")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(logDir, backgroundLogName)
	if err := rotateBackgroundLog(path); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return err
	}
	log.SetOutput(&rotatingLogWriter{path: path, file: file, size: info.Size()})
	return nil
}

type rotatingLogWriter struct {
	mu   sync.Mutex
	path string
	file *os.File
	size int64
}

func (writer *rotatingLogWriter) Write(data []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.size > 0 && writer.size+int64(len(data)) > backgroundLogSize {
		if err := writer.file.Close(); err != nil {
			return 0, err
		}
		if err := rotateBackgroundLog(writer.path); err != nil {
			return 0, err
		}
		file, err := os.OpenFile(writer.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return 0, err
		}
		writer.file, writer.size = file, 0
	}
	n, err := writer.file.Write(data)
	writer.size += int64(n)
	return n, err
}

func rotateBackgroundLog(path string) error {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) || err == nil && info.Size() < backgroundLogSize {
		return nil
	}
	if err != nil {
		return err
	}
	if err := os.Remove(path + fmt.Sprintf(".%d", backgroundLogFiles-1)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for i := backgroundLogFiles - 2; i >= 1; i-- {
		oldPath, newPath := path+fmt.Sprintf(".%d", i), path+fmt.Sprintf(".%d", i+1)
		if err := os.Rename(oldPath, newPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.Rename(path, path+".1"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
