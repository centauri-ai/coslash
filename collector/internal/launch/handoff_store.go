package launch

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

type handoffRecord struct {
	Agent     string `json:"agent"`
	SessionID string `json:"sessionId"`
	ExpiresAt int64  `json:"expiresAtUnix"`
	Body      string `json:"body"`
}

// PutHandoff stages a bounded UTF-8 brief bound to agent/session and returns an opaque ID.
func PutHandoff(agent, sessionID string, r io.Reader) (string, error) {
	if err := ValidateRemoteAgent(agent); err != nil {
		return "", err
	}
	if err := ValidateUUIDSessionID(sessionID); err != nil {
		return "", err
	}
	if err := CleanupHandoffs(); err != nil {
		return "", err
	}
	body, err := readHandoffBody(r)
	if err != nil {
		return "", err
	}
	if err := ensureHandoffDir(); err != nil {
		return "", err
	}
	id, err := newHandoffID()
	if err != nil {
		return "", err
	}
	record := handoffRecord{
		Agent:     agent,
		SessionID: sessionID,
		ExpiresAt: time.Now().Add(HandoffMaxAge).Unix(),
		Body:      handoffPreamble + body,
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return "", fmt.Errorf("launch: encoding handoff record: %w", err)
	}
	path := handoffPath(id)
	if err := writeHandoffAtomic(path, payload); err != nil {
		return "", err
	}
	return id, nil
}

// ClaimHandoff atomically claims a staged handoff for the bound agent/session.
func ClaimHandoff(agent, sessionID, id string) (body string, cleanup func() error, err error) {
	if err := ValidateRemoteAgent(agent); err != nil {
		return "", nil, err
	}
	if err := ValidateUUIDSessionID(sessionID); err != nil {
		return "", nil, err
	}
	if err := ValidateHandoffID(id); err != nil {
		return "", nil, err
	}
	path := handoffPath(id)
	claimed := claimedHandoffPath(id)
	record, err := readHandoffRecord(path)
	if err != nil {
		if errors.Is(err, ErrHandoffNotFound) {
			if _, statErr := os.Lstat(claimed); statErr == nil {
				return "", nil, ErrHandoffUsed
			}
		}
		return "", nil, err
	}
	if time.Now().Unix() >= record.ExpiresAt {
		return "", nil, ErrHandoffExpired
	}
	if record.Agent != agent || record.SessionID != sessionID {
		return "", nil, fmt.Errorf("%w: handoff binding mismatch", ErrInvalidInput)
	}
	if err := os.Rename(path, claimed); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if _, statErr := os.Lstat(claimed); statErr == nil {
				return "", nil, ErrHandoffUsed
			}
			return "", nil, ErrHandoffNotFound
		}
		return "", nil, fmt.Errorf("launch: claiming handoff: %w", err)
	}
	cleanup = func() error {
		if removeErr := os.Remove(claimed); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
			return removeErr
		}
		return nil
	}
	return record.Body, cleanup, nil
}

func readHandoffBody(r io.Reader) (string, error) {
	limited := io.LimitReader(r, int64(MaxHandoffBytes)+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return "", fmt.Errorf("%w: reading handoff: %v", ErrInvalidInput, err)
	}
	if len(data) > MaxHandoffBytes {
		return "", fmt.Errorf("%w: handoff exceeds %d bytes", ErrInvalidInput, MaxHandoffBytes)
	}
	if !utf8.Valid(data) {
		return "", fmt.Errorf("%w: handoff is not valid UTF-8", ErrInvalidInput)
	}
	return string(data), nil
}

func newHandoffID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("launch: generating handoff id: %w", err)
	}
	return "h_" + hex.EncodeToString(raw[:]), nil
}

func ensureHandoffDir() error {
	dir := handoffDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("launch: creating handoff directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("launch: securing handoff directory: %w", err)
	}
	return nil
}

func handoffPath(id string) string {
	return filepath.Join(handoffDir(), id)
}

func claimedHandoffPath(id string) string {
	return handoffPath(id) + ".claimed"
}

func writeHandoffAtomic(finalPath string, data []byte) error {
	// Stage under a non-h_* name so concurrent sweeps never observe a partial record.
	tmp, err := os.CreateTemp(handoffDir(), "staging-*")
	if err != nil {
		return fmt.Errorf("launch: creating handoff staging file: %w", err)
	}
	tmpPath := tmp.Name()
	ok := false
	defer func() {
		tmp.Close()
		if !ok {
			os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("launch: securing handoff staging file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("launch: writing handoff staging file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("launch: closing handoff staging file: %w", err)
	}
	if err := os.Link(tmpPath, finalPath); err != nil {
		return fmt.Errorf("launch: publishing handoff file: %w", err)
	}
	ok = true
	_ = os.Remove(tmpPath)
	return nil
}

func readHandoffRecord(path string) (handoffRecord, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return handoffRecord{}, ErrHandoffNotFound
	}
	if err != nil {
		return handoffRecord{}, fmt.Errorf("launch: stat handoff: %w", err)
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return handoffRecord{}, fmt.Errorf("%w: handoff path is not a regular file", ErrInvalidInput)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return handoffRecord{}, fmt.Errorf("launch: reading handoff: %w", err)
	}
	var record handoffRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return handoffRecord{}, fmt.Errorf("%w: corrupt handoff record", ErrInvalidInput)
	}
	if err := ValidateRemoteAgent(record.Agent); err != nil {
		return handoffRecord{}, fmt.Errorf("%w: corrupt handoff record", ErrInvalidInput)
	}
	if err := ValidateUUIDSessionID(record.SessionID); err != nil {
		return handoffRecord{}, fmt.Errorf("%w: corrupt handoff record", ErrInvalidInput)
	}
	return record, nil
}

func sweepBoundHandoffs(now time.Time) error {
	entries, err := os.ReadDir(handoffDir())
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	cutoff := now.Add(-HandoffMaxAge)
	for _, entry := range entries {
		name := entry.Name()
		id := strings.TrimSuffix(name, ".claimed")
		if !handoffIDPattern.MatchString(id) {
			continue
		}
		path := filepath.Join(handoffDir(), name)
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() {
			_ = os.Remove(path)
			continue
		}
		record, err := readHandoffRecord(path)
		if err != nil {
			if info.ModTime().Before(cutoff) {
				if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
					return removeErr
				}
			}
			continue
		}
		if now.Unix() >= record.ExpiresAt {
			if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
				return removeErr
			}
		}
	}
	return nil
}
