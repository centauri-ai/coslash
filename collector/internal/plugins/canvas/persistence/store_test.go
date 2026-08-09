package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/contracts"
)

func testSession() contracts.SessionIdentity {
	return contracts.SessionIdentity{Agent: "claude", ID: "018f2c3d-4e5f-6a7b-8c9d-0e1f2a3b4c5d"}
}

func newStore(t *testing.T, options Options) (*Store, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "canvas")
	if options.Now == nil {
		moment := time.Date(2026, 8, 8, 22, 0, 0, 0, time.UTC)
		options.Now = func() time.Time {
			moment = moment.Add(time.Second)
			return moment
		}
	}
	store, err := Open(t.Context(), root, options)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, root
}

func save(t *testing.T, store *Store, session contracts.SessionIdentity, expected uint64, state string) contracts.WorkspaceDocument {
	t.Helper()
	document, err := store.Save(t.Context(), session, contracts.WorkspaceWrite{
		SchemaVersion:    SchemaVersion,
		ExpectedRevision: expected,
		State:            json.RawMessage(state),
	})
	if err != nil {
		t.Fatalf("save at revision %d: %v", expected, err)
	}
	return document
}

func TestLoadMissingSessionReturnsEmptyRevision(t *testing.T) {
	t.Parallel()
	store, _ := newStore(t, Options{})

	document, err := store.Load(t.Context(), testSession())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if document.Revision != 0 {
		t.Fatalf("revision = %d, want 0", document.Revision)
	}
	if string(document.State) != "null" {
		t.Fatalf("state = %s, want null", document.State)
	}
	if document.Session != testSession() {
		t.Fatalf("session = %+v, want %+v", document.Session, testSession())
	}
}

func TestFirstWriteAndReadBack(t *testing.T) {
	t.Parallel()
	store, _ := newStore(t, Options{})
	session := testSession()

	written := save(t, store, session, 0, `{"layout": {"session": {"x": 16}}}`)
	if written.Revision != 1 {
		t.Fatalf("revision = %d, want 1", written.Revision)
	}
	if written.UpdatedAt.IsZero() {
		t.Fatal("updatedAt was not stamped")
	}

	loaded, err := store.Load(t.Context(), session)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Revision != 1 {
		t.Fatalf("loaded revision = %d, want 1", loaded.Revision)
	}
	if string(loaded.State) != `{"layout":{"session":{"x":16}}}` {
		t.Fatalf("state = %s, want compacted round trip", loaded.State)
	}
	if !loaded.UpdatedAt.Equal(written.UpdatedAt) {
		t.Fatalf("updatedAt = %v, want %v", loaded.UpdatedAt, written.UpdatedAt)
	}
}

func TestRevisionConflictReportsActualRevision(t *testing.T) {
	t.Parallel()
	store, _ := newStore(t, Options{})
	session := testSession()

	save(t, store, session, 0, `{"a":1}`)
	save(t, store, session, 1, `{"a":2}`)

	_, err := store.Save(t.Context(), session, contracts.WorkspaceWrite{
		SchemaVersion:    SchemaVersion,
		ExpectedRevision: 1,
		State:            json.RawMessage(`{"a":3}`),
	})
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("error = %v, want ConflictError", err)
	}
	if conflict.Actual != 2 || conflict.Expected != 1 {
		t.Fatalf("conflict = %+v, want expected 1 actual 2", conflict)
	}
	if !errors.Is(err, ErrConflict) {
		t.Fatal("conflict does not unwrap to ErrConflict")
	}

	loaded, err := store.Load(t.Context(), session)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if string(loaded.State) != `{"a":2}` {
		t.Fatalf("state = %s, want the rejected write to leave revision 2 intact", loaded.State)
	}
}

