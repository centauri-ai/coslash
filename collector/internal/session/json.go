package session

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
)

// WalkJSONL decodes a JSONL file one value at a time. A torn final value from
// a live-appended transcript is ignored, matching the collector's prior
// ParseJSONL behavior without retaining every decoded row.
func WalkJSONL[T any](path string, visit func(T) error) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	for {
		var record T
		err := decoder.Decode(&record)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				break
			}
			return err
		}
		if err := visit(record); err != nil {
			return err
		}
	}
	return nil
}

func ReadFirstJSONL[T any](path string) (T, error) {
	var record T
	file, err := os.Open(path)
	if err != nil {
		return record, err
	}
	defer file.Close()
	if err := json.NewDecoder(file).Decode(&record); err != nil {
		return record, err
	}
	return record, nil
}

// ParseJSONL remains for small sidecar journals whose complete contents are
// needed together.
func ParseJSONL[T any](path string) ([]T, error) {
	var records []T
	err := WalkJSONL(path, func(record T) error {
		records = append(records, record)
		return nil
	})
	return records, err
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
