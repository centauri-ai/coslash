package hubclient

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/sessionbackupproducer"
)

const (
	backupWorkspace       = "10000000-0000-4000-8000-000000000001"
	backupRevision        = "30000000-0000-4000-8000-000000000001"
	backupAudienceVersion = "audience-v1"
)

func TestBackupChunkPlanPreservesReviewedExactBytes(t *testing.T) {
	manager, prepared := openBackupFixture(t)
	client := Client{Backup: manager}
	plan, err := client.backupChunkPlan(prepared, 128)
	if err != nil || len(plan) < 2 {
		t.Fatalf("plan=%#v err=%v", plan, err)
	}
	var total int64
	for _, chunk := range plan {
		body, err := client.readBackupChunk(prepared, chunk)
		sum := sha256.Sum256(body)
		if err != nil || int64(len(body)) != chunk.ByteCount || chunk.ByteCount > 128 ||
			hex.EncodeToString(sum[:]) != chunk.SHA256 {
			t.Fatalf("chunk=%#v bytes=%d err=%v", chunk, len(body), err)
		}
		total += chunk.ByteCount
	}
	if total != prepared.Coverage.TotalBytes {
		t.Fatalf("planned bytes=%d want=%d", total, prepared.Coverage.TotalBytes)
	}
	consent := BackupConsent{CompleteBackupSHA256: prepared.BundleID, TotalBytes: total}
	status := backupUploadStatus{CompleteBackupSHA256: prepared.BundleID, TotalBytes: total, ExpectedChunks: len(plan)}
	if !validBackupStatusBinding(status, consent, len(plan)) {
		t.Fatal("exact reviewed status was rejected")
	}
	status.CompleteBackupSHA256 = strings.Repeat("0", 64)
	if validBackupStatusBinding(status, consent, len(plan)) {
		t.Fatal("retargeted status was accepted")
	}
}

func TestBackupChunkReceiptMustMatchReviewedChunk(t *testing.T) {
	chunk := backupChunkSpec{ArtifactOrdinal: 2, ChunkOrdinal: 3, ByteCount: 4, SHA256: strings.Repeat("a", 64)}
	receipt := backupChunkReceipt{ArtifactOrdinal: 2, ChunkOrdinal: 3, ByteCount: 4, SHA256: chunk.SHA256}
	if !validBackupChunkReceipt(receipt, chunk) {
		t.Fatal("matching chunk receipt was rejected")
	}
	receipt.SHA256 = strings.Repeat("b", 64)
	if validBackupChunkReceipt(receipt, chunk) {
		t.Fatal("mismatched chunk receipt was accepted")
	}
}

