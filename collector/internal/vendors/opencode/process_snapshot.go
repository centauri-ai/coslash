package opencode

import (
	"encoding/json"
	"fmt"
)

type windowsTUIProcess struct {
	PID         int    `json:"PID"`
	StartedAt   int64  `json:"StartedAt"`
	Executable  string `json:"Executable"`
	CommandLine string `json:"CommandLine"`
}

func decodeWindowsProcessSnapshot(output []byte, all bool) ([]windowsTUIProcess, error) {
	if len(output) > 4<<20 {
		return nil, fmt.Errorf("OpenCode process output exceeds limit")
	}
	var records []windowsTUIProcess
	if err := json.Unmarshal(output, &records); err != nil {
		return nil, err
	}
	if all && records == nil {
		return nil, fmt.Errorf("unverified OpenCode process snapshot")
	}
	if len(records) > 65536 {
		return nil, fmt.Errorf("OpenCode process count exceeds limit")
	}
	return records, nil
}
