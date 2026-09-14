package session

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
)

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