func TestShareBackupsUploadsCompleteBundleWithDestinationAssertions(t *testing.T) {
	manager, prepared := openBackupFixture(t)
	plan := []backupChunkSpec(nil)
	received := []backupChunkReceipt(nil)
	var receivedBytes int64
	chunkRequests := 0
	createSeen, finalizeSeen, rateLimited := false, false, false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/share-destination":
			_, _ = io.WriteString(w, `{"contractVersion":"hub-share/v1","state":"ready","destination":{"workspaceId":"`+backupWorkspace+`","workspaceName":"Compiler Team","currentMemberCount":2,"resultingMemberCount":2,"currentApprovedSessionCount":0,"historyDisclosure":"Current members","credentialState":"paired","audienceVersion":"`+backupAudienceVersion+`"},"configured":true}`)
		case r.Method == http.MethodGet && r.URL.Path == "/.well-known/coslash-server":
			_, _ = io.WriteString(w, `{"product":"coslash-server","serverId":"server-v3","displayName":"Hub","protocolVersions":["v3"],"snapshotVersions":[],"maxSnapshotBytes":0,"fullSessionVersions":[],"maxFullSessionBytes":0,"maxRequestBytes":1048576,"backupVersions":["session-backup/v1"],"backupUploadVersions":["backup-upload/v1"],"maxBackupBytes":1073741824,"maxBackupChunkBytes":128,"backupWorkspaceBytes":53687091200,"backupUploadExpiresSeconds":86400,"pairingUrl":"","teamUrl":""}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v3/backup-uploads":
			if r.Header.Get("Idempotency-Key") != "backup-idempotency-key-0001" ||
				r.Header.Get(backupWorkspaceHeader) != backupWorkspace || r.Header.Get(backupAudienceHeader) != backupAudienceVersion {
				t.Errorf("create headers = %#v", r.Header)
			}
			var create struct {
				Reviewed string            `json:"reviewedCompleteBackupSha256"`
				Chunks   []backupChunkSpec `json:"chunks"`
			}
			if err := json.NewDecoder(r.Body).Decode(&create); err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			plan = create.Chunks
			if create.Reviewed != prepared.BundleID || len(plan) < 2 {
				t.Errorf("create review=%q chunks=%d", create.Reviewed, len(plan))
			}
			createSeen = true
			first := plan[0]
			received = []backupChunkReceipt{{ArtifactOrdinal: first.ArtifactOrdinal, ChunkOrdinal: first.ChunkOrdinal,
				ByteCount: first.ByteCount, SHA256: first.SHA256, ReceivedAt: time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)}}
			receivedBytes = first.ByteCount
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(backupUploadStatus{UploadID: "20000000-0000-4000-8000-000000000001",
				State: "active", CompleteBackupSHA256: prepared.BundleID, TotalBytes: prepared.Coverage.TotalBytes,
				ExpectedChunks: len(plan), ReceivedBytes: receivedBytes, ReceivedChunks: received})
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/v3/backup-uploads/20000000-0000-4000-8000-000000000001/artifacts/"):
			if r.Header.Get(backupWorkspaceHeader) != backupWorkspace || r.Header.Get(backupAudienceHeader) != backupAudienceVersion {
				t.Errorf("chunk headers = %#v", r.Header)
			}
			if !rateLimited {
				rateLimited = true
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = io.WriteString(w, `{"code":"rate_limited"}`)
				return
			}
			parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
			if len(parts) != 7 || parts[3] != "artifacts" || parts[5] != "chunks" {
				t.Errorf("unexpected chunk path: %s", r.URL.Path)
				w.WriteHeader(http.StatusNotFound)
				return
			}
			artifact, artifactErr := strconv.Atoi(parts[4])
			chunk, chunkErr := strconv.Atoi(parts[6])
			if artifactErr != nil || chunkErr != nil {
				t.Errorf("invalid chunk coordinates: %s", r.URL.Path)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			var expected backupChunkSpec
			found := false
			for _, candidate := range plan {
				if candidate.ArtifactOrdinal == artifact && candidate.ChunkOrdinal == chunk {
					expected = candidate
					found = true
				}
			}
			if !found {
				t.Errorf("chunk %d/%d was absent from create plan", artifact, chunk)
			}
			body, _ := io.ReadAll(r.Body)
			sum := sha256.Sum256(body)
			if int64(len(body)) != expected.ByteCount || hex.EncodeToString(sum[:]) != expected.SHA256 {
				t.Errorf("chunk %d/%d did not match the reviewed plan", artifact, chunk)
			}
			chunkRequests++
			received = append(received, backupChunkReceipt{ArtifactOrdinal: artifact, ChunkOrdinal: chunk,
				ByteCount: expected.ByteCount, SHA256: expected.SHA256,
				ReceivedAt: time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)})
			receivedBytes += expected.ByteCount
			_ = json.NewEncoder(w).Encode(received[len(received)-1])
		case r.Method == http.MethodPost && r.URL.Path == "/v3/backup-uploads/20000000-0000-4000-8000-000000000001/finalize":
			if r.Header.Get(backupWorkspaceHeader) != backupWorkspace || r.Header.Get(backupAudienceHeader) != backupAudienceVersion {
				t.Errorf("finalize headers = %#v", r.Header)
			}
			if len(received) != len(plan) {
				t.Errorf("finalize received %d of %d chunks", len(received), len(plan))
			}
			finalizeSeen = true
			_ = json.NewEncoder(w).Encode(backupUploadStatus{UploadID: "20000000-0000-4000-8000-000000000001",
				State: "completed", CompleteBackupSHA256: prepared.BundleID, TotalBytes: prepared.Coverage.TotalBytes,
				ExpectedChunks: len(plan), ReceivedBytes: receivedBytes, ReceivedChunks: received,
				Result: &backupUploadResult{RevisionID: backupRevision, CompleteBackupSHA256: prepared.BundleID,
					RepositoryID: "40000000-0000-4000-8000-000000000001",
					SharedAt:     time.Date(2026, 9, 22, 20, 0, 0, 0, time.UTC),
					RevisionURL:  "/v3/session-backups/" + backupRevision}})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	base, _ := url.Parse(server.URL)
	client := Client{
		BaseURL: base, Credentials: &memoryCredentials{}, Backup: manager,
		LoadSourceSession: func(sourceID, agent, sessionID string, revision int64) (*session.Session, error) {
			if sourceID != prepared.Selection.SourceID || agent != prepared.Selection.Agent ||
				sessionID != prepared.Selection.SessionID || revision != 123 {
				return nil, errors.New("unexpected source binding")
			}
			return &session.Session{Agent: agent, ID: sessionID, LastActivityTime: revision}, nil
		},
	}
	result, err := client.ShareBackups(context.Background(), BackupShareRequest{
		ContractVersion: BackupShareVersion, RequestID: "request-full-upload", Items: []BackupShareItemRequest{{
			LocalSessionID: prepared.Selection.SourceID + ":" + prepared.Selection.Agent + ":" + prepared.Selection.SessionID,
			Selection:      prepared.Selection, IdempotencyKey: "backup-idempotency-key-0001",
			Consent: BackupConsent{
				PreviewContractVersion: BackupPreviewVersion, BundleID: prepared.BundleID,
				SourceRevision: prepared.Manifest.Source.SourceRevision, SelectedRevision: 123,
				CompleteBackupSHA256: prepared.BundleID, TotalBytes: prepared.Coverage.TotalBytes,
				DestinationWorkspaceID: backupWorkspace, DestinationName: "Compiler Team", AudienceMemberCount: 2,
				AudienceVersion: backupAudienceVersion, ServerID: "server-v3", MaxBackupBytes: 1 << 30,
				MaxBackupChunkBytes: 128, BackupWorkspaceBytes: 50 << 30,
			},
		}},
	})
	if err != nil || result.State != "succeeded" || len(result.Results) != 1 || result.Results[0].State != "accepted" ||
		result.Results[0].Route == nil || result.Results[0].Route.Path != "/v3/session-backups/"+backupRevision {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if !createSeen || !finalizeSeen || !rateLimited || chunkRequests != len(plan)-1 {
		t.Fatalf("create=%v rateLimited=%v chunks=%d/%d finalize=%v", createSeen, rateLimited, chunkRequests, len(plan)-1, finalizeSeen)
	}
}

func TestBackupChunkRateLimitRetryIsBounded(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"code":"rate_limited"}`)
	}))
	t.Cleanup(server.Close)
	base, _ := url.Parse(server.URL)
	client := Client{BaseURL: base}
	_, problem, err := client.sendBackupChunk(context.Background(), "credential", backupWorkspace,
		backupAudienceVersion, "20000000-0000-4000-8000-000000000001", backupChunkSpec{}, nil)
	if err == nil || problem.Code != "rate_limited" || requests != 5 {
		t.Fatalf("requests=%d problem=%#v err=%v", requests, problem, err)
	}
}