func TestConcurrentClientsNeverLoseAnUpdate(t *testing.T) {
	t.Parallel()
	store, _ := newStore(t, Options{})
	session := testSession()
	const writers = 24

	var group sync.WaitGroup
	group.Add(writers)
	for range writers {
		go func() {
			defer group.Done()
			for {
				current, err := store.Load(t.Context(), session)
				if err != nil {
					t.Errorf("load: %v", err)
					return
				}
				counter := 0
				if current.Revision > 0 {
					var state struct {
						Counter int `json:"counter"`
					}
					if err := json.Unmarshal(current.State, &state); err != nil {
						t.Errorf("decode state: %v", err)
						return
					}
					counter = state.Counter
				}
				_, err = store.Save(t.Context(), session, contracts.WorkspaceWrite{
					SchemaVersion:    SchemaVersion,
					ExpectedRevision: current.Revision,
					State:            json.RawMessage(fmt.Sprintf(`{"counter":%d}`, counter+1)),
				})
				if err == nil {
					return
				}
				if !errors.Is(err, ErrConflict) {
					t.Errorf("save: %v", err)
					return
				}
			}
		}()
	}
	group.Wait()

	final, err := store.Load(t.Context(), session)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var state struct {
		Counter int `json:"counter"`
	}
	if err := json.Unmarshal(final.State, &state); err != nil {
		t.Fatalf("decode final state: %v", err)
	}
	if state.Counter != writers {
		t.Fatalf("counter = %d, want %d; an update was lost", state.Counter, writers)
	}
	if final.Revision != uint64(writers) {
		t.Fatalf("revision = %d, want %d", final.Revision, writers)
	}
	if size := store.locks.size(); size != 0 {
		t.Fatalf("retained %d key locks, want 0", size)
	}
}

func TestCorruptDocumentIsReportedAndRecoverable(t *testing.T) {
	t.Parallel()
	store, root := newStore(t, Options{})
	session := testSession()
	save(t, store, session, 0, `{"a":1}`)

	path := filepath.Join(root, filepath.FromSlash(documentName(session)))
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("corrupt document: %v", err)
	}

	_, err := store.Load(t.Context(), session)
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("load error = %v, want ErrCorrupt", err)
	}
	if strings.Contains(err.Error(), root) {
		t.Fatalf("corruption error leaked the private path: %v", err)
	}

	// A non-zero expected revision must not silently discard a document we
	// could not decode.
	_, err = store.Save(t.Context(), session, contracts.WorkspaceWrite{
		SchemaVersion:    SchemaVersion,
		ExpectedRevision: 1,
		State:            json.RawMessage(`{"a":2}`),
	})
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("save error = %v, want ErrCorrupt", err)
	}

	recovered := save(t, store, session, 0, `{"a":3}`)
	if recovered.Revision != 1 {
		t.Fatalf("recovered revision = %d, want 1", recovered.Revision)
	}
	loaded, err := store.Load(t.Context(), session)
	if err != nil {
		t.Fatalf("load after recovery: %v", err)
	}
	if string(loaded.State) != `{"a":3}` {
		t.Fatalf("state = %s, want the recovery write", loaded.State)
	}
}

func TestDocumentIdentityMismatchIsCorruption(t *testing.T) {
	t.Parallel()
	store, root := newStore(t, Options{})
	session := testSession()
	save(t, store, session, 0, `{"a":1}`)

	forged, err := json.Marshal(contracts.WorkspaceDocument{
		SchemaVersion: SchemaVersion,
		Revision:      9,
		Session:       contracts.SessionIdentity{Agent: "codex", ID: "someone-else"},
		State:         json.RawMessage(`{"a":1}`),
		UpdatedAt:     time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("marshal forged document: %v", err)
	}
	path := filepath.Join(root, filepath.FromSlash(documentName(session)))
	if err := os.WriteFile(path, forged, 0o600); err != nil {
		t.Fatalf("write forged document: %v", err)
	}

	if _, err := store.Load(t.Context(), session); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("load error = %v, want ErrCorrupt for a mismatched identity", err)
	}
}

func TestUnsupportedSchemaIsRejected(t *testing.T) {
	t.Parallel()
	store, _ := newStore(t, Options{})

	_, err := store.Save(t.Context(), testSession(), contracts.WorkspaceWrite{
		SchemaVersion:    SchemaVersion + 1,
		ExpectedRevision: 0,
		State:            json.RawMessage(`{}`),
	})
	if !errors.Is(err, ErrSchemaUnsupported) {
		t.Fatalf("error = %v, want ErrSchemaUnsupported", err)
	}
}

func TestStateBoundsAreEnforced(t *testing.T) {
	t.Parallel()
	store, _ := newStore(t, Options{MaxStateBytes: 128})

	oversized := fmt.Sprintf(`{"blob":%q}`, strings.Repeat("x", 256))
	_, err := store.Save(t.Context(), testSession(), contracts.WorkspaceWrite{
		SchemaVersion:    SchemaVersion,
		ExpectedRevision: 0,
		State:            json.RawMessage(oversized),
	})
	if !errors.Is(err, ErrStateTooLarge) {
		t.Fatalf("error = %v, want ErrStateTooLarge", err)
	}

	// Whitespace alone must not push a payload over the bound.
	padded := "{\n  \"a\" : 1\n}" + strings.Repeat(" ", 120)
	if _, err := store.Save(t.Context(), testSession(), contracts.WorkspaceWrite{
		SchemaVersion:    SchemaVersion,
		ExpectedRevision: 0,
		State:            json.RawMessage(padded),
	}); err != nil {
		t.Fatalf("padded save: %v", err)
	}
}

