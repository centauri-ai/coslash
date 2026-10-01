package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	historyRow := `{"sessionId":"` + deleteID + `","display":"erase"}` + "\n"
	history := deleteFixture(t, home, ".claude/history.jsonl", historyRow+`{"sessionId":"`+neighborID+`","display":"keep"}`+"\n")
	index := deleteFixture(t, home, project+"sessions-index.json", `{"version":1,"entries":[{"sessionId":"`+neighborID+`"}],"other":"keep"}`)
	if err := deleteSession(context.Background(), home, deleteID, closedDeleteProcesses); err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("survived %s: %v", path, err)
		}
	}
	for path, want := range map[string]string{neighbor: "neighbor", unrelated: "keep", config: "configuration", history: strings.Repeat(" ", len(historyRow)-1) + "\n" + `{"sessionId":"` + neighborID + `","display":"keep"}` + "\n"} {
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
	if !errors.Is(err, ErrSessionUnverified) {
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
	if !strings.Contains(string(data), deleteID) || !strings.Contains(string(data), neighborID) {
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
	if err := deleteSession(context.Background(), home, deleteID, probe); !errors.Is(err, ErrSessionUnverified) {
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
	if err := deleteSession(context.Background(), home, deleteID, probe); !errors.Is(err, ErrSessionUnverified) {
		t.Fatalf("late artifact: %v", err)
	}
	if _, err := os.Stat(transcript); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteSessionRefusesSharedWriterWithoutTranscriptAssociation(t *testing.T) {
	for _, shared := range []struct{ name, path, body string }{
		{"history", ".claude/history.jsonl", `{"sessionId":"` + deleteID + `"}` + "\n"},
		{"index", ".claude/projects/project/sessions-index.json", `{"entries":[{"sessionId":"` + deleteID + `"},{"sessionId":"` + neighborID + `"}]}`},
	} {
		t.Run(shared.name, func(t *testing.T) {
			home := t.TempDir()
			transcript := deleteFixture(t, home, ".claude/projects/project/"+deleteID+".jsonl", "{}\n")
			path := deleteFixture(t, home, shared.path, shared.body)
			writer, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0600)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			before, err := writer.Stat()
			if err != nil {
				t.Fatal(err)
			}
			// The process has opened shared storage but has no session metadata or
			// transcript association yet. A target-only open-file check misses it.
			probe := func(context.Context) ([]deleteProcess, error) { return []deleteProcess{{pid: 42, claude: true}}, nil }
			if err := deleteSession(context.Background(), home, deleteID, probe); !errors.Is(err, ErrSessionUnverified) {
				t.Fatalf("writer not excluded: %v", err)
			}
			after, err := os.Stat(path)
			if err != nil || !os.SameFile(before, after) {
				t.Fatalf("shared inode replaced: %v", err)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != shared.body {
				t.Fatalf("shared data changed: %q %v", data, err)
			}
			if _, err := os.Stat(transcript); err != nil {
				t.Fatal(err)
			}
			if shared.name == "history" {
				neighbor := `{"sessionId":"` + neighborID + `"}` + "\n"
				if _, err := writer.WriteString(neighbor); err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(path)
				if err != nil || string(data) != shared.body+neighbor {
					t.Fatalf("old-inode neighbor append lost: %q %v", data, err)
				}
			}
		})
	}
}

func TestReviewMetadataRegularReplacement(t *testing.T) {
	for _, kind := range []string{"live", "desktop"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			transcript := deleteFixture(t, home, ".claude/projects/project/"+deleteID+".jsonl", "{}\n")
			relative := ".claude/sessions/old.json"
			before := `{"sessionId":"` + deleteID + `","pid":42}`
			after := `{"sessionId":"` + neighborID + `","pid":99}`
			if kind == "desktop" {
				relative = desktopDeleteRelative() + "/account/project/old.json"
				before = `{"cliSessionId":"` + deleteID + `"}`
				after = `{"cliSessionId":"` + neighborID + `"}`
			}
			metadata := deleteFixture(t, home, relative, before)
			probe := func(context.Context) ([]deleteProcess, error) {
				replacement := metadata + ".replacement"
				if err := os.WriteFile(replacement, []byte(after), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(replacement, metadata); err != nil {
					t.Fatal(err)
				}
				return []deleteProcess{{pid: 99}}, nil
			}
			err := deleteSession(context.Background(), home, deleteID, probe)
			_, statErr := os.Stat(metadata)
			t.Logf("result=%v neighborMetadataRemoved=%v", err, errors.Is(statErr, os.ErrNotExist))
			if err == nil && errors.Is(statErr, os.ErrNotExist) {
				t.Fatal("reported success after deleting replacement metadata owned by neighbor")
			}
			if _, err := os.Stat(transcript); err != nil {
				t.Fatalf("wrote before refusing changed ownership: %v", err)
			}
		})
	}
}

func TestReviewUnknownLayoutReadableAfterSuccess(t *testing.T) {
	home := t.TempDir()
	body := `{"sessionId":"` + deleteID + `","type":"user","uuid":"row","message":{"content":"hello"}}` + "\n"
	deleteFixture(t, home, ".claude/projects/project/"+deleteID+".jsonl", body)
	hidden := deleteFixture(t, home, ".claude/projects/project/nested/"+deleteID+".jsonl", body)
	err := deleteSession(context.Background(), home, deleteID, closedDeleteProcesses)
	files, discoveryErr := FilesSource(vendors.LocalReadSource, ProjectsRoot(home))
	parsed, readErr := parseTranscript(hidden)
	found := false
	for _, p := range files {
		if FamilyIDFromPath(p) == deleteID {
			found = true
		}
	}
	t.Logf("result=%v discoveryErr=%v found=%v parsed=%v readErr=%v", err, discoveryErr, found, parsed != nil, readErr)
	if err == nil && found && parsed != nil && readErr == nil {
		t.Fatal("reported success with collector-readable target in unknown layout")
	}
}

func TestReviewFreshPIDPublishedDuringProbe(t *testing.T) {
	home := t.TempDir()
	transcript := deleteFixture(t, home, ".claude/projects/project/"+deleteID+".jsonl", "{}\n")
	metadata := deleteFixture(t, home, ".claude/sessions/old.json", `{"sessionId":"`+deleteID+`","pid":42}`)
	probe := func(context.Context) ([]deleteProcess, error) {
		if err := os.WriteFile(metadata, []byte(`{"sessionId":"`+deleteID+`","pid":99}`), 0600); err != nil {
			t.Fatal(err)
		}
		return []deleteProcess{{pid: 99}}, nil
	}
	err := deleteSession(context.Background(), home, deleteID, probe)
	_, statErr := os.Stat(transcript)
	t.Logf("result=%v activeTranscriptRemoved=%v", err, errors.Is(statErr, os.ErrNotExist))
	if !errors.Is(err, ErrSessionActive) && !errors.Is(err, ErrSessionUnverified) {
		t.Fatalf("failed to refuse fresh known-active session: %v", err)
	}
	if statErr != nil {
		t.Fatal("removed active transcript")
	}
}

func TestDeleteSharedReferencesNeedWriterProtocol(t *testing.T) {
	for _, shared := range []struct{ name, path, body string }{
		{"history", ".claude/history.jsonl", `{"sessionId":"` + deleteID + `"}` + "\n"},
		{"index", ".claude/projects/project/sessions-index.json", `{"entries":[{"sessionId":"` + deleteID + `"},{"sessionId":"` + neighborID + `"}]}`},
	} {
		t.Run(shared.name, func(t *testing.T) {
			home := t.TempDir()
			target := deleteFixture(t, home, ".claude/projects/project/"+deleteID+".jsonl", "{}\n")
			path := deleteFixture(t, home, shared.path, shared.body)
			writer, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0600)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			before, err := writer.Stat()
			if err != nil {
				t.Fatal(err)
			}
			wantBody := shared.body
			if shared.name == "history" {
				wantBody = strings.Repeat(" ", len(shared.body)-1) + "\n"
			}
			err = deleteSession(context.Background(), home, deleteID, closedDeleteProcesses)
			if (shared.name == "history" && err != nil) || (shared.name == "index" && !errors.Is(err, ErrSessionUnverified)) {
				t.Fatalf("unproven writer protocol: %v", err)
			}
			after, err := os.Stat(path)
			if err != nil || !os.SameFile(before, after) {
				t.Fatalf("shared inode replaced: %v", err)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != wantBody {
				t.Fatal("shared references mutated")
			}
			if _, err := os.Stat(target); (shared.name == "history" && !errors.Is(err, os.ErrNotExist)) || (shared.name == "index" && err != nil) {
				t.Fatal("target mutated before refusal")
			}
			if shared.name == "history" {
				neighbor := `{"sessionId":"` + neighborID + `"}` + "\n"
				if _, err := writer.WriteString(neighbor); err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(path)
				if err != nil || string(data) != wantBody+neighbor {
					t.Fatalf("lost append through existing descriptor: %q %v", data, err)
				}
			}
		})
	}
}

func TestDeleteSessionUnknownChildLayoutRefusesWithoutMutation(t *testing.T) {
	home := t.TempDir()
	main := deleteFixture(t, home, ".claude/projects/project/"+deleteID+".jsonl", "{}\n")
	unknown := deleteFixture(t, home, ".claude/projects/project/nested/"+deleteID+"/subagents/agent-child.jsonl", "{}\n")
	if err := deleteSession(context.Background(), home, deleteID, closedDeleteProcesses); !errors.Is(err, ErrSessionUnverified) {
		t.Fatal(err)
	}
	for _, path := range []string{main, unknown} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
	}
}

type deleteSkippedSource struct {
	vendors.ReadSource
	path string
}

func (source deleteSkippedSource) ReadDir(path string) ([]os.DirEntry, error) {
	if path == source.path {
		return nil, os.ErrPermission
	}
	return source.ReadSource.ReadDir(path)
}

func TestDeleteFamilyRefusesSkippedAuthoritativeScan(t *testing.T) {
	home := t.TempDir()
	path := deleteFixture(t, home, ".claude/projects/project/"+deleteID+".jsonl", "{}\n")
	root := ProjectsRoot(home)
	source := deleteSkippedSource{vendors.LocalReadSource, filepath.Dir(path)}
	if err := validateDeleteFamily(context.Background(), source, root, deleteID, []string{path}); err == nil {
		t.Fatal("accepted incomplete scan")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteOwnedParentRefusesAncestorReplacement(t *testing.T) {
	for _, kind := range []string{"relative", "absolute"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			outside := t.TempDir()
			path := deleteFixture(t, home, ".claude/projects/project/"+deleteID+".jsonl", "target")
			inside := deleteFixture(t, home, "unrelated/"+deleteID+".jsonl", "inside neighbor")
			external := deleteFixture(t, outside, deleteID+".jsonl", "outside neighbor")
			root, err := os.OpenRoot(home)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			relative, _ := filepath.Rel(home, path)
			artifact, err := pinDeleteArtifact(root, relative)
			if err != nil {
				t.Fatal(err)
			}
			defer artifact.parent.Close()
			defer artifact.file.Close()
			project := filepath.Dir(path)
			if err := os.Rename(project, project+"-original"); err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join("..", "..", "..", "unrelated")
			if kind == "absolute" {
				destination = outside
			}
			if err := os.Symlink(destination, project); err != nil {
				t.Skip(err)
			}
			if err := artifact.remove(); err == nil {
				t.Fatal("accepted changed expected parent")
			}
			for path, want := range map[string]string{inside: "inside neighbor", external: "outside neighbor"} {
				data, err := os.ReadFile(path)
				if err != nil || string(data) != want {
					t.Fatalf("neighbor lost: %q %v", data, err)
				}
			}
			data, err := artifact.parent.ReadFile(artifact.leaf)
			if err != nil || string(data) != "target" {
				t.Fatalf("expected parent handle redirected: %q %v", data, err)
			}
		})
	}
}

func TestDeleteMetadataReplacementWithIdenticalBytesRefuses(t *testing.T) {
	home := t.TempDir()
	target := deleteFixture(t, home, ".claude/projects/project/"+deleteID+".jsonl", "{}\n")
	body := `{"sessionId":"` + deleteID + `","pid":42}`
	metadata := deleteFixture(t, home, ".claude/sessions/old.json", body)
	probe := func(context.Context) ([]deleteProcess, error) {
		replacement := deleteFixture(t, home, ".claude/sessions/replacement.json", body)
		if err := os.Rename(replacement, metadata); err != nil {
			t.Fatal(err)
		}
		return nil, nil
	}
	if err := deleteSession(context.Background(), home, deleteID, probe); !errors.Is(err, ErrSessionUnverified) {
		t.Fatal(err)
	}
	for _, path := range []string{target, metadata} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDeleteSessionRetainedCaptureRefusesRetry(t *testing.T) {
	home := t.TempDir()
	target := deleteFixture(t, home, ".claude/projects/project/"+deleteID+".jsonl", "{}\n")
	retained := deleteFixture(t, home, ".claude/session-env/.coslash-delete-"+deleteID+"-interrupted/"+deleteID+"/environment", "retained target")
	neighbor := deleteFixture(t, home, ".claude/session-env/"+neighborID+"/environment", "neighbor")
	if err := deleteSession(context.Background(), home, deleteID, closedDeleteProcesses); !errors.Is(err, ErrSessionUnverified) {
		t.Fatalf("retained capture accepted: %v", err)
	}
	for path, want := range map[string]string{target: "{}\n", retained: "retained target", neighbor: "neighbor"} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != want {
			t.Fatalf("modified before refusal: %q %v", data, err)
		}
	}
}

func TestDeleteHistoryBlankingBoundaries(t *testing.T) {
	for _, ending := range []string{"\n", "\r\n", ""} {
		t.Run(fmt.Sprintf("ending-%q", ending), func(t *testing.T) {
			home := t.TempDir()
			neighbor := `{"sessionId":"` + neighborID + `","display":"keep 世界"}` + "\n"
			target := `{"sessionId":"` + deleteID + `","display":"erase 世界"}` + ending
			history := deleteFixture(t, home, ".claude/history.jsonl", neighbor+target)
			if err := deleteSession(context.Background(), home, deleteID, closedDeleteProcesses); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(history)
			if err != nil || len(data) != len(neighbor+target) || string(data[:len(neighbor)]) != neighbor || len(bytes.TrimSpace(data[len(neighbor):])) != 0 {
				t.Fatalf("history neighbors/length: %q %v", data, err)
			}
			if _, err := os.Stat(history + ".lock"); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("lock leaked: %v", err)
			}
			if err := deleteSession(context.Background(), home, deleteID, closedDeleteProcesses); !errors.Is(err, ErrSessionMissing) {
				t.Fatalf("blank retry: %v", err)
			}
		})
	}
}

func TestDeleteHistoryNativeWriterLockRefusesBeforeMutation(t *testing.T) {
	home := t.TempDir()
	target := deleteFixture(t, home, ".claude/projects/project/"+deleteID+".jsonl", "{}\n")
	body := `{"sessionId":"` + deleteID + `"}` + "\n"
	history := deleteFixture(t, home, ".claude/history.jsonl", body)
	if err := os.Mkdir(history+".lock", 0700); err != nil {
		t.Fatal(err)
	}
	if err := deleteSession(context.Background(), home, deleteID, closedDeleteProcesses); !errors.Is(err, ErrSessionUnverified) {
		t.Fatalf("writer lock: %v", err)
	}
	data, _ := os.ReadFile(history)
	if string(data) != body {
		t.Fatal("locked history changed")
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatal("transcript changed before refusal")
	}
	if info, err := os.Stat(history + ".lock"); err != nil || !info.IsDir() {
		t.Fatal("native writer lock removed")
	}
}
