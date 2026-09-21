//go:build windows

package cursor

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
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

	originalIDE, originalCLI, originalExecutable, originalResume := cursorIDEProcessRunning, cursorCLIStoreProcesses, cursorProcessExecutable, cursorCLIResumeSessionIDs
	t.Cleanup(func() {
		cursorIDEProcessRunning = originalIDE
		cursorCLIStoreProcesses = originalCLI
		cursorProcessExecutable = originalExecutable
		cursorCLIResumeSessionIDs = originalResume
	})
	cursorCLIResumeSessionIDs = func(string) map[string]bool { return nil }
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

	originalIDE, originalCLI, originalExecutable, originalResume := cursorIDEProcessRunning, cursorCLIStoreProcesses, cursorProcessExecutable, cursorCLIResumeSessionIDs
	t.Cleanup(func() {
		cursorIDEProcessRunning = originalIDE
		cursorCLIStoreProcesses = originalCLI
		cursorProcessExecutable = originalExecutable
		cursorCLIResumeSessionIDs = originalResume
	})
	cursorCLIResumeSessionIDs = func(string) map[string]bool { return nil }
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

func TestWindowsCursorLivenessUsesOpenCLISidecar(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	id := "00000000-0000-4000-8000-000000000005"
	store := filepath.Join(home, ".cursor", "chats", "workspace", id, "store.db")
	if err := os.MkdirAll(filepath.Dir(store), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{store, store + "-shm"} {
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	originalIDE, originalCLI, originalExecutable, originalResume := cursorIDEProcessRunning, cursorCLIStoreProcesses, cursorProcessExecutable, cursorCLIResumeSessionIDs
	t.Cleanup(func() {
		cursorIDEProcessRunning = originalIDE
		cursorCLIStoreProcesses = originalCLI
		cursorProcessExecutable = originalExecutable
		cursorCLIResumeSessionIDs = originalResume
	})
	cursorCLIResumeSessionIDs = func(string) map[string]bool { return nil }
	cursorIDEProcessRunning = func() bool { return false }
	cursorCLIStoreProcesses = func(path string) ([]uint32, error) {
		if path == store+"-shm" {
			return []uint32{4321}, nil
		}
		return nil, nil
	}
	cursorProcessExecutable = func(uint32) (string, error) {
		return filepath.Join(home, "AppData", "Local", "cursor-agent", "versions", "2026.09.18-build", "node.exe"), nil
	}
	if got := loadLiveSessions(); got[id] != entrypointCLI {
		t.Fatalf("live sessions = %#v, want CLI chat with open SQLite sidecar", got)
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

	originalIDE, originalCLI, originalExecutable, originalResume := cursorIDEProcessRunning, cursorCLIStoreProcesses, cursorProcessExecutable, cursorCLIResumeSessionIDs
	t.Cleanup(func() {
		cursorIDEProcessRunning = originalIDE
		cursorCLIStoreProcesses = originalCLI
		cursorProcessExecutable = originalExecutable
		cursorCLIResumeSessionIDs = originalResume
	})
	cursorCLIResumeSessionIDs = func(string) map[string]bool { return nil }
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

	originalIDE, originalCLI, originalExecutable, originalResume := cursorIDEProcessRunning, cursorCLIStoreProcesses, cursorProcessExecutable, cursorCLIResumeSessionIDs
	t.Cleanup(func() {
		cursorIDEProcessRunning = originalIDE
		cursorCLIStoreProcesses = originalCLI
		cursorProcessExecutable = originalExecutable
		cursorCLIResumeSessionIDs = originalResume
	})
	cursorCLIResumeSessionIDs = func(string) map[string]bool { return nil }
	cursorIDEProcessRunning = func() bool { return false }
	cursorCLIStoreProcesses = func(string) ([]uint32, error) { return []uint32{5678}, nil }
	cursorProcessExecutable = func(uint32) (string, error) {
		return filepath.Join(home, "AppData", "Local", "Microsoft", "WindowsApps", "SearchIndexer.exe"), nil
	}
	if got := loadLiveSessions(); len(got) != 0 {
		t.Fatalf("live sessions = %#v, want unrelated store holder ignored", got)
	}
}

func TestWindowsCursorLivenessUsesResumeProcessCommandLine(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	id := "00000000-0000-4000-8000-000000000006"
	originalIDE, originalResume := cursorIDEProcessRunning, cursorCLIResumeSessionIDs
	t.Cleanup(func() {
		cursorIDEProcessRunning = originalIDE
		cursorCLIResumeSessionIDs = originalResume
	})
	cursorIDEProcessRunning = func() bool { return false }
	cursorCLIResumeSessionIDs = func(string) map[string]bool { return map[string]bool{id: true} }
	if got := loadLiveSessions(); got[id] != entrypointCLI {
		t.Fatalf("live sessions = %#v, want CLI resume process", got)
	}
}

func TestCursorResumeIDFromWindowsCommandLine(t *testing.T) {
	id := "00000000-0000-4000-8000-000000000007"
	home := `C:\Users\person`
	executable := `C:\Users\person\AppData\Local\cursor-agent\versions\2026.09.18-build\node.exe`
	entrypoint := `C:\Users\person\AppData\Local\cursor-agent\versions\2026.09.18-build\index.js`
	for _, commandLine := range []string{
		`"` + executable + `" "` + entrypoint + `" --resume ` + id,
		`"` + executable + `" "` + entrypoint + `" --resume=` + id,
	} {
		if got := cursorResumeID(home, executable, commandLine); got != id {
			t.Fatalf("cursorResumeID(%q) = %q, want %q", commandLine, got, id)
		}
	}
	for _, commandLine := range []string{
		`"` + executable + `" "C:\other\index.js" --resume ` + id,
		`"` + executable + `" "` + entrypoint + `" -- --resume ` + id,
		`"` + executable + `" "` + entrypoint + `" worker-server`,
	} {
		if got := cursorResumeID(home, executable, commandLine); got != "" {
			t.Fatalf("cursorResumeID(%q) matched unrelated session %q", commandLine, got)
		}
	}
}

func TestCursorResumeIDAcceptsPackagedLocalCacheAlias(t *testing.T) {
	home := `C:\Users\person`
	id := "00000000-0000-4000-8000-000000000008"
	packageFamily := "OpenAI.Codex_test"
	originalSameFile := cursorSameFile
	t.Cleanup(func() { cursorSameFile = originalSameFile })
	cursorSameFile = func(string, string) bool { return true }
	executable := filepath.Join(home, "AppData", "Local", "Packages", packageFamily, "LocalCache", "Local", "cursor-agent", "versions", "2026.09.18-build", "node.exe")
	entrypoint := filepath.Join(home, "AppData", "Local", "cursor-agent", "versions", "2026.09.18-build", "index.js")
	commandLine := windows.ComposeCommandLine([]string{entrypoint, "--resume", id})
	commandLine = windows.ComposeCommandLine([]string{executable}) + " " + commandLine
	if got := cursorResumeID(home, executable, commandLine); got != id {
		t.Fatalf("cursorResumeID(%q) = %q, want %q", commandLine, got, id)
	}
}

func TestProcessCommandLineReadsCurrentWindowsProcess(t *testing.T) {
	commandLine, err := processCommandLine(uint32(os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	arguments, err := windows.DecomposeCommandLine(commandLine)
	if err != nil {
		t.Fatal(err)
	}
	if len(arguments) == 0 || !strings.EqualFold(filepath.Clean(arguments[0]), filepath.Clean(executable)) {
		t.Fatalf("process command line = %q, want executable %q", commandLine, executable)
	}
}

func TestCursorAgentExecutableRequiresSameFileForVirtualAlias(t *testing.T) {
	home := t.TempDir()
	relative := filepath.Join("versions", "2026.09.18-build", "node.exe")
	logical := filepath.Join(home, "AppData", "Local", "cursor-agent", relative)
	virtual := filepath.Join(home, "AppData", "Local", "Packages", "OpenAI.Codex_test", "LocalCache", "Local", "cursor-agent", relative)
	unrelated := filepath.Join(home, "AppData", "Local", "Packages", "Unrelated.App_test", "LocalCache", "Local", "cursor-agent", relative)
	for _, path := range []string{logical, virtual, unrelated} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(logical, []byte("cursor agent"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(logical, virtual); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unrelated, []byte("cursor agent"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !isCursorAgentExecutable(home, virtual) {
		t.Fatal("same-file virtual alias was not recognized")
	}
	if isCursorAgentExecutable(home, unrelated) {
		t.Fatal("different-file virtual alias was recognized")
	}
}

func TestIsCursorAgentExecutable(t *testing.T) {
	home := filepath.Join("C:\\", "Users", "person")
	packageFamily := "OpenAI.Codex_test"
	originalSameFile := cursorSameFile
	t.Cleanup(func() { cursorSameFile = originalSameFile })
	cursorSameFile = func(left, _ string) bool {
		return !strings.Contains(left, "Unrelated.App_test")
	}
	if !isCursorAgentExecutable(home, filepath.Join(home, "AppData", "Local", "cursor-agent", "versions", "2026.09.18-build", "node.exe")) {
		t.Fatal("installed cursor-agent node.exe was not recognized")
	}
	if !isCursorAgentExecutable(home, filepath.Join(home, "AppData", "Local", "cursor-agent", "node.exe")) {
		t.Fatal("direct cursor-agent node.exe was not recognized")
	}
	if isCursorAgentExecutable(home, filepath.Join(home, "AppData", "Local", "Programs", "cursor", "Cursor.exe")) {
		t.Fatal("Cursor IDE executable was recognized as cursor-agent")
	}
	virtualized := filepath.Join(home, "AppData", "Local", "Packages", packageFamily, "LocalCache", "Local", "cursor-agent", "versions", "2026.09.18-build", "node.exe")
	if !isCursorAgentExecutable(home, virtualized) {
		t.Fatal("package-virtualized cursor-agent node.exe was not recognized")
	}
	unrelated := filepath.Join(home, "AppData", "Local", "Packages", "Unrelated.App_test", "LocalCache", "Local", "cursor-agent", "versions", "2026.09.18-build", "node.exe")
	if isCursorAgentExecutable(home, unrelated) {
		t.Fatal("unrelated package-local cursor-agent node.exe was recognized")
	}
}
