package remote

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/claude"
	"github.com/centauri-ai/coslash/collector/internal/vendors/codex"
	"github.com/pkg/sftp"
)

func TestSourceReadsOnlyAllowlistedRegularFiles(t *testing.T) {
	root := t.TempDir()
	transcript := filepath.Join(root, ".claude", "projects", "repo", "session.jsonl")
	writeTestFile(t, transcript, "{\"ok\":true}\n")
	writeTestFile(t, filepath.Join(root, "secret.txt"), "secret")
	source, err := newSource(localOperations(root), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	file, err := source.Open(transcript)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	if string(data) != "{\"ok\":true}\n" {
		t.Fatalf("unexpected transcript: %q", data)
	}
	if _, err := source.Open(filepath.Join(root, "secret.txt")); !errors.Is(err, ErrPathDenied) {
		t.Fatalf("expected path denial, got %v", err)
	}
	if _, err := source.Stat("/proc/not-a-pid"); !errors.Is(err, ErrPathDenied) {
		t.Fatalf("expected proc denial, got %v", err)
	}
}

func TestSourceRejectsSymlinkAndCanonicalEscape(t *testing.T) {
	root := t.TempDir()
	projects := filepath.Join(root, ".claude", "projects")
	writeTestFile(t, filepath.Join(root, "outside.jsonl"), "{}\n")
	if err := os.MkdirAll(projects, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(projects, "escape.jsonl")
	if err := os.Symlink(filepath.Join(root, "outside.jsonl"), link); err != nil {
		t.Fatal(err)
	}
	source, err := newSource(localOperations(root), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Open(link); !errors.Is(err, ErrSymlink) {
		t.Fatalf("expected symlink rejection, got %v", err)
	}
}

func TestSourceRejectsUnreadableAllowedRoot(t *testing.T) {
	root := t.TempDir()
	projects := filepath.Join(root, ".claude", "projects")
	if err := os.MkdirAll(projects, 0o700); err != nil {
		t.Fatal(err)
	}
	operations := localOperations(root)
	lstat := operations.lstat
	operations.lstat = func(name string) (os.FileInfo, error) {
		if name == projects {
			return nil, fs.ErrPermission
		}
		return lstat(name)
	}
	if _, err := newSource(operations, Limits{}); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("expected permission error, got %v", err)
	}
}

func TestSourceEnforcesFileTotalEntryAndDepthLimits(t *testing.T) {
	root := t.TempDir()
	projects := filepath.Join(root, ".claude", "projects")
	writeTestFile(t, filepath.Join(projects, "large.jsonl"), "12345")
	writeTestFile(t, filepath.Join(projects, "small.jsonl"), "123")
	writeTestFile(t, filepath.Join(projects, "deep", "child.jsonl"), "1")

	fileLimited, err := newSource(localOperations(root), Limits{MaxFileBytes: 4})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fileLimited.Open(filepath.Join(projects, "large.jsonl")); !errors.Is(err, ErrFileLimit) {
		t.Fatalf("expected file limit, got %v", err)
	}

	totalLimited, err := newSource(localOperations(root), Limits{MaxFileBytes: 8, MaxTotalBytes: 2})
	if err != nil {
		t.Fatal(err)
	}
	file, err := totalLimited.Open(filepath.Join(projects, "small.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(file); !errors.Is(err, ErrTotalLimit) {
		t.Fatalf("expected total limit, got %v", err)
	}
	_ = file.Close()

	entryLimited, err := newSource(localOperations(root), Limits{MaxEntries: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entryLimited.ReadDir(projects); !errors.Is(err, ErrEntryLimit) {
		t.Fatalf("expected entry limit, got %v", err)
	}

	depthLimited, err := newSource(localOperations(root), Limits{MaxDepth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := depthLimited.Open(filepath.Join(projects, "deep", "child.jsonl")); !errors.Is(err, ErrDepthLimit) {
		t.Fatalf("expected depth limit, got %v", err)
	}
}

func TestApprovedDefaultFileBoundary(t *testing.T) {
	root := t.TempDir()
	name := filepath.Join(root, ".codex", "sessions", "boundary.jsonl")
	writeTestFile(t, name, "")
	if err := os.Truncate(name, DefaultMaxFileBytes); err != nil {
		t.Fatal(err)
	}
	source, err := newSource(localOperations(root), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	file, err := source.Open(name)
	if err != nil {
		t.Fatalf("file at limit was rejected: %v", err)
	}
	_ = file.Close()
	if err := os.Truncate(name, DefaultMaxFileBytes+1); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Open(name); !errors.Is(err, ErrFileLimit) {
		t.Fatalf("expected file over limit to fail, got %v", err)
	}
}

func TestApprovedDefaultGuardrails(t *testing.T) {
	if DefaultMaxFileBytes != 32<<20 || DefaultMaxTotalBytes != 128<<20 {
		t.Fatalf("unexpected byte limits: file=%d total=%d", DefaultMaxFileBytes, DefaultMaxTotalBytes)
	}
	if DefaultDeadline != 30*time.Second || DefaultConnectTimeout != 8*time.Second {
		t.Fatalf("unexpected deadlines: refresh=%s connect=%s", DefaultDeadline, DefaultConnectTimeout)
	}
	if vendors.MaxCandidateFilesPerAgent != 2_000 {
		t.Fatalf("unexpected candidate limit: %d", vendors.MaxCandidateFilesPerAgent)
	}
}

func TestSourceWorksThroughSFTPProtocol(t *testing.T) {
	root := t.TempDir()
	transcript := filepath.Join(root, ".codex", "sessions", "2026", "08", "session.jsonl")
	writeTestFile(t, transcript, "{}\n")
	client, closeServer := testSFTPClient(t, root)
	defer closeServer()
	source, err := newSource(sftpOperations{
		realPath: client.RealPath,
		lstat:    client.Lstat,
		readDir:  client.ReadDir,
		open: func(path string) (io.ReadCloser, error) {
			return client.Open(path)
		},
	}, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	file, err := source.Open(transcript)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	if string(data) != "{}\n" {
		t.Fatalf("unexpected data: %q", data)
	}
}

func TestRemoteCollectorsParseThroughSFTPOnMac(t *testing.T) {
	root := t.TempDir()
	claudeID := "11111111-1111-4111-8111-111111111111"
	writeTestFile(
		t,
		filepath.Join(root, ".claude", "projects", "repo", claudeID+".jsonl"),
		`{"sessionId":"`+claudeID+`","cwd":"/work/claude","gitBranch":"main","timestamp":"2026-08-21T10:00:00Z","type":"user","message":{"content":"hello"}}`+"\n",
	)
	codexID := "22222222-2222-4222-8222-222222222222"
	writeTestFile(
		t,
		filepath.Join(root, ".codex", "sessions", "2026", "08", "21", "rollout-2026-08-21T10-00-00-"+codexID+".jsonl"),
		`{"timestamp":"2026-08-21T10:00:00Z","type":"session_meta","payload":{"id":"`+codexID+`","session_id":"`+codexID+`","cwd":"/work/codex","git":{"branch":"topic"}}}`+"\n"+
			`{"timestamp":"2026-08-21T10:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"hello"}}`+"\n",
	)
	client, closeServer := testSFTPClient(t, root)
	defer closeServer()
	source, err := newSource(sftpOperations{
		realPath: client.RealPath,
		lstat:    client.Lstat,
		readDir:  client.ReadDir,
		open: func(path string) (io.ReadCloser, error) {
			return client.Open(path)
		},
	}, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	claudeCollection, err := claude.CollectRemote(source, root, 0, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(claudeCollection.Sessions) != 1 {
		t.Fatalf("unexpected Claude sessions: %#v", claudeCollection.Sessions)
	}
	claudeSession := claudeCollection.Sessions[0].Session
	if claudeSession.ID != claudeID || claudeSession.WorkingDirectory != "/work/claude" {
		t.Fatalf("unexpected Claude session: %#v", claudeSession)
	}
	localClaude, _, err := claude.CollectSource(
		vendors.LocalReadSource,
		claude.ProjectsRoot(root),
		0,
		vendors.EmptySessionMetadata(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !equalParsedSessions(claudeCollection.Sessions, localClaude) {
		t.Fatalf("Claude SFTP facts differ from OS facts:\nSFTP %#v\nOS %#v", claudeCollection.Sessions, localClaude)
	}
	codexCollection, err := codex.CollectRemote(source, root, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(codexCollection.Sessions) != 1 {
		t.Fatalf("unexpected Codex sessions: %#v", codexCollection.Sessions)
	}
	codexSession := codexCollection.Sessions[0].Session
	if codexSession.ID != codexID || codexSession.WorkingDirectory != "/work/codex" {
		t.Fatalf("unexpected Codex session: %#v", codexSession)
	}
	localCodex, _, err := codex.CollectSource(
		vendors.LocalReadSource,
		root,
		0,
		vendors.EmptySessionMetadata(),
		func(string, string) bool { return true },
	)
	if err != nil {
		t.Fatal(err)
	}
	if !equalParsedSessions(codexCollection.Sessions, localCodex) {
		t.Fatalf("Codex SFTP facts differ from OS facts:\nSFTP %#v\nOS %#v", codexCollection.Sessions, localCodex)
	}
}

func TestRemoteCollectionFailsInsteadOfServingPartialParse(t *testing.T) {
	root := t.TempDir()
	writeTestFile(
		t,
		filepath.Join(root, ".claude", "projects", "repo", "broken.jsonl"),
		"{not-json}\n",
	)
	source, err := newSource(localOperations(root), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := claude.CollectRemote(source, root, 0, time.Now()); err == nil {
		t.Fatal("expected strict remote parse failure")
	}
}

func TestCodexForkAccountingReadsParentThroughSource(t *testing.T) {
	root := t.TempDir()
	parentID := "33333333-3333-4333-8333-333333333333"
	forkID := "44444444-4444-4444-8444-444444444444"
	parentPath := filepath.Join(
		root, ".codex", "sessions", "2026", "08", "21",
		"rollout-2026-08-21T10-00-00-"+parentID+".jsonl",
	)
	forkPath := filepath.Join(
		root, ".codex", "sessions", "2026", "08", "21",
		"rollout-2026-08-21T10-01-00-"+forkID+".jsonl",
	)
	tokenRows := func(id, forkedFrom string) string {
		return `{"timestamp":"2026-08-21T10:00:00Z","type":"session_meta","payload":{"id":"` + id + `","session_id":"` + id + `","forked_from_id":"` + forkedFrom + `","cwd":"/work"}}` + "\n" +
			`{"timestamp":"2026-08-21T10:00:01Z","type":"turn_context","payload":{"model":"gpt-5"}}` + "\n" +
			`{"timestamp":"2026-08-21T10:00:02Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":10,"output_tokens":2},"last_token_usage":{"input_tokens":10,"output_tokens":2}}}}` + "\n"
	}
	writeTestFile(t, parentPath, tokenRows(parentID, ""))
	writeTestFile(t, forkPath, tokenRows(forkID, parentID))
	source, err := newSource(localOperations(root), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	collection, err := codex.CollectRemote(source, root, 0)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]*vendors.ParsedSession{}
	for _, parsed := range collection.Sessions {
		byID[parsed.Session.ID] = parsed
	}
	if byID[parentID] == nil || byID[forkID] == nil {
		t.Fatalf("missing fork family: %#v", byID)
	}
	if got := byID[parentID].Session.Tokens["gpt-5"].InputTokens; got != 10 {
		t.Fatalf("parent input tokens = %d, want 10", got)
	}
	if got := byID[forkID].Session.Tokens["gpt-5"].InputTokens; got != 0 {
		t.Fatalf("fork inherited input tokens = %d, want 0", got)
	}
}

func TestMeasuredBoundedSFTPRead(t *testing.T) {
	root := t.TempDir()
	name := filepath.Join(root, ".claude", "projects", "repo", "large.jsonl")
	payload := strings.Repeat("x", 4<<20)
	writeTestFile(t, name, payload)
	client, closeServer := testSFTPClient(t, root)
	defer closeServer()
	source, err := newSource(sftpOperations{
		realPath: client.RealPath,
		lstat:    client.Lstat,
		readDir:  client.ReadDir,
		open: func(path string) (io.ReadCloser, error) {
			return client.Open(path)
		},
	}, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	file, err := source.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	written, err := io.Copy(io.Discard, file)
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	if written != int64(len(payload)) || source.bytes.Load() != written {
		t.Fatalf("read %d bytes, counter %d", written, source.bytes.Load())
	}
	t.Logf("read %d bytes through SFTP in %s", written, time.Since(started))
}

func equalParsedSessions(left, right []*vendors.ParsedSession) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		leftItem := *left[index]
		rightItem := *right[index]
		// SFTP v3 reports modification times at second precision.
		leftItem.LogModifiedAtMs = 0
		rightItem.LogModifiedAtMs = 0
		if !reflect.DeepEqual(leftItem, rightItem) {
			return false
		}
	}
	return true
}

func localOperations(root string) sftpOperations {
	return sftpOperations{
		realPath: func(name string) (string, error) {
			if name == "." {
				return root, nil
			}
			return filepath.EvalSymlinks(name)
		},
		lstat: os.Lstat,
		readDir: func(name string) ([]os.FileInfo, error) {
			entries, err := os.ReadDir(name)
			if err != nil {
				return nil, err
			}
			infos := make([]os.FileInfo, 0, len(entries))
			for _, entry := range entries {
				info, err := entry.Info()
				if err != nil {
					return nil, err
				}
				infos = append(infos, info)
			}
			return infos, nil
		},
		open: func(name string) (io.ReadCloser, error) { return os.Open(name) },
	}
}

func writeTestFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

type pipeReadWriteCloser struct {
	io.Reader
	io.WriteCloser
}

func testSFTPClient(t *testing.T, root string) (*sftp.Client, func()) {
	t.Helper()
	clientReader, serverWriter := io.Pipe()
	serverReader, clientWriter := io.Pipe()
	serverConn := pipeReadWriteCloser{Reader: serverReader, WriteCloser: serverWriter}
	server, err := sftp.NewServer(
		serverConn,
		sftp.ReadOnly(),
		sftp.WithServerWorkingDirectory(root),
	)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		_ = server.Serve()
		_ = server.Close()
		close(done)
	}()
	client, err := sftp.NewClientPipe(clientReader, clientWriter)
	if err != nil {
		t.Fatal(err)
	}
	return client, func() {
		_ = client.Close()
		_ = serverReader.Close()
		_ = serverWriter.Close()
		<-done
	}
}
