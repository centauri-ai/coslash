//go:build windows

package cursor

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsCursorLivenessUsesSelectedIDEChat(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	path := filepath.Join(cursorGlobalStorage(home), "state.vscdb")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE ItemTable (key TEXT PRIMARY KEY, value BLOB)`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	id := "00000000-0000-4000-8000-000000000001"
	if _, err := db.Exec(`INSERT INTO ItemTable(key, value) VALUES ('cursor/glass.selectedAgent', ?)`, id); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	originalIDE, originalCLI, originalExecutable := cursorIDEProcessRunning, cursorCLIStoreProcesses, cursorProcessExecutable
	t.Cleanup(func() {
		cursorIDEProcessRunning = originalIDE
		cursorCLIStoreProcesses = originalCLI
		cursorProcessExecutable = originalExecutable
	})
	cursorCLIStoreProcesses = func(string) ([]uint32, error) { return nil, nil }
	cursorProcessExecutable = func(uint32) (string, error) { return "", os.ErrNotExist }
	cursorIDEProcessRunning = func() bool { return true }
	if got := loadLiveSessions(); got[id] != entrypointIDE {
		t.Fatalf("live sessions = %#v, want selected IDE chat", got)
	}
	cursorIDEProcessRunning = func() bool { return false }
	if got := loadLiveSessions(); len(got) != 0 {
		t.Fatalf("live sessions without Cursor process = %#v", got)
	}
}

func TestWindowsCursorLivenessUsesOpenCLIStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	id := "00000000-0000-4000-8000-000000000002"
	store := filepath.Join(home, ".cursor", "chats", "workspace", id, "store.db")
	if err := os.MkdirAll(filepath.Dir(store), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	originalIDE, originalCLI, originalExecutable := cursorIDEProcessRunning, cursorCLIStoreProcesses, cursorProcessExecutable
	t.Cleanup(func() {
		cursorIDEProcessRunning = originalIDE
		cursorCLIStoreProcesses = originalCLI
		cursorProcessExecutable = originalExecutable
	})
	cursorIDEProcessRunning = func() bool { return false }
	cursorCLIStoreProcesses = func(path string) ([]uint32, error) {
		if path == store {
			return []uint32{1234}, nil
		}
		return nil, nil
	}
	cursorProcessExecutable = func(pid uint32) (string, error) {
		if pid == 1234 {
			return filepath.Join(home, "AppData", "Local", "cursor-agent", "versions", "2026.09.18-build", "node.exe"), nil
		}
		return filepath.Join(home, "unrelated.exe"), nil
	}
	if got := loadLiveSessions(); got[id] != entrypointCLI {
		t.Fatalf("live sessions = %#v, want open CLI chat", got)
	}
}

func TestWindowsCursorLivenessMarksSameSessionAmbiguousAcrossLanes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	id := "00000000-0000-4000-8000-000000000003"
	store := filepath.Join(home, ".cursor", "chats", "workspace", id, "store.db")
	if err := os.MkdirAll(filepath.Dir(store), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(cursorGlobalStorage(home), "state.vscdb")
	if err := os.MkdirAll(filepath.Dir(state), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", state)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE ItemTable (key TEXT PRIMARY KEY, value BLOB)`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO ItemTable(key, value) VALUES ('cursor/glass.selectedAgent', ?)`, id); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	originalIDE, originalCLI, originalExecutable := cursorIDEProcessRunning, cursorCLIStoreProcesses, cursorProcessExecutable
	t.Cleanup(func() {
		cursorIDEProcessRunning = originalIDE
		cursorCLIStoreProcesses = originalCLI
		cursorProcessExecutable = originalExecutable
	})
	cursorIDEProcessRunning = func() bool { return true }
	cursorCLIStoreProcesses = func(string) ([]uint32, error) { return []uint32{1234}, nil }
	cursorProcessExecutable = func(uint32) (string, error) {
		return filepath.Join(home, "AppData", "Local", "cursor-agent", "versions", "2026.09.18-build", "node.exe"), nil
	}
	if got := loadLiveSessions(); got[id] != "" {
		t.Fatalf("live sessions = %#v, want ambiguous lane", got)
	}
}

func TestWindowsCursorLivenessRejectsUnrelatedStoreHolder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	id := "00000000-0000-4000-8000-000000000004"
	store := filepath.Join(home, ".cursor", "chats", "workspace", id, "store.db")
	if err := os.MkdirAll(filepath.Dir(store), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	originalIDE, originalCLI, originalExecutable := cursorIDEProcessRunning, cursorCLIStoreProcesses, cursorProcessExecutable
	t.Cleanup(func() {
		cursorIDEProcessRunning = originalIDE
		cursorCLIStoreProcesses = originalCLI
		cursorProcessExecutable = originalExecutable
	})
	cursorIDEProcessRunning = func() bool { return false }
	cursorCLIStoreProcesses = func(string) ([]uint32, error) { return []uint32{5678}, nil }
	cursorProcessExecutable = func(uint32) (string, error) {
		return filepath.Join(home, "AppData", "Local", "Microsoft", "WindowsApps", "SearchIndexer.exe"), nil
	}
	if got := loadLiveSessions(); len(got) != 0 {
		t.Fatalf("live sessions = %#v, want unrelated store holder ignored", got)
	}
}

func TestIsCursorAgentExecutable(t *testing.T) {
	home := filepath.Join("C:\\", "Users", "person")
	if !isCursorAgentExecutable(home, filepath.Join(home, "AppData", "Local", "cursor-agent", "versions", "2026.09.18-build", "node.exe")) {
		t.Fatal("installed cursor-agent node.exe was not recognized")
	}
	if !isCursorAgentExecutable(home, filepath.Join(home, "AppData", "Local", "cursor-agent", "node.exe")) {
		t.Fatal("direct cursor-agent node.exe was not recognized")
	}
	if isCursorAgentExecutable(home, filepath.Join(home, "AppData", "Local", "Programs", "cursor", "Cursor.exe")) {
		t.Fatal("Cursor IDE executable was recognized as cursor-agent")
	}
}
