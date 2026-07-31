package session

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
)

// ParseJSONL decodes a JSONL file (one JSON value per line) into a slice of T.
func ParseJSONL[T any](path string) ([]T, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	var records []T
	for {
		var record T
		err := decoder.Decode(&record)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				break
			}
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

func ReadJSONIfValid(path string, value any) (bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(data, value); err != nil {
		return false, nil
	}
	return true, nil
}
