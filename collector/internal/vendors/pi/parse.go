package pi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const (
	maxTranscriptRecordBytes = 16 << 20
	maxTranscriptBytes       = 64 << 20
	maxTranscriptEntries     = 100000
)

// Raw reading deliberately avoids Pi's persistent loader, which repairs/migrates files.
func parseTranscript(path string) (*transcript, error) {
	return parseTranscriptContext(context.Background(), path)
}
func parseTranscriptContext(ctx context.Context, path string) (*transcript, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > maxTranscriptBytes {
		return nil, fmt.Errorf("transcript limit exceeded: maximum %d bytes", maxTranscriptBytes)
	}
	t := &transcript{Path: abs, ByID: map[string]int{}}
	r := bufio.NewReader(f)
	totalBytes := 0
	for line := 1; ; line++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, readErr := readTranscriptRecord(ctx, r, maxTranscriptBytes-totalBytes)
		totalBytes += len(data)
		if readErr != nil && readErr != io.EOF {
			return nil, fmt.Errorf("line %d: %w", line, readErr)
		}
		data = bytes.TrimSpace(data)
		if len(data) > 0 {
			if t.Header.ID != "" && len(t.Entries) >= maxTranscriptEntries {
				return nil, fmt.Errorf("line %d: entries limit exceeded: maximum %d", line, maxTranscriptEntries)
			}
			if !json.Valid(data) {
				var value any
				decodeErr := json.Unmarshal(data, &value)
				if readErr == io.EOF && t.Header.ID != "" && decodeErr.Error() == "unexpected end of JSON input" {
					t.Incomplete = true
					t.Diagnostics = append(t.Diagnostics, fmt.Sprintf("line %d: incomplete final record", line))
					break
				}
				return nil, fmt.Errorf("line %d: malformed JSON record", line)
			}
			if t.Header.ID == "" {
				if err := json.Unmarshal(data, &t.Header); err != nil {
					return nil, fmt.Errorf("line %d: invalid session header: %w", line, err)
				}
				if t.Header.Type != "session" || t.Header.ID == "" {
					return nil, fmt.Errorf("line %d: expected session header with identity", line)
				}
				if t.Header.Version != 3 {
					return nil, fmt.Errorf("unsupported Pi transcript schema %d (verified schema: 3, Pi 0.99.1)", t.Header.Version)
				}
				t.Header.Raw = append(json.RawMessage(nil), data...)
			} else {
				var e entry
				if err := json.Unmarshal(data, &e); err != nil {
					return nil, fmt.Errorf("line %d: invalid entry: %w", line, err)
				}
				if e.Type == "" || e.Type == "session" || e.ID == "" {
					return nil, fmt.Errorf("line %d: invalid entry identity/type", line)
				}
				if _, exists := t.ByID[e.ID]; exists {
					return nil, fmt.Errorf("line %d: duplicate entry ID %q", line, e.ID)
				}
				e.AppendIndex = len(t.Entries)
				e.Raw = append(json.RawMessage(nil), data...)
				if err := extractUsage(&e); err != nil {
					return nil, fmt.Errorf("line %d: %w", line, err)
				}
				t.ByID[e.ID] = e.AppendIndex
				t.Entries = append(t.Entries, e)
				if _, err := time.Parse(time.RFC3339Nano, e.Timestamp); err != nil {
					t.Diagnostics = append(t.Diagnostics, fmt.Sprintf("entry %q: missing or invalid timestamp", e.ID))
				}
			}
		}
		if readErr == io.EOF {
			break
		}
	}
	if t.Header.ID == "" {
		return nil, fmt.Errorf("missing session header")
	}
	for _, e := range t.Entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if e.ParentID != nil && *e.ParentID != "" {
			if index, ok := t.ByID[*e.ParentID]; !ok {
				t.Diagnostics = append(t.Diagnostics, fmt.Sprintf("entry %q: orphan parent %q", e.ID, *e.ParentID))
			} else if index >= e.AppendIndex {
				t.Diagnostics = append(t.Diagnostics, fmt.Sprintf("entry %q: parent is not earlier in append order", e.ID))
			}
		}
	}
	return t, nil
}

func readTranscriptRecord(ctx context.Context, reader *bufio.Reader, remaining int) ([]byte, error) {
	var data bytes.Buffer
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		fragment, err := reader.ReadSlice('\n')
		if len(fragment) > remaining-data.Len() {
			return nil, fmt.Errorf("transcript limit exceeded: maximum %d bytes", maxTranscriptBytes)
		}
		if len(fragment) > maxTranscriptRecordBytes-data.Len() {
			return nil, fmt.Errorf("record limit exceeded: maximum %d bytes", maxTranscriptRecordBytes)
		}
		data.Write(fragment)
		if err != bufio.ErrBufferFull {
			return data.Bytes(), err
		}
	}
}

func extractUsage(e *entry) error {
	switch e.Type {
	case "message", "usage", "compaction", "branch_summary":
	default:
		return nil
	}
	var payload struct {
		Provider string          `json:"provider"`
		Model    string          `json:"model"`
		Usage    json.RawMessage `json:"usage"`
		Message  struct {
			Role          string          `json:"role"`
			Provider      string          `json:"provider"`
			Model         string          `json:"model"`
			ResponseModel string          `json:"responseModel"`
			Usage         json.RawMessage `json:"usage"`
			NestedCalls   *struct {
				Complete bool `json:"complete"`
			} `json:"nestedCalls"`
		} `json:"message"`
	}
	if err := json.Unmarshal(e.Raw, &payload); err != nil {
		return err
	}
	source, provider, model, raw := "", "", "", payload.Usage
	switch e.Type {
	case "message":
		if payload.Message.NestedCalls != nil {
			e.NestedDetailIncomplete = !payload.Message.NestedCalls.Complete
		}
		switch payload.Message.Role {
		case "assistant":
			source, provider, model = "assistant", payload.Message.Provider, payload.Message.Model
			if payload.Message.ResponseModel != "" {
				model = payload.Message.ResponseModel
			}
		case "toolResult":
			source = "tool_result"
		default:
			return nil
		}
		raw = payload.Message.Usage
	case "usage":
		source, provider, model = "usage", payload.Provider, payload.Model
	case "compaction", "branch_summary":
		source = e.Type
	default:
		return nil
	}
	u := &usageFact{Source: source, Provider: provider, Model: model, Raw: raw}
	if len(raw) != 0 && !bytes.Equal(raw, []byte("null")) {
		if err := json.Unmarshal(raw, u); err != nil {
			return fmt.Errorf("invalid usage: %w", err)
		}
	}
	u.TokensMissing = u.Input == nil || u.Output == nil || u.CacheRead == nil || u.CacheWrite == nil
	for _, count := range []*int{u.Input, u.Output, u.CacheRead, u.CacheWrite, u.CacheWrite1h, u.TotalTokens} {
		if count != nil && *count < 0 {
			return fmt.Errorf("invalid negative usage tokens")
		}
	}
	u.CostMissing = u.Cost == nil || u.Cost.Total == nil
	if !u.CostMissing && *u.Cost.Total < 0 {
		return fmt.Errorf("invalid negative usage cost")
	}
	if !u.TokensMissing {
		u.ZeroTokens = *u.Input == 0 && *u.Output == 0 && *u.CacheRead == 0 && *u.CacheWrite == 0
	}
	if !u.CostMissing {
		u.ZeroCost = *u.Cost.Total == 0
	}
	e.Usage = u
	return nil
}
