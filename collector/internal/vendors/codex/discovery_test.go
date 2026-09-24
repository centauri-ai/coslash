package codex

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestLocalDiscoveryIncludesArchivedFamiliesAndDeduplicatesActiveCopies(t *testing.T) {
	home := t.TempDir()
	const (
		rootA  = "11111111-2222-3333-4444-555555555555"
		childA = "66666666-7777-8888-9999-aaaaaaaaaaaa"
		rootB  = "bbbbbbbb-cccc-dddd-eeee-ffffffffffff"
		rootC  = "01234567-89ab-cdef-0123-456789abcdef"
	)
	archived := filepath.Join(home, ".codex", "archived_sessions")
	active := filepath.Join(home, ".codex", "sessions", "2026", "09", "21")
	archiveRootA := writeDiscoveryRollout(t, archived, rootA, rootA, "")
	archiveChildA := writeDiscoveryRollout(t, archived, rootA+"_"+childA, childA, rootA)
	archiveRootB := writeDiscoveryRollout(t, archived, rootB, rootB, "")
	archiveRootC := writeDiscoveryRollout(t, archived, rootC, rootC, "")
	activeRootC := writeDiscoveryRollout(t, active, rootC, rootC, "")

	indexPath := SessionIndexPath(home)
	indexRows := fmt.Sprintf(
		"{\"id\":%q,\"thread_name\":\"Archived A\"}\n{\"id\":%q,\"thread_name\":\"Archived B\"}\n{\"id\":%q,\"thread_name\":\"Archived C\"}\n",
		rootA, rootB, rootC,
	)
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(indexPath, []byte(indexRows), 0o600); err != nil {
		t.Fatal(err)
	}

	source := vendors.LocalReadSource
	files, err := filesForHomeSourceContext(context.Background(), source, home)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 4 {
		t.Fatalf("discovered files = %d, want 4 after active/archive deduplication: %#v", len(files), files)
	}
	memberFiles := map[string]string{}
	headers := HeadersSource(source, files)
	roots := FamilyRoots(headers)
	families := map[string]bool{}
	for file, header := range headers {
		if header.Err != nil {
			t.Fatalf("header for %q: %v", filepath.Base(file), header.Err)
		}
		if previous, duplicate := memberFiles[header.SessionID]; duplicate {
			t.Fatalf("member %q appears twice: %q and %q", header.SessionID, filepath.Base(previous), filepath.Base(file))
		}
		memberFiles[header.SessionID] = file
		families[roots[file]] = true
	}
	for _, id := range []string{rootA, rootB, rootC} {
		if !families[id] {
			t.Fatalf("indexed archived family %q was not discovered", id)
		}
	}
	if memberFiles[rootC] != activeRootC || memberFiles[rootC] == archiveRootC {
		t.Fatalf("active copy was not preferred for duplicate member: %q", filepath.Base(memberFiles[rootC]))
	}

	indexed, present, err := ReadSessionIndexRows(source, home, map[string]bool{rootA: true, rootB: true, rootC: true})
	if err != nil || !present || len(indexed[rootA]) != 1 || len(indexed[rootB]) != 1 || len(indexed[rootC]) != 1 {
		t.Fatalf("archived family index rows = %#v, present = %t, error = %v", indexed, present, err)
	}

	selected := FilesForRootSource(source, files, rootA)
	if len(selected) != 2 || !containsPath(selected, archiveRootA) || !containsPath(selected, archiveChildA) {
		t.Fatalf("exact archived family selection = %#v", selected)
	}
	for id, want := range map[string]string{rootB: archiveRootB, rootC: activeRootC} {
		selected := FilesForRootSource(source, files, id)
		if len(selected) != 1 || selected[0] != want {
			t.Fatalf("exact family selection for %q = %#v, want %q", id, selected, filepath.Base(want))
		}
	}
	parsed, err := ParseFamilyFilesSource(source, home, selected, []string{activeRootC})
	if err != nil || len(parsed) != 2 {
		t.Fatalf("parsed archived family = %d sessions, %v", len(parsed), err)
	}
	parsedMembers := map[string]string{}
	for _, item := range parsed {
		parsedMembers[item.Session.ID] = item.ParentID
	}
	if len(parsedMembers) != 2 || parsedMembers[rootA] != "" || parsedMembers[childA] != rootA {
		t.Fatalf("parsed family membership = %#v", parsedMembers)
	}
}

