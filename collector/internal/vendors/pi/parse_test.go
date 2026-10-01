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
	"strings"
	"testing"
)

func writeTranscript(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "custom session file.jsonl")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func fixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/schema3.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestSchema3ReadOnlyFacts(t *testing.T) {
	data := fixture(t)
	path := writeTranscript(t, data)
	parsed, err := parseTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(data, after) {
		t.Fatal("parser changed transcript")
	}
	if parsed.Header.ID != "custom.session_01" || parsed.Header.ParentSession != "/parent with spaces.jsonl" || parsed.Header.CWD != "/project with spaces" {
		t.Fatalf("header: %+v", parsed.Header)
	}
	if len(parsed.Entries) != 14 || parsed.Entries[13].Type != "future_entry" || !bytes.Contains(parsed.Entries[13].Raw, []byte("unknownPayload")) {
		t.Fatalf("entries not retained: %d", len(parsed.Entries))
	}
	usageCount, totalInput, totalCost := 0, 0, 0.0
	for i, e := range parsed.Entries {
		if e.AppendIndex != i || parsed.ByID[e.ID] != i {
			t.Fatal("append identity lost")
		}
		if e.Usage == nil {
			continue
		}
		usageCount++
		if e.Usage.TokensMissing || e.Usage.CostMissing {
			t.Fatal("complete usage marked missing")
		}
		totalInput += *e.Usage.Input
		totalCost += *e.Usage.Cost.Total
	}
	if usageCount != 5 || totalInput != 45 || totalCost != 12.5 {
		t.Fatalf("usage including nested double count: %d %d %f", usageCount, totalInput, totalCost)
	}
	if parsed.Entries[1].Usage.Model != "actual" || parsed.Entries[2].Usage.Model != "" || !parsed.Entries[2].NestedDetailIncomplete {
		t.Fatal("attribution/nested detail facts lost")
	}
	if len(parsed.Diagnostics) != 0 || parsed.Incomplete {
		t.Fatalf("unexpected diagnostics: %v", parsed.Diagnostics)
	}
}

func TestReadOnlyFinalRecords(t *testing.T) {
	for _, test := range []struct {
		name        string
		tail        string
		incomplete  bool
		wantEntries int
		wantError   bool
	}{
		{"partial", `{"type":"message","id":"partial"`, true, 14, false},
		{"valid_unterminated", `{"type":"custom","id":"final","parentId":"unknown","timestamp":"2026-09-30T10:00:15Z"}`, false, 15, false},
		{"completed_malformed", "{broken}\n", false, 0, true},
		{"unterminated_malformed", "{broken}", false, 0, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := append(fixture(t), []byte(test.tail)...)
			path := writeTranscript(t, data)
			parsed, err := parseTranscript(path)
			if (err != nil) != test.wantError {
				t.Fatalf("error: %v", err)
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(data, after) {
				t.Fatal("parser changed final record")
			}
			if err == nil && (parsed.Incomplete != test.incomplete || len(parsed.Entries) != test.wantEntries) {
				t.Fatalf("tail facts: %+v", parsed)
			}
		})
	}
}

func TestInvalidIdentitySchemaAndOrphans(t *testing.T) {
	data := fixture(t)
	for _, version := range []string{"0", "1", "2", "4"} {
		path := writeTranscript(t, bytes.Replace(data, []byte(`"version":3`), []byte(`"version":`+version), 1))
		if _, err := parseTranscript(path); err == nil || !strings.Contains(err.Error(), "unsupported Pi transcript schema") {
			t.Fatalf("version %s: %v", version, err)
		}
	}
	path := writeTranscript(t, bytes.Replace(data, []byte(`"version":3,`), nil, 1))
	if _, err := parseTranscript(path); err == nil {
		t.Fatal("missing schema accepted")
	}
	path = writeTranscript(t, append(data, []byte(`{"type":"custom","id":"user","parentId":null}`+"\n")...))
	if _, err := parseTranscript(path); err == nil || !strings.Contains(err.Error(), "duplicate entry ID") {
		t.Fatalf("duplicate: %v", err)
	}
	path = writeTranscript(t, bytes.Replace(data, []byte(`"parentId":"info"`), []byte(`"parentId":"absent"`), 1))
	parsed, err := parseTranscript(path)
	if err != nil || len(parsed.Entries) != 14 || len(parsed.Diagnostics) != 1 || !strings.Contains(parsed.Diagnostics[0], "orphan parent") {
		t.Fatalf("orphan: %v %+v", err, parsed)
	}
	for _, pair := range [][2]string{{`"input":1,`, `"input":-1,`}, {`"total":0.5`, `"total":-0.5`}} {
		path := writeTranscript(t, bytes.Replace(data, []byte(pair[0]), []byte(pair[1]), 1))
		if _, err := parseTranscript(path); err == nil || !strings.Contains(err.Error(), "invalid negative usage") {
			t.Fatalf("negative accounting: %v", err)
		}
	}
}

