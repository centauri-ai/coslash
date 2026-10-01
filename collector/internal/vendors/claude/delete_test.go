package claude

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

const deleteID = "12345678-1234-4234-8234-123456789abc"
const neighborID = "12345678-1234-4234-8234-123456789abd"

func deleteFixture(t *testing.T, home, path, body string) string {
	t.Helper()
	path = filepath.Join(home, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func closedDeleteProcesses(context.Context) ([]deleteProcess, error) { return nil, nil }

func TestDeleteSessionFamily(t *testing.T) {
	home := t.TempDir()
	project := ".claude/projects/project/"
	root := deleteFixture(t, home, project+deleteID+".jsonl", `{"sessionId":"`+deleteID+`","type":"user","uuid":"row","message":{"content":"hello"}}`+"\n")
	if parsed, err := parseTranscript(root); err != nil || parsed == nil {
		t.Fatalf("fixture unreadable before deletion: %v", err)
	}
	files := []string{root, deleteFixture(t, home, ".claude/projects/second/"+deleteID+".jsonl", "{}\n")}
	for _, path := range []string{
		project + deleteID + "/subagents/agent-child.jsonl",
		project + deleteID + "/tool-results/result.txt",
		".claude/session-env/" + deleteID + "/environment",
		".claude/file-history/" + deleteID + "/backup",
		".claude/sessions/old.json",
		desktopDeleteRelative() + "/account/project/desktop.json",
	} {
		body := "disposable"
		if path == ".claude/sessions/old.json" {
			body = `{"sessionId":"` + deleteID + `","pid":42}`
		}
		if filepath.Base(path) == "desktop.json" {
			body = `{"cliSessionId":"` + deleteID + `","title":"disposable"}`
		}
		files = append(files, deleteFixture(t, home, path, body))
	}
	neighbor := deleteFixture(t, home, project+neighborID+".jsonl", "neighbor")
	unrelated := deleteFixture(t, home, "unrelated/"+deleteID+"/keep", "keep")
	config := deleteFixture(t, home, ".claude/settings.json", "configuration")
	history := deleteFixture(t, home, ".claude/history.jsonl", `{"sessionId":"`+deleteID+`","display":"remove"}`+"\n"+`{"sessionId":"`+neighborID+`","display":"keep"}`+"\n")
	index := deleteFixture(t, home, project+"sessions-index.json", `{"version":1,"entries":[{"sessionId":"`+deleteID+`"},{"sessionId":"`+neighborID+`"}],"other":"keep"}`)
	if err := deleteSession(context.Background(), home, deleteID, closedDeleteProcesses); err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("survived %s: %v", path, err)
		}
	}
	for path, want := range map[string]string{neighbor: "neighbor", unrelated: "keep", config: "configuration", history: `{"sessionId":"` + neighborID + `","display":"keep"}` + "\n"} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("neighbor changed %s: %q %v", path, got, err)
		}
	}
	got, err := os.ReadFile(index)
	var keptIndex struct {
		Version int `json:"version"`
		Entries []struct {
			SessionID string `json:"sessionId"`
		} `json:"entries"`
		Other string `json:"other"`
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(got, &keptIndex); err != nil {
		t.Fatal(err)
	}
	if keptIndex.Version != 1 || keptIndex.Other != "keep" || len(keptIndex.Entries) != 1 || keptIndex.Entries[0].SessionID != neighborID {
		t.Fatalf("index neighbor changed: %s", got)
	}
	filesAfter, err := FilesSource(vendors.LocalReadSource, ProjectsRoot(home))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range filesAfter {
		if FamilyIDFromPath(file) == deleteID {
			t.Fatalf("still discoverable: %s", file)
		}
	}
	if _, err := parseTranscript(root); err == nil {
		t.Fatal("deleted transcript still readable")
	}
	if err := deleteSession(context.Background(), home, deleteID, closedDeleteProcesses); !errors.Is(err, ErrSessionMissing) {
		t.Fatalf("repeat: %v", err)
	}
}