func TestHealthIncludesArchivedRolloutsWhenActiveTreeIsAbsent(t *testing.T) {
	home := t.TempDir()
	const archivedID = "11111111-2222-3333-4444-555555555555"
	writeDiscoveryRollout(t, ArchivedDir(home), archivedID, archivedID, "")

	health := healthForHomeSourceContext(context.Background(), vendors.LocalReadSource, home)
	if health.Err != nil {
		t.Fatalf("health error = %v", health.Err)
	}
	if health.Missing {
		t.Fatal("health marked the source missing when archived rollouts exist")
	}
	if health.Root != filepath.Join(home, ".codex") {
		t.Fatalf("health root = %q, want combined Codex root %q", health.Root, filepath.Join(home, ".codex"))
	}
	if health.Entries != 1 || health.Sessions != 1 {
		t.Fatalf("health entries/sessions = %d/%d, want 1/1", health.Entries, health.Sessions)
	}
}

func TestHealthDeduplicatesActiveCopies(t *testing.T) {
	home := t.TempDir()
	const duplicateID = "66666666-7777-8888-9999-aaaaaaaaaaaa"
	archived := ArchivedDir(home)
	active := filepath.Join(SessionsRoot(home), "2026", "09", "21")
	archivedCopy := writeDiscoveryRollout(t, archived, duplicateID, duplicateID, "")
	activeCopy := writeDiscoveryRollout(t, active, duplicateID, duplicateID, "")

	health := healthForHomeSourceContext(context.Background(), vendors.LocalReadSource, home)
	if health.Err != nil {
		t.Fatalf("health error = %v", health.Err)
	}
	if health.Missing {
		t.Fatal("health marked the source missing when active rollouts exist")
	}
	if health.Entries != 1 || health.Sessions != 1 {
		t.Fatalf("health entries/sessions = %d/%d, want 1/1", health.Entries, health.Sessions)
	}

	scan, err := scanForHomeSourceContext(context.Background(), vendors.LocalReadSource, home)
	if err != nil {
		t.Fatal(err)
	}
	if !containsPath(scan.Files, activeCopy) || containsPath(scan.Files, archivedCopy) {
		t.Fatalf("health scan did not prefer the active duplicate: %#v", scan.Files)
	}
}

func TestHealthIsMissingWhenBothCodexTreesAreAbsent(t *testing.T) {
	health := healthForHomeSourceContext(context.Background(), vendors.LocalReadSource, t.TempDir())
	if health.Err != nil {
		t.Fatalf("health error = %v", health.Err)
	}
	if !health.Missing {
		t.Fatal("health did not mark Codex missing when both session trees are absent")
	}
}

func writeDiscoveryRollout(t *testing.T, directory, filenameIDs, sessionID, parentID string) string {
	t.Helper()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	parent := ""
	if parentID != "" {
		parent = fmt.Sprintf(",\"parent_thread_id\":%q", parentID)
	}
	content := fmt.Sprintf(
		"{\"timestamp\":\"2026-09-20T10:00:00Z\",\"type\":\"session_meta\",\"payload\":{\"id\":%q,\"session_id\":%q%s}}\n",
		sessionID, sessionID, parent,
	)
	path := filepath.Join(directory, "rollout-2026-09-20T10-00-00-"+filenameIDs+".jsonl")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func containsPath(paths []string, want string) bool {
	for _, path := range paths {
		if path == want {
			return true
		}
	}
	return false
}