func TestPrepareBackupAcceptsOpaqueAudienceVersion(t *testing.T) {
	capabilityRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/share-destination":
			_, _ = io.WriteString(w, `{"contractVersion":"hub-share/v1","state":"ready","destination":{"workspaceId":"`+backupWorkspace+`","workspaceName":"Compiler Team","currentMemberCount":2,"resultingMemberCount":2,"currentApprovedSessionCount":0,"historyDisclosure":"Current members","credentialState":"paired","audienceVersion":"audience-v1"},"configured":true}`)
		case "/.well-known/coslash-server":
			capabilityRequests++
			_, _ = io.WriteString(w, `{"product":"coslash-server","serverId":"server-v3","displayName":"Hub","protocolVersions":["v3"],"snapshotVersions":[],"maxSnapshotBytes":0,"fullSessionVersions":[],"maxFullSessionBytes":0,"maxRequestBytes":1048576,"backupVersions":["session-backup/v1"],"backupUploadVersions":["backup-upload/v1"],"maxBackupBytes":1073741824,"maxBackupChunkBytes":128,"backupWorkspaceBytes":53687091200,"backupUploadExpiresSeconds":86400,"pairingUrl":"","teamUrl":""}`)
		default:
			t.Fatalf("unexpected request %s", r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)
	base, _ := url.Parse(server.URL)
	manager := sessionbackupproducer.New(sessionbackupproducer.Options{
		Root: t.TempDir(), OpenSource: func(context.Context, sessionbackupproducer.Selection) (sessionbackupproducer.SourceHandle, error) {
			return sessionbackupproducer.SourceHandle{}, errors.New("source unavailable")
		},
	})
	client := Client{BaseURL: base, Credentials: &memoryCredentials{}, Backup: manager}
	preview, err := client.PrepareBackup(context.Background(), sessionbackupproducer.Selection{
		SourceKind: "local", SourceID: "local", Agent: "codex", SessionID: "session",
	})
	if err == nil || preview.State != "blocked" || preview.Problem == nil || preview.Problem.Code != "artifact_unavailable" || capabilityRequests != 1 {
		t.Fatalf("preview=%#v capabilityRequests=%d err=%v", preview, capabilityRequests, err)
	}
}