func TestDeleteSessionRefusesBeforeMutation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*testing.T, string)
		probe func(context.Context) ([]deleteProcess, error)
		cause error
	}{
		{name: "active", setup: func(t *testing.T, h string) {
			deleteFixture(t, h, ".claude/sessions/live.json", `{"sessionId":"`+deleteID+`","pid":42}`)
		}, probe: func(context.Context) ([]deleteProcess, error) { return []deleteProcess{{pid: 42}}, nil }, cause: ErrSessionActive},
		{name: "unknown Claude process", probe: func(context.Context) ([]deleteProcess, error) { return []deleteProcess{{pid: 42, claude: true}}, nil }, cause: ErrSessionUnverified},
		{name: "probe failed", probe: func(context.Context) ([]deleteProcess, error) { return nil, errors.New("denied") }, cause: ErrSessionUnverified},
		{name: "malformed metadata", setup: func(t *testing.T, h string) { deleteFixture(t, h, ".claude/sessions/broken.json", "{") }, cause: ErrSessionUnverified},
		{name: "PID missing", setup: func(t *testing.T, h string) {
			deleteFixture(t, h, ".claude/sessions/unknown.json", `{"sessionId":"`+deleteID+`"}`)
		}, cause: ErrSessionUnverified},
		{name: "background job", setup: func(t *testing.T, h string) {
			deleteFixture(t, h, ".claude/jobs/job/state.json", `{"sessionId":"`+deleteID+`"}`)
		}, cause: ErrSessionUnverified},
		{name: "malformed index", setup: func(t *testing.T, h string) { deleteFixture(t, h, ".claude/projects/project/sessions-index.json", "{") }, cause: ErrSessionUnverified},
		{name: "malformed history", setup: func(t *testing.T, h string) { deleteFixture(t, h, ".claude/history.jsonl", "{") }, cause: ErrSessionUnverified},
		{name: "background", setup: func(t *testing.T, h string) {
			deleteFixture(t, h, ".claude/projects/project/"+deleteID+".jsonl", `{"sessionId":"`+deleteID+`","sessionKind":"bg"}`)
		}, cause: ErrSessionUnverified},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			path := deleteFixture(t, home, ".claude/projects/project/"+deleteID+".jsonl", "{}\n")
			if tc.setup != nil {
				tc.setup(t, home)
			}
			before, _ := os.ReadFile(path)
			probe := tc.probe
			if probe == nil {
				probe = closedDeleteProcesses
			}
			err := deleteSession(context.Background(), home, deleteID, probe)
			if !errors.Is(err, tc.cause) {
				t.Fatalf("got %v want %v", err, tc.cause)
			}
			after, readErr := os.ReadFile(path)
			if readErr != nil || string(after) != string(before) {
				t.Fatalf("mutated: %v", readErr)
			}
		})
	}
}

