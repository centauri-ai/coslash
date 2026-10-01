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

func TestLocalDiscoveryUsesEffectiveCodexHome(t *testing.T) {
	home := t.TempDir()
	data := mustCanonicalRoot(t, t.TempDir())
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", data)
	const id = "11111111-2222-4333-8444-555555555555"
	active := writeDiscoveryRollout(t, filepath.Join(data, "sessions"), id, id, "")
	archived := writeDiscoveryRollout(t, filepath.Join(data, "archived_sessions"), "66666666-7777-4888-8999-aaaaaaaaaaaa", "66666666-7777-4888-8999-aaaaaaaaaaaa", id)
	if err := os.WriteFile(filepath.Join(data, "session_index.jsonl"), []byte(`{"id":"`+id+`","thread_name":"synthetic name"}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := Root()
	if err != nil || root != filepath.Join(data, "sessions") {
		t.Fatalf("effective root = %q, %v", root, err)
	}
	files, err := FilesContext(context.Background())
	if err != nil || len(files) != 2 || !containsPath(files, active) || !containsPath(files, archived) {
		t.Fatalf("effective files = %v, %v", files, err)
	}
	names, err := loadThreadNamesContext(context.Background())
	if err != nil || names[id] != "synthetic name" {
		t.Fatalf("effective names = %v, %v", names, err)
	}
	health := Health()
	if health.Err != nil || health.Root != data || health.Sessions != 1 {
		t.Fatalf("effective health = %+v", health)
	}
	explicit, err := filesForHomeSourceContext(context.Background(), vendors.LocalReadSource, home)
	if err != nil || len(explicit) != 0 {
		t.Fatal("explicit-home source inherited local environment")
	}
}

func TestLocalRootMatchesFilesystemCanonicalization(t *testing.T) {
	base := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	actual := filepath.Join(base, "target", "data")
	decoy := filepath.Join(base, "data")
	if err := os.MkdirAll(filepath.Join(base, "target", "subdir"), 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(filepath.Join(base, "target", "subdir"), link); err != nil {
		t.Skip(err)
	}
	override := link + string(filepath.Separator) + ".." + string(filepath.Separator) + "data"
	const id = "11111111-2222-4333-8444-555555555555"
	writeDiscoveryRollout(t, filepath.Join(actual, "sessions"), id, id, "")
	writeDiscoveryRollout(t, filepath.Join(decoy, "sessions"), "66666666-7777-4888-8999-aaaaaaaaaaaa", "66666666-7777-4888-8999-aaaaaaaaaaaa", "")
	canonical, err := filepath.EvalSymlinks(override)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", override)
	files, err := FilesContext(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || SessionIDFromRollout(files[0]) != id {
		t.Fatalf("read decoy storage instead of canonical root %q: %v", canonical, files)
	}
}
func TestLocalOverrideDoesNotRequireHome(t *testing.T) {
	data := t.TempDir()
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("CODEX_HOME", data)
	root, err := Root()
	if err != nil || root != filepath.Join(mustCanonicalRoot(t, data), "sessions") {
		t.Fatalf("valid CODEX_HOME rejected without default home: root=%q err=%v", root, err)
	}
}

func mustCanonicalRoot(t *testing.T, path string) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestLocalRootOverrideBoundaries(t *testing.T) {
	base := t.TempDir()
	t.Chdir(base)
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	if err := os.Mkdir("relative", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("file", []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, override := range []string{"relative", "missing", "file", ""} {
		t.Run(override, func(t *testing.T) {
			t.Setenv("CODEX_HOME", override)
			root, err := Root()
			if override == "relative" {
				if err != nil || root != filepath.Join(mustCanonicalRoot(t, "relative"), "sessions") {
					t.Fatalf("root=%q err=%v", root, err)
				}
			} else {
				if err == nil {
					t.Fatalf("accepted override %q with no default home: %q", override, root)
				}
				if _, err := Files(); err == nil {
					t.Fatal("discovery ignored resolution error")
				}
				if _, err := loadThreadNames(); err == nil {
					t.Fatal("names ignored resolution error")
				}
				if Health().Err == nil {
					t.Fatal("health ignored resolution error")
				}
			}
		})
	}
}