func TestInvalidStateIsRejected(t *testing.T) {
	t.Parallel()
	store, _ := newStore(t, Options{})

	for name, state := range map[string]string{
		"empty":     "",
		"notJSON":   "{oops",
		"trailing":  `{"a":1}{"b":2}`,
		"truncated": `{"a":`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := store.Save(t.Context(), testSession(), contracts.WorkspaceWrite{
				SchemaVersion:    SchemaVersion,
				ExpectedRevision: 0,
				State:            json.RawMessage(state),
			})
			if !errors.Is(err, ErrInvalidState) {
				t.Fatalf("error = %v, want ErrInvalidState", err)
			}
		})
	}
}

func TestQuotaBoundsNewDocumentsOnly(t *testing.T) {
	t.Parallel()
	store, _ := newStore(t, Options{MaxDocuments: 2})

	first := contracts.SessionIdentity{Agent: "claude", ID: "one"}
	second := contracts.SessionIdentity{Agent: "codex", ID: "two"}
	third := contracts.SessionIdentity{Agent: "claude", ID: "three"}
	save(t, store, first, 0, `{"a":1}`)
	save(t, store, second, 0, `{"a":1}`)

	_, err := store.Save(t.Context(), third, contracts.WorkspaceWrite{
		SchemaVersion:    SchemaVersion,
		ExpectedRevision: 0,
		State:            json.RawMessage(`{"a":1}`),
	})
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("error = %v, want ErrQuotaExceeded", err)
	}

	// Existing workspaces stay writable once the bound is reached.
	save(t, store, first, 1, `{"a":2}`)

	document, err := store.Load(t.Context(), third)
	if err != nil {
		t.Fatalf("load rejected session: %v", err)
	}
	if document.Revision != 0 {
		t.Fatalf("rejected session revision = %d, want 0", document.Revision)
	}
}