func TestDeleteSessionInvalidAndCancelled(t *testing.T) {
	for _, id := range []string{"", "../" + deleteID, "not-a-uuid", deleteID + ".jsonl"} {
		if err := DeleteSession(context.Background(), t.TempDir(), id); !errors.Is(err, ErrSessionInvalid) {
			t.Fatalf("%q: %v", id, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := deleteSession(ctx, t.TempDir(), deleteID, closedDeleteProcesses); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestDeleteSessionRejectsSymlinks(t *testing.T) {
	home := t.TempDir()
	outside := t.TempDir()
	target := deleteFixture(t, outside, "keep", "neighbor")
	path := deleteFixture(t, home, ".claude/projects/project/"+deleteID+".jsonl", "{}\n")
	link := filepath.Join(home, ".claude", "file-history", deleteID)
	if err := os.MkdirAll(filepath.Dir(link), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, link); err != nil {
		t.Skip(err)
	}
	if err := deleteSession(context.Background(), home, deleteID, closedDeleteProcesses); !errors.Is(err, ErrSessionUnverified) {
		t.Fatal(err)
	}
	for _, p := range []string{path, target} {
		if _, err := os.Stat(p); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDeleteProcessInventory(t *testing.T) {
	for _, tc := range []struct {
		name, output string
		windows      bool
		candidate    bool
		invalid      bool
	}{
		{name: "native", output: "42 claude claude --resume " + deleteID, candidate: true},
		{name: "node CLI", output: "42 node node /opt/lib/node_modules/@anthropic-ai/claude-code/cli.js", candidate: true},
		{name: "Desktop", output: "42 /Applications/Claude.app/Contents/MacOS/Claude /Applications/Claude.app/Contents/MacOS/Claude", candidate: true},
		{name: "unrelated directory", output: "42 go go test ./internal/vendors/claude"},
		{name: "windows native", output: `"claude.exe","42","Console","1","123 K"`, windows: true, candidate: true},
		{name: "windows node unverifiable", output: `"node.exe","42","Console","1","123 K"`, windows: true, candidate: true},
		{name: "invalid PID", output: "nope claude", invalid: true},
		{name: "empty", invalid: true},
		{name: "malformed windows", output: `"claude.exe"`, windows: true, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			processes, err := parseDeleteProcesses(tc.output, tc.windows)
			if tc.invalid {
				if err == nil {
					t.Fatal("accepted incomplete probe")
				}
				return
			}
			if err != nil || len(processes) != 1 || processes[0].pid != 42 || processes[0].claude != tc.candidate {
				t.Fatalf("got %+v, %v", processes, err)
			}
		})
	}
}

func TestDeleteSessionPartialFailure(t *testing.T) {
	home := t.TempDir()
	transcript := deleteFixture(t, home, ".claude/projects/project/"+deleteID+".jsonl", "{}\n")
	index := deleteFixture(t, home, ".claude/projects/project/sessions-index.json", `{"entries":[{"sessionId":"`+deleteID+`"},{"sessionId":"`+neighborID+`"}]}`)
	historyBody := `{"sessionId":"` + deleteID + `"}` + "\n"
	history := deleteFixture(t, home, ".claude/history.jsonl", historyBody)
	temporary := history + ".coslash-delete-" + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(temporary, []byte("preexisting"), 0600); err != nil {
		t.Fatal(err)
	}
	err := deleteSession(context.Background(), home, deleteID, closedDeleteProcesses)
	if !errors.Is(err, ErrSessionFailed) {
		t.Fatalf("partial result: %v", err)
	}
	if _, err := os.Stat(transcript); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(history)
	if string(data) != historyBody {
		t.Fatal("history truncated")
	}
	data, _ = os.ReadFile(index)
	if strings.Contains(string(data), deleteID) || !strings.Contains(string(data), neighborID) {
		t.Fatalf("unexpected index: %s", data)
	}
	data, _ = os.ReadFile(temporary)
	if string(data) != "preexisting" {
		t.Fatal("unowned temporary removed")
	}
}

func TestDeleteSessionSharedRecordsChanged(t *testing.T) {
	home := t.TempDir()
	transcript := deleteFixture(t, home, ".claude/projects/project/"+deleteID+".jsonl", "{}\n")
	history := deleteFixture(t, home, ".claude/history.jsonl", `{"sessionId":"`+deleteID+`"}`+"\n")
	replacement := `{"sessionId":"` + neighborID + `"}` + "\n"
	probe := func(context.Context) ([]deleteProcess, error) {
		if err := os.WriteFile(history, []byte(replacement), 0600); err != nil {
			t.Fatal(err)
		}
		return nil, nil
	}
	if err := deleteSession(context.Background(), home, deleteID, probe); !errors.Is(err, ErrSessionFailed) {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(history)
	if string(data) != replacement {
		t.Fatal("lost concurrent neighbor")
	}
	if _, err := os.Stat(transcript); err != nil {
		t.Fatal(err)
	}
}

func TestDesktopMetadataDeletionLeavesTranscriptResumable(t *testing.T) {
	home := t.TempDir()
	root := deleteFixture(t, home, ".claude/projects/project/"+deleteID+".jsonl", `{"sessionId":"`+deleteID+`","type":"user","uuid":"row","message":{"content":"disposable"}}`+"\n")
	metadata := deleteFixture(t, home, desktopDeleteRelative()+"/account/project/desktop.json", `{"cliSessionId":"`+deleteID+`","title":"disposable"}`)
	if err := os.Remove(metadata); err != nil {
		t.Fatal(err)
	}
	if parsed, err := parseTranscript(root); err != nil || parsed == nil {
		t.Fatalf("metadata-only experiment: %v", err)
	}
	if err := deleteSession(context.Background(), home, deleteID, closedDeleteProcesses); err != nil {
		t.Fatal(err)
	}
	if _, err := parseTranscript(root); err == nil {
		t.Fatal("adapter left transcript readable")
	}
}

func TestDeleteSessionVerifiesAbsence(t *testing.T) {
	home := t.TempDir()
	transcript := deleteFixture(t, home, ".claude/projects/project/"+deleteID+".jsonl", "{}\n")
	probe := func(context.Context) ([]deleteProcess, error) {
		deleteFixture(t, home, ".claude/projects/late/"+deleteID+".jsonl", "{}\n")
		return nil, nil
	}
	if err := deleteSession(context.Background(), home, deleteID, probe); !errors.Is(err, ErrSessionFailed) {
		t.Fatalf("late artifact: %v", err)
	}
	if _, err := os.Stat(transcript); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}
