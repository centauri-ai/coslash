package opencode

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

// ParserVersion identifies this exporter's parse output. Bumping it
// invalidates only OpenCode entries in the local parse cache.
const ParserVersion = "opencode-1"

type cachedTask struct {
	Name   string `json:"name,omitempty"`
	Status string `json:"status,omitempty"`
}

type cachedExtra struct {
	Tasks map[string]cachedTask `json:"tasks,omitempty"`
}

type cachedFamily struct {
	Members []json.RawMessage `json:"members"`
}

// familyKey names a family by its root and the update times of every stored
// member, which OpenCode advances whenever a session's messages change.
func familyKey(familyID string, rows []storedSession) vendors.CacheKey {
	parts := make([]string, 0, len(rows))
	for _, row := range rows {
		parts = append(parts, row.id+":"+strconv.FormatInt(row.updatedAt, 10)+":"+strconv.FormatBool(row.v2))
	}
	return vendors.CacheKey{Agent: vendors.AgentOpenCode, Identity: familyID, Version: ParserVersion, Fingerprint: strings.Join(parts, ";")}
}

func encodeCachedFamily(family []parsedSession) (json.RawMessage, error) {
	encoded := cachedFamily{Members: make([]json.RawMessage, 0, len(family))}
	for _, item := range family {
		extra := cachedExtra{}
		if len(item.tasks) > 0 {
			extra.Tasks = make(map[string]cachedTask, len(item.tasks))
			for id, task := range item.tasks {
				extra.Tasks[id] = cachedTask{Name: task.name, Status: task.status}
			}
		}
		member, err := vendors.EncodeParsedSession(item.transcript, extra)
		if err != nil {
			return nil, err
		}
		encoded.Members = append(encoded.Members, member)
	}
	return json.Marshal(encoded)
}

func decodeCachedFamily(payload json.RawMessage) ([]parsedSession, error) {
	var encoded cachedFamily
	if err := json.Unmarshal(payload, &encoded); err != nil {
		return nil, err
	}
	family := make([]parsedSession, 0, len(encoded.Members))
	for _, member := range encoded.Members {
		var extra cachedExtra
		transcript, err := vendors.DecodeParsedSession(member, &extra)
		if err != nil {
			return nil, err
		}
		if transcript == nil {
			return nil, errMalformedSession
		}
		item := parsedSession{transcript: transcript, tasks: map[string]taskLink{}}
		for id, task := range extra.Tasks {
			item.tasks[id] = taskLink{name: task.Name, status: task.Status}
		}
		family = append(family, item)
	}
	return family, nil
}