func TestStateSurvivesRestart(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "canvas")
	session := testSession()

	first, err := Open(t.Context(), root, Options{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := first.Save(t.Context(), session, contracts.WorkspaceWrite{
		SchemaVersion:    SchemaVersion,
		ExpectedRevision: 0,
		State:            json.RawMessage(`{"pinIds":["goal"]}`),
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	second, err := Open(t.Context(), root, Options{})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer second.Close()

	loaded, err := second.Load(t.Context(), session)
	if err != nil {
		t.Fatalf("load after restart: %v", err)
	}
	if loaded.Revision != 1 || string(loaded.State) != `{"pinIds":["goal"]}` {
		t.Fatalf("restart lost state: revision %d state %s", loaded.Revision, loaded.State)
	}

	entries, err := second.List(t.Context())
	if err != nil {
		t.Fatalf("list after restart: %v", err)
	}
	if len(entries) != 1 || entries[0].Session != session || entries[0].Revision != 1 {
		t.Fatalf("entries = %+v, want the restored session at revision 1", entries)
	}
}

func TestListIsOrderedAndCatalogsEveryWorkspace(t *testing.T) {
	t.Parallel()
	store, _ := newStore(t, Options{})

	sessions := []contracts.SessionIdentity{
		{Agent: "codex", ID: "b"},
		{Agent: "claude", ID: "b"},
		{Agent: "claude", ID: "a"},
	}
	for _, session := range sessions {
		save(t, store, session, 0, `{"a":1}`)
	}

	entries, err := store.List(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	got := make([]string, 0, len(entries))
	for _, entry := range entries {
		got = append(got, entry.Session.Agent+"/"+entry.Session.ID)
	}
	want := []string{"claude/a", "claude/b", "codex/b"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("entries = %v, want %v", got, want)
	}
}

func TestCorruptIndexDoesNotHideDocuments(t *testing.T) {
	t.Parallel()
	store, root := newStore(t, Options{})
	session := testSession()
	save(t, store, session, 0, `{"a":1}`)

	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(indexName)), []byte("{broken"), 0o600); err != nil {
		t.Fatalf("corrupt index: %v", err)
	}

	loaded, err := store.Load(t.Context(), session)
	if err != nil {
		t.Fatalf("load with corrupt index: %v", err)
	}
	if loaded.Revision != 1 {
		t.Fatalf("revision = %d, want 1", loaded.Revision)
	}

	// The catalog is derived data and repairs itself on the next write.
	save(t, store, session, 1, `{"a":2}`)
	entries, err := store.List(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 1 || entries[0].Revision != 2 {
		t.Fatalf("entries = %+v, want the repaired catalog at revision 2", entries)
	}
}

func TestIdentitiesDifferingOnlyByCaseDoNotCollide(t *testing.T) {
	t.Parallel()
	store, _ := newStore(t, Options{})

	lower := contracts.SessionIdentity{Agent: "claude", ID: "abcdef"}
	upper := contracts.SessionIdentity{Agent: "claude", ID: "ABCDEF"}
	save(t, store, lower, 0, `{"which":"lower"}`)
	save(t, store, upper, 0, `{"which":"upper"}`)

	loadedLower, err := store.Load(t.Context(), lower)
	if err != nil {
		t.Fatalf("load lower: %v", err)
	}
	loadedUpper, err := store.Load(t.Context(), upper)
	if err != nil {
		t.Fatalf("load upper: %v", err)
	}
	if string(loadedLower.State) != `{"which":"lower"}` || string(loadedUpper.State) != `{"which":"upper"}` {
		t.Fatalf("case-only identity difference collided: %s / %s", loadedLower.State, loadedUpper.State)
	}
}

func TestSameIDAcrossAgentsIsSeparate(t *testing.T) {
	t.Parallel()
	store, _ := newStore(t, Options{})

	claude := contracts.SessionIdentity{Agent: "claude", ID: "shared"}
	codex := contracts.SessionIdentity{Agent: "codex", ID: "shared"}
	save(t, store, claude, 0, `{"agent":"claude"}`)
	save(t, store, codex, 0, `{"agent":"codex"}`)

	loaded, err := store.Load(t.Context(), claude)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if string(loaded.State) != `{"agent":"claude"}` {
		t.Fatalf("state = %s; a bare ID was used as the key", loaded.State)
	}
}

func TestInvalidSessionIdentitiesAreRejected(t *testing.T) {
	t.Parallel()
	store, _ := newStore(t, Options{})

	cases := map[string]contracts.SessionIdentity{
		"emptyAgent":   {Agent: "", ID: "abc"},
		"emptyID":      {Agent: "claude", ID: ""},
		"controlChar":  {Agent: "claude", ID: "a\x00b"},
		"newline":      {Agent: "claude", ID: "a\nb"},
		"invalidUTF8":  {Agent: "claude", ID: "\xff\xfe"},
		"oversizedID":  {Agent: "claude", ID: strings.Repeat("a", maxIdentityFieldBytes+1)},
		"traversalID":  {Agent: "claude", ID: "../../../etc/passwd"},
		"absolutePath": {Agent: "claude", ID: "/etc/passwd"},
	}
	for name, session := range cases {
		t.Run(name, func(t *testing.T) {
			err := ValidateSession(session)
			switch name {
			case "traversalID", "absolutePath":
				// These are safe by construction because the identity never
				// becomes a path component, so they are accepted and stored
				// under a digest instead.
				if err != nil {
					t.Fatalf("ValidateSession(%q) = %v, want accepted", name, err)
				}
				document, saveErr := store.Save(t.Context(), session, contracts.WorkspaceWrite{
					SchemaVersion:    SchemaVersion,
					ExpectedRevision: 0,
					State:            json.RawMessage(`{"a":1}`),
				})
				if saveErr != nil {
					t.Fatalf("save: %v", saveErr)
				}
				if document.Session != session {
					t.Fatalf("identity was rewritten: %+v", document.Session)
				}
			default:
				if !errors.Is(err, ErrInvalidSession) {
					t.Fatalf("ValidateSession(%q) = %v, want ErrInvalidSession", name, err)
				}
			}
		})
	}
}

func TestTraversalIdentityStaysInsideTheStore(t *testing.T) {
	t.Parallel()
	store, root := newStore(t, Options{})
	session := contracts.SessionIdentity{Agent: "../..", ID: "../../../etc/passwd"}

	save(t, store, session, 0, `{"a":1}`)

	entries, err := os.ReadDir(filepath.Join(root, documentDirectory))
	if err != nil {
		t.Fatalf("read store directory: %v", err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), "..") || strings.Contains(entry.Name(), "/") {
			t.Fatalf("identity reached the filesystem as %q", entry.Name())
		}
	}
	if _, err := os.Stat(filepath.Join(root, "..", "passwd")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a file escaped the store root: %v", err)
	}
}

func TestSymlinkedDocumentIsRefused(t *testing.T) {
	t.Parallel()
	store, root := newStore(t, Options{})
	session := testSession()

	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte(`{"stolen":true}`), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	path := filepath.Join(root, filepath.FromSlash(documentName(session)))
	if err := os.Symlink(outside, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := store.Load(t.Context(), session); err == nil {
		t.Fatal("load followed a symlink out of the store")
	}
	_, err := store.Save(t.Context(), session, contracts.WorkspaceWrite{
		SchemaVersion:    SchemaVersion,
		ExpectedRevision: 0,
		State:            json.RawMessage(`{"a":1}`),
	})
	if err == nil {
		t.Fatal("save followed a symlink out of the store")
	}
	contents, err := os.ReadFile(outside)
	if err != nil {
		t.Fatalf("read outside file: %v", err)
	}
	if string(contents) != `{"stolen":true}` {
		t.Fatalf("symlink target was overwritten: %s", contents)
	}
}

func TestReadOnlyStoreFailsSafely(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("permission enforcement does not apply to root")
	}
	store, root := newStore(t, Options{})
	session := testSession()
	save(t, store, session, 0, `{"a":1}`)

	directory := filepath.Join(root, documentDirectory)
	if err := os.Chmod(directory, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(directory, 0o700) })

	_, err := store.Save(t.Context(), session, contracts.WorkspaceWrite{
		SchemaVersion:    SchemaVersion,
		ExpectedRevision: 1,
		State:            json.RawMessage(`{"a":2}`),
	})
	if err == nil {
		t.Fatal("save succeeded on a read-only store")
	}
	if Code(err) != CodePersistenceFailed {
		t.Fatalf("code = %s, want %s", Code(err), CodePersistenceFailed)
	}

	// The previously saved workspace must still be readable so the UI stays
	// usable while saving is broken.
	loaded, loadErr := store.Load(t.Context(), session)
	if loadErr != nil {
		t.Fatalf("load on a read-only store: %v", loadErr)
	}
	if loaded.Revision != 1 || string(loaded.State) != `{"a":1}` {
		t.Fatalf("read-only load lost state: revision %d state %s", loaded.Revision, loaded.State)
	}
}

func TestStoredFilesUsePrivateModes(t *testing.T) {
	t.Parallel()
	store, root := newStore(t, Options{})
	save(t, store, testSession(), 0, `{"a":1}`)

	for _, name := range []string{documentName(testSession()), indexName} {
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("%s mode = %v, want no group or world access", name, info.Mode().Perm())
		}
	}
	info, err := os.Stat(root)
	if err != nil {
		t.Fatalf("stat root: %v", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("root mode = %v, want no group or world access", info.Mode().Perm())
	}
}

func TestCancelledContextStopsWork(t *testing.T) {
	t.Parallel()
	store, _ := newStore(t, Options{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := store.Load(ctx, testSession()); !errors.Is(err, context.Canceled) {
		t.Fatalf("load error = %v, want context.Canceled", err)
	}
	_, err := store.Save(ctx, testSession(), contracts.WorkspaceWrite{
		SchemaVersion:    SchemaVersion,
		ExpectedRevision: 0,
		State:            json.RawMessage(`{"a":1}`),
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("save error = %v, want context.Canceled", err)
	}
}

func TestRepeatedLifecycleReleasesKeyLocks(t *testing.T) {
	t.Parallel()
	store, _ := newStore(t, Options{})

	for index := range 50 {
		session := contracts.SessionIdentity{Agent: "claude", ID: fmt.Sprintf("session-%d", index)}
		save(t, store, session, 0, `{"a":1}`)
		if _, err := store.Load(t.Context(), session); err != nil {
			t.Fatalf("load: %v", err)
		}
	}
	if size := store.locks.size(); size != 0 {
		t.Fatalf("retained %d key locks after 50 sessions, want 0", size)
	}
}

func TestOpenRejectsInvalidOptions(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "canvas")

	if _, err := Open(t.Context(), root, Options{MaxStateBytes: -1}); err == nil {
		t.Fatal("negative MaxStateBytes was accepted")
	}
	if _, err := Open(t.Context(), root, Options{MaxDocuments: -1}); err == nil {
		t.Fatal("negative MaxDocuments was accepted")
	}
	if _, err := Open(t.Context(), "", Options{}); err == nil {
		t.Fatal("empty root was accepted")
	}
}
