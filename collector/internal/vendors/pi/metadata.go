package pi

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

type RuntimeRecord struct {
	Version              int     `json:"version"`
	RuntimeID            string  `json:"runtimeId"`
	PID                  int     `json:"pid"`
	ProcessStartIdentity string  `json:"processStartIdentity"`
	StartedAtMs          int64   `json:"startedAtMs"`
	SessionID            string  `json:"sessionId"`
	TranscriptPath       string  `json:"transcriptPath"`
	LeafID               *string `json:"leafId"`
	WorkState            string  `json:"workState"`
	DialogOpen           bool    `json:"dialogOpen"`
	Sequence             int64   `json:"sequence"`
	UpdatedAtMs          int64   `json:"updatedAtMs"`
}
type runtimeEvidence struct {
	Record RuntimeRecord `json:"record"`
	Exited bool          `json:"exited"`
}

func stateHome() string {
	if value := os.Getenv("COSLASH_HOME"); value != "" {
		return value
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".coslash")
}
func validRecord(r RuntimeRecord) bool {
	return r.Version == 1 && r.RuntimeID != "" && r.PID > 0 && r.StartedAtMs > 0 && r.SessionID != "" && filepath.IsAbs(r.TranscriptPath) && r.Sequence > 0 && r.UpdatedAtMs > 0 && (r.WorkState == "busy" || r.WorkState == "idle" || r.WorkState == "unknown")
}
func runtimeRecords() ([]runtimeEvidence, error) {
	byOwnerPath := map[string]runtimeEvidence{}
	for _, kind := range []string{"pi-history", "pi-runtime"} {
		entries, err := os.ReadDir(filepath.Join(stateHome(), kind))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
				continue
			}
			data, err := os.ReadFile(filepath.Join(stateHome(), kind, entry.Name()))
			if err != nil {
				continue
			}
			var evidence runtimeEvidence
			if kind == "pi-runtime" {
				err = json.Unmarshal(data, &evidence.Record)
			} else {
				err = json.Unmarshal(data, &evidence)
			}
			var raw map[string]json.RawMessage
			recordData := data
			if kind == "pi-history" {
				var envelope map[string]json.RawMessage
				if json.Unmarshal(data, &envelope) != nil || envelope["exited"] == nil {
					continue
				}
				recordData = envelope["record"]
			}
			if json.Unmarshal(recordData, &raw) != nil {
				continue
			}
			complete := true
			for _, field := range []string{"version", "runtimeId", "pid", "processStartIdentity", "startedAtMs", "sessionId", "transcriptPath", "leafId", "workState", "dialogOpen", "sequence", "updatedAtMs"} {
				if raw[field] == nil || (field != "leafId" && string(raw[field]) == "null") {
					complete = false
				}
			}
			if kind == "pi-runtime" && entry.Name() != evidence.Record.RuntimeID+".json" {
				continue
			}
			if err != nil || !complete || !validRecord(evidence.Record) {
				continue
			}
			key := evidence.Record.RuntimeID + "\x00" + evidence.Record.TranscriptPath
			previous, ok := byOwnerPath[key]
			if !ok || previous.Record.Sequence < evidence.Record.Sequence || (previous.Record.Sequence == evidence.Record.Sequence && !evidence.Exited) {
				byOwnerPath[key] = evidence
			}
		}
	}
	result := make([]runtimeEvidence, 0, len(byOwnerPath))
	for _, evidence := range byOwnerPath {
		result = append(result, evidence)
	}
	return result, nil
}

// ownerState is live, dead, or unknown. A reused PID proves the recorded owner died.
func ownerState(e runtimeEvidence) string {
	if e.Exited {
		return "dead"
	}
	if !session.IsProcessAlive(e.Record.PID) {
		if processAbsent(e.Record.PID) {
			return "dead"
		}
		return "unknown"
	}
	if e.Record.ProcessStartIdentity == "" {
		return "unknown"
	}
	identity, err := ProcessStartIdentity(e.Record.PID)
	if err != nil {
		return "unknown"
	}
	if identity != e.Record.ProcessStartIdentity {
		return "dead"
	}
	return "live"
}
func statusFor(owners []runtimeEvidence, verify func(runtimeEvidence) string) string {
	busy, idle, dead, unknown := false, false, false, false
	for _, owner := range owners {
		switch verify(owner) {
		case "live":
			if owner.Record.DialogOpen {
				return "waiting"
			}
			switch owner.Record.WorkState {
			case "busy":
				busy = true
			case "idle":
				idle = true
			default:
				unknown = true
			}
		case "dead":
			dead = true
		default:
			unknown = true
		}
	}
	if busy {
		return "busy"
	}
	if unknown {
		return "unknown"
	}
	if idle {
		return "idle"
	}
	if dead {
		return "inactive"
	}
	return "unknown"
}
func LoadMetadata() (*vendors.SessionMetadata, error) {
	return LoadMetadataContext(context.Background())
}
func LoadMetadataContext(ctx context.Context) (*vendors.SessionMetadata, error) {
	snapshot, err := snapshotContext(ctx)
	var records []runtimeEvidence
	if snapshot != nil {
		records = snapshot.records
	}
	if err != nil {
		return nil, err
	}
	grouped := map[string][]runtimeEvidence{}
	for _, record := range records {
		grouped[record.Record.SessionID] = append(grouped[record.Record.SessionID], record)
	}
	metadata := vendors.EmptySessionMetadata()
	for id, owners := range grouped {
		metadata.Session(id).Live = statusFor(owners, func(e runtimeEvidence) string { return snapshot.states[runtimeOwnerKey(e)] })
	}
	return metadata, nil
}