func TestCompletedBackupRetryDoesNotRequireDiscardedSpool(t *testing.T) {
	manager, prepared := openBackupFixture(t)
	selection, sourceRevision, totalBytes := prepared.Selection, prepared.Manifest.Source.SourceRevision, prepared.Coverage.TotalBytes
	if err := manager.Discard(prepared.BundleID); err != nil {
		t.Fatal(err)
	}
	statusRequests, uploadRequests := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/share-destination":
			_, _ = io.WriteString(w, `{"contractVersion":"hub-share/v1","state":"ready","destination":{"workspaceId":"`+backupWorkspace+`","workspaceName":"Compiler Team","currentMemberCount":2,"resultingMemberCount":2,"currentApprovedSessionCount":0,"historyDisclosure":"Current members","credentialState":"paired","audienceVersion":"`+backupAudienceVersion+`"},"configured":true}`)
		case r.URL.Path == "/.well-known/coslash-server":
			_, _ = io.WriteString(w, `{"product":"coslash-server","serverId":"server-v3","displayName":"Hub","protocolVersions":["v3"],"snapshotVersions":[],"maxSnapshotBytes":0,"fullSessionVersions":[],"maxFullSessionBytes":0,"maxRequestBytes":1048576,"backupVersions":["session-backup/v1"],"backupUploadVersions":["backup-upload/v1"],"maxBackupBytes":1073741824,"maxBackupChunkBytes":128,"backupWorkspaceBytes":53687091200,"backupUploadExpiresSeconds":86400,"pairingUrl":"","teamUrl":""}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v3/backup-uploads/status":
			statusRequests++
			_ = json.NewEncoder(w).Encode(backupUploadStatus{
				UploadID: "20000000-0000-4000-8000-000000000001", State: "completed",
				CompleteBackupSHA256: prepared.BundleID, TotalBytes: totalBytes,
				Result: &backupUploadResult{
					RevisionID: backupRevision, CompleteBackupSHA256: prepared.BundleID,
					RepositoryID: "40000000-0000-4000-8000-000000000001", SharedAt: time.Date(2026, 9, 22, 20, 0, 0, 0, time.UTC),
					RevisionURL: "/v3/session-backups/" + backupRevision,
				},
			})
		case strings.HasPrefix(r.URL.Path, "/v3/backup-uploads"):
			uploadRequests++
			http.Error(w, "unexpected upload", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	base, _ := url.Parse(server.URL)
	client := Client{
		BaseURL: base, Credentials: &memoryCredentials{}, Backup: manager,
		LoadSourceSession: func(string, string, string, int64) (*session.Session, error) {
			return nil, errors.New("completed retry must not reload the source")
		},
	}
	result, err := client.ShareBackups(context.Background(), BackupShareRequest{
		ContractVersion: BackupShareVersion, RequestID: "request-2", Items: []BackupShareItemRequest{{
			LocalSessionID: selection.SourceID + ":codex:" + selection.SessionID, Selection: selection,
			IdempotencyKey: "backup-idempotency-key-0001", Consent: BackupConsent{
				PreviewContractVersion: BackupPreviewVersion, BundleID: prepared.BundleID,
				SourceRevision: sourceRevision, SelectedRevision: 123,
				CompleteBackupSHA256: prepared.BundleID, TotalBytes: totalBytes,
				DestinationWorkspaceID: backupWorkspace, DestinationName: "Compiler Team", AudienceMemberCount: 2,
				AudienceVersion: backupAudienceVersion, ServerID: "server-v3", MaxBackupBytes: 1 << 30,
				MaxBackupChunkBytes: 128, BackupWorkspaceBytes: 50 << 30,
			},
		}},
	})
	if err != nil || result.State != "succeeded" || result.Results[0].State != "already_accepted" ||
		!result.Results[0].Deduplicated || statusRequests != 1 || uploadRequests != 0 {
		t.Fatalf("result=%#v status=%d uploads=%d err=%v", result, statusRequests, uploadRequests, err)
	}
}

func TestBackupShareRejectsUnsupportedCachedSelectionBeforeLookup(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests++
	}))
	t.Cleanup(server.Close)
	base, _ := url.Parse(server.URL)
	item := BackupShareItemRequest{
		LocalSessionID: "local:claude:session", IdempotencyKey: "backup-idempotency-key-0001",
		Selection: sessionbackupproducer.Selection{SourceKind: "local", SourceID: "local", Agent: "claude", SessionID: "session"},
		Consent: BackupConsent{
			PreviewContractVersion: BackupPreviewVersion, BundleID: strings.Repeat("a", 64),
			SourceRevision: "source-revision", SelectedRevision: 123,
			CompleteBackupSHA256: strings.Repeat("a", 64), TotalBytes: 1,
			DestinationWorkspaceID: backupWorkspace, DestinationName: "Compiler Team", AudienceMemberCount: 2,
			AudienceVersion: backupAudienceVersion, ServerID: "server-v3", MaxBackupBytes: 1 << 30,
			MaxBackupChunkBytes: 128, BackupWorkspaceBytes: 50 << 30,
		},
	}
	result, err := (&Client{BaseURL: base}).shareBackupItem(context.Background(), "credential", item)
	if err != nil || result.State != "failed" || result.Error == nil ||
		result.Error.Code != "complete_backup_unsupported" || requests != 0 {
		t.Fatalf("result=%#v requests=%d err=%v", result, requests, err)
	}
}

func TestRetainedBackupUsesIdempotentCreateWithoutStatusProbe(t *testing.T) {
	manager, prepared := openBackupFixture(t)
	plan, err := (&Client{Backup: manager}).backupChunkPlan(prepared, 128)
	if err != nil {
		t.Fatal(err)
	}
	statusRequests, createRequests := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/share-destination":
			_, _ = io.WriteString(w, `{"contractVersion":"hub-share/v1","state":"ready","destination":{"workspaceId":"`+backupWorkspace+`","workspaceName":"Compiler Team","currentMemberCount":2,"resultingMemberCount":2,"currentApprovedSessionCount":0,"historyDisclosure":"Current members","credentialState":"paired","audienceVersion":"`+backupAudienceVersion+`"},"configured":true}`)
		case r.URL.Path == "/.well-known/coslash-server":
			_, _ = io.WriteString(w, `{"product":"coslash-server","serverId":"server-v3","displayName":"Hub","protocolVersions":["v3"],"snapshotVersions":[],"maxSnapshotBytes":0,"fullSessionVersions":[],"maxFullSessionBytes":0,"maxRequestBytes":1048576,"backupVersions":["session-backup/v1"],"backupUploadVersions":["backup-upload/v1"],"maxBackupBytes":1073741824,"maxBackupChunkBytes":128,"backupWorkspaceBytes":53687091200,"backupUploadExpiresSeconds":86400,"pairingUrl":"","teamUrl":""}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v3/backup-uploads/status":
			statusRequests++
			http.Error(w, "status unavailable", http.StatusServiceUnavailable)
		case r.Method == http.MethodPost && r.URL.Path == "/v3/backup-uploads":
			createRequests++
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(backupUploadStatus{
				UploadID: "20000000-0000-4000-8000-000000000001", State: "completed",
				CompleteBackupSHA256: prepared.BundleID, TotalBytes: prepared.Coverage.TotalBytes,
				ExpectedChunks: len(plan), Result: &backupUploadResult{
					RevisionID: backupRevision, CompleteBackupSHA256: prepared.BundleID,
					RepositoryID: "40000000-0000-4000-8000-000000000001",
					SharedAt:     time.Date(2026, 9, 22, 20, 0, 0, 0, time.UTC),
					RevisionURL:  "/v3/session-backups/" + backupRevision,
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	base, _ := url.Parse(server.URL)
	client := Client{
		BaseURL: base, Credentials: &memoryCredentials{}, Backup: manager,
		LoadSourceSession: func(string, string, string, int64) (*session.Session, error) {
			return &session.Session{Agent: prepared.Selection.Agent, LastActivityTime: 123}, nil
		},
	}
	result, err := client.ShareBackups(context.Background(), BackupShareRequest{
		ContractVersion: BackupShareVersion, RequestID: "request-3", Items: []BackupShareItemRequest{{
			LocalSessionID: prepared.Selection.SourceID + ":codex:" + prepared.Selection.SessionID, Selection: prepared.Selection,
			IdempotencyKey: "backup-idempotency-key-0002", Consent: BackupConsent{
				PreviewContractVersion: BackupPreviewVersion, BundleID: prepared.BundleID,
				SourceRevision: prepared.Manifest.Source.SourceRevision, SelectedRevision: 123,
				CompleteBackupSHA256: prepared.BundleID, TotalBytes: prepared.Coverage.TotalBytes,
				DestinationWorkspaceID: backupWorkspace, DestinationName: "Compiler Team", AudienceMemberCount: 2,
				AudienceVersion: backupAudienceVersion, ServerID: "server-v3", MaxBackupBytes: 1 << 30,
				MaxBackupChunkBytes: 128, BackupWorkspaceBytes: 50 << 30,
			},
		}},
	})
	if err != nil || result.State != "succeeded" || statusRequests != 0 || createRequests != 1 {
		t.Fatalf("result=%#v status=%d creates=%d err=%v", result, statusRequests, createRequests, err)
	}
}

func TestCompletedBackupResultRequiresCanonicalRevisionRoute(t *testing.T) {
	consent := BackupConsent{CompleteBackupSHA256: strings.Repeat("a", 64)}
	valid := backupUploadResult{
		RevisionID: backupRevision, CompleteBackupSHA256: consent.CompleteBackupSHA256,
		RepositoryID: "40000000-0000-4000-8000-000000000001", SharedAt: time.Now(),
		RevisionURL: "/v3/session-backups/" + backupRevision,
	}
	if !validCompletedBackupResult(&valid, consent) {
		t.Fatal("canonical completed result was rejected")
	}
	valid.RevisionURL = "https://evil.example/v3/session-backups/" + backupRevision
	if validCompletedBackupResult(&valid, consent) {
		t.Fatal("absolute result URL was accepted")
	}
}

func openBackupFixture(t *testing.T) (*sessionbackupproducer.Manager, *sessionbackupproducer.Prepared) {
	t.Helper()
	fixture := filepath.Join("..", "..", "sessionbackup", "v1", "testdata", "fixtures", "valid", "family")
	manifestBytes, err := os.ReadFile(filepath.Join(fixture, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		CompleteBackupSHA256 string `json:"completeBackupSha256"`
	}
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.CopyFS(filepath.Join(root, manifest.CompleteBackupSHA256), os.DirFS(fixture)); err != nil {
		t.Fatal(err)
	}
	manager := sessionbackupproducer.New(sessionbackupproducer.Options{Root: root})
	prepared, err := manager.Open(manifest.CompleteBackupSHA256)
	if err != nil {
		t.Fatal(err)
	}
	return manager, prepared
}