func TestLargeLineAndMissingAccounting(t *testing.T) {
	var records []byte
	for _, record := range []any{
		header{Type: "session", Version: 3, ID: "large", Timestamp: "2026-09-30T10:00:00Z"},
		map[string]any{"type": "message", "id": "large", "parentId": nil, "message": map[string]any{"role": "assistant", "content": strings.Repeat("x", 2<<20)}},
		map[string]any{"type": "usage", "id": "zero", "parentId": "large", "usage": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "cost": map[string]any{"total": 0}}},
	} {
		encoded, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, encoded...)
		records = append(records, '\n')
	}
	path := writeTranscript(t, records)
	parsed, err := parseTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Entries) != 2 || !parsed.Entries[0].Usage.TokensMissing || !parsed.Entries[0].Usage.CostMissing || !parsed.Entries[1].Usage.ZeroTokens || !parsed.Entries[1].Usage.ZeroCost {
		t.Fatal("missing/zero accounting facts lost")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(records, after) {
		t.Fatal("large transcript changed")
	}
}

func TestTranscriptResourceBudgets(t *testing.T) {
	for _, kind := range []string{"record", "transcript", "entries"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "bounded.jsonl")
			f, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if _, err = f.WriteString(`{"type":"session","version":3,"id":"bounded"}` + "\n"); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "record":
				if _, err = f.WriteString(strings.Repeat(" ", maxTranscriptRecordBytes+1)); err != nil {
					t.Fatal(err)
				}
			case "transcript":
				if err = f.Truncate(maxTranscriptBytes + 1); err != nil {
					t.Fatal(err)
				}
			case "entries":
				for i := 0; i < maxTranscriptEntries; i++ {
					if _, err = fmt.Fprintf(f, `{"type":"custom","id":"%d","timestamp":"2026-09-30T10:00:00Z"}`+"\n", i); err != nil {
						t.Fatal(err)
					}
				}
				// Even an incomplete record must not bypass the entry budget.
				if _, err = f.WriteString(`{"type":"custom","id":"excess"`); err != nil {
					t.Fatal(err)
				}
			}
			if err = f.Close(); err != nil {
				t.Fatal(err)
			}
			parsed, err := parseTranscript(path)
			if parsed != nil || err == nil || !strings.Contains(err.Error(), kind+" limit") {
				t.Fatalf("expected %s limit rejection without partial transcript, got parsed=%v error=%v", kind, parsed != nil, err)
			}
		})
	}
}

func TestBoundedRecordFraming(t *testing.T) {
	for _, test := range []struct {
		name, input string
		remaining   int
		wantEOF     bool
		wantError   string
	}{
		{"exact budget", "{}\n", 3, false, ""},
		{"exact record limit", strings.Repeat("x", maxTranscriptRecordBytes), maxTranscriptBytes, true, ""},
		{"unterminated", "{}", 2, true, ""},
		{"growing transcript", "{}\n", 2, false, "transcript limit"},
		{"exhausted EOF", "", 0, true, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			data, err := readTranscriptRecord(context.Background(), bufio.NewReader(strings.NewReader(test.input)), test.remaining)
			if test.wantError != "" {
				if data != nil || err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("data=%q error=%v", data, err)
				}
			} else if string(data) != test.input || (err == io.EOF) != test.wantEOF || (err != nil && err != io.EOF) {
				t.Fatalf("data=%q error=%v", data, err)
			}
		})
	}
}