// RuntimeTranscriptPaths includes retained exited owners; it is independent of liveness.
func RuntimeTranscriptPaths() ([]string, error) {
	return RuntimeTranscriptPathsContext(context.Background())
}
func RuntimeTranscriptPathsContext(ctx context.Context) ([]string, error) {
	snapshot, err := snapshotContext(ctx)
	var records []runtimeEvidence
	if snapshot != nil {
		records = snapshot.records
	}
	if err != nil {
		return nil, err
	}
	paths := map[string]bool{}
	for _, record := range records {
		paths[record.Record.TranscriptPath] = true
	}
	result := make([]string, 0, len(paths))
	for path := range paths {
		result = append(result, path)
	}
	return result, nil
}

func canonicalTranscriptPath(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
		return resolved
	}
	return absolute
}

type runtimeSnapshotKey struct{}

// RuntimeSnapshot is request-scoped: one disk read and owner verification per refresh.
type RuntimeSnapshot struct {
	records []runtimeEvidence
	owners  map[string][]runtimeEvidence
	states  map[string]string
}

func runtimeOwnerKey(e runtimeEvidence) string {
	return e.Record.RuntimeID + "\x00" + e.Record.TranscriptPath
}
func LoadRuntimeSnapshot() (*RuntimeSnapshot, error) {
	records, err := runtimeRecords()
	if err != nil {
		return nil, err
	}
	snapshot := &RuntimeSnapshot{records: records, owners: map[string][]runtimeEvidence{}, states: map[string]string{}}
	processStates := map[string]string{}
	for _, record := range records {
		key := record.Record.SessionID + "\x00" + canonicalTranscriptPath(record.Record.TranscriptPath)
		snapshot.owners[key] = append(snapshot.owners[key], record)
		state := "dead"
		if !record.Exited {
			processKey := strconv.Itoa(record.Record.PID) + "\x00" + record.Record.ProcessStartIdentity
			var found bool
			state, found = processStates[processKey]
			if !found {
				state = ownerState(record)
				processStates[processKey] = state
			}
		}
		snapshot.states[runtimeOwnerKey(record)] = state
	}
	return snapshot, nil
}
func snapshotContext(ctx context.Context) (*RuntimeSnapshot, error) {
	if snapshot, ok := ctx.Value(runtimeSnapshotKey{}).(*RuntimeSnapshot); ok {
		if snapshot == nil {
			return nil, errors.New("Pi runtime snapshot unavailable")
		}
		return snapshot, nil
	}
	return LoadRuntimeSnapshot()
}
func WithRuntimeSnapshot(ctx context.Context, snapshot *RuntimeSnapshot) context.Context {
	return context.WithValue(ctx, runtimeSnapshotKey{}, snapshot)
}
func (s *RuntimeSnapshot) Leaf(id, path string) (string, bool) {
	leaf, found := "", false
	for _, record := range s.owners[id+"\x00"+canonicalTranscriptPath(path)] {
		if s.states[runtimeOwnerKey(record)] != "live" {
			continue
		}
		if record.Record.LeafID == nil {
			return "", false
		}
		if found && leaf != *record.Record.LeafID {
			return "", false
		}
		leaf, found = *record.Record.LeafID, true
	}
	return leaf, found
}
func runtimeLeafContext(ctx context.Context, id, path string) (string, bool) {
	if snapshot, ok := ctx.Value(runtimeSnapshotKey{}).(*RuntimeSnapshot); ok {
		if snapshot == nil {
			return "", false
		}
		return snapshot.Leaf(id, path)
	}
	return RuntimeLeaf(id, path)
}

// RuntimeLeaf returns evidence only when verified live owners agree on the leaf.
func RuntimeLeaf(id, path string) (string, bool) {
	snapshot, err := LoadRuntimeSnapshot()
	if err != nil {
		return "", false
	}
	return snapshot.Leaf(id, path)
}

// ConfiguredSessionRoots discovers configured roots, never arbitrary unobserved directories.
func ConfiguredSessionRoots() []string {
	roots := filepath.SplitList(os.Getenv("COSLASH_PI_SESSION_ROOTS"))
	if root := os.Getenv("PI_CODING_AGENT_SESSION_DIR"); root != "" {
		roots = append(roots, root)
	}
	root, err := Root()
	var data []byte
	if err == nil {
		data, err = os.ReadFile(filepath.Join(filepath.Dir(root), "settings.json"))
	}
	if err == nil {
		var settings struct {
			SessionDir string `json:"sessionDir"`
		}
		if json.Unmarshal(data, &settings) == nil && settings.SessionDir != "" {
			roots = append(roots, settings.SessionDir)
		}
	}
	result := []string{}
	for _, root := range roots {
		if root != "" {
			if absolute, err := ResolveDirectory(root); err == nil {
				result = append(result, absolute)
			}
		}
	}
	return result
}
