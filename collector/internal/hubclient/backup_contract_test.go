package hubclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/sessionbackupproducer"
)

func TestBackupDestinationTransportMatchesSharedContract(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "backup-transport-contract.json"))
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		DestinationAssertionHeader       string   `json:"destinationAssertionHeader"`
		DestinationAudienceVersionHeader string   `json:"destinationAudienceVersionHeader"`
		DestinationAudienceVersionField  string   `json:"destinationAudienceVersionField"`
		AudienceVersionFormat            string   `json:"audienceVersionFormat"`
		MutationMethods                  []string `json:"mutationMethods"`
	}
	if err := json.Unmarshal(body, &contract); err != nil {
		t.Fatal(err)
	}
	if contract.DestinationAssertionHeader != backupWorkspaceHeader ||
		contract.DestinationAudienceVersionHeader != backupAudienceHeader ||
		contract.DestinationAudienceVersionField != "audienceVersion" ||
		contract.AudienceVersionFormat != "opaque header-safe token (max 200 bytes)" ||
		!reflect.DeepEqual(contract.MutationMethods, []string{"create", "chunk", "finalize", "abort"}) {
		t.Fatalf("backup destination transport contract drifted: %#v", contract)
	}
}

func TestBackupAudienceVersionUsesSharedOpaqueTokenContract(t *testing.T) {
	for _, value := range []string{"audience-v1", "workspace-audience-2026-09", "audience-v1:" + strings.Repeat("a", 64)} {
		if !validAudienceVersion(value) {
			t.Errorf("valid audience version was rejected: %q", value)
		}
	}
	for _, value := range []string{"", " audience-v1", "audience-v1 ", "audience\x00v1", strings.Repeat("a", 201)} {
		if validAudienceVersion(value) {
			t.Errorf("invalid audience version was accepted: %q", value)
		}
	}
	item := BackupShareItemRequest{
		LocalSessionID: "local:codex:session",
		Selection:      sessionbackupproducer.Selection{SourceKind: "local", SourceID: "local", Agent: "codex", SessionID: "session"},
		IdempotencyKey: "backup-idempotency-key-0001",
		Consent: BackupConsent{
			PreviewContractVersion: BackupPreviewVersion, BundleID: strings.Repeat("a", 64),
			SourceRevision: "revision", SelectedRevision: 1, CompleteBackupSHA256: strings.Repeat("a", 64), TotalBytes: 1,
			DestinationWorkspaceID: "workspace", DestinationName: "Team", AudienceVersion: "audience-v1",
			ServerID: "hub", MaxBackupBytes: 1, MaxBackupChunkBytes: 1, BackupWorkspaceBytes: 1,
		},
	}
	if !validBackupShareItem(item) {
		t.Fatal("cached consent with an opaque audience version was rejected")
	}
	item.Consent.AudienceVersion = " audience-v1"
	if validBackupShareItem(item) {
		t.Fatal("cached consent with an unsafe audience version was accepted")
	}
}

func TestBackupMutationsCarryDestinationContractHeaders(t *testing.T) {
	manager, prepared := openBackupFixture(t)
	client := &Client{Backup: manager}
	plan, err := client.backupChunkPlan(prepared, 128)
	if err != nil || len(plan) == 0 {
		t.Fatalf("chunk plan=%#v err=%v", plan, err)
	}
	calls := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		statusCode := http.StatusOK
		if r.Header.Get(backupWorkspaceHeader) != backupWorkspace || r.Header.Get(backupAudienceHeader) != backupAudienceVersion {
			t.Errorf("%s headers = %#v", r.URL.Path, r.Header)
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v3/backup-uploads":
			calls["create"] = true
			statusCode = http.StatusCreated
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/chunks/"):
			calls["chunk"] = true
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/finalize"):
			calls["finalize"] = true
		default:
			t.Errorf("unexpected backup request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_ = json.NewEncoder(w).Encode(backupUploadStatus{
			UploadID: "20000000-0000-4000-8000-000000000001", CompleteBackupSHA256: prepared.BundleID,
		})
	}))
	defer server.Close()
	client.BaseURL, err = url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	item := BackupShareItemRequest{
		IdempotencyKey: "backup-idempotency-key-0001",
		Consent: BackupConsent{
			DestinationWorkspaceID: backupWorkspace,
			AudienceVersion:        backupAudienceVersion,
			CompleteBackupSHA256:   prepared.BundleID,
		},
	}
	ctx := context.Background()
	status, _, err := client.createOrResumeBackup(ctx, "credential", item, prepared, plan)
	if err != nil {
		t.Fatalf("create upload: %v", err)
	}
	body, err := client.readBackupChunk(prepared, plan[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := client.sendBackupChunk(ctx, "credential", backupWorkspace, backupAudienceVersion, status.UploadID, plan[0], body); err != nil {
		t.Fatalf("send chunk: %v", err)
	}
	if _, _, err := client.finalizeBackup(ctx, "credential", backupWorkspace, backupAudienceVersion, status.UploadID); err != nil {
		t.Fatalf("finalize upload: %v", err)
	}
	for _, method := range []string{"create", "chunk", "finalize"} {
		if !calls[method] {
			t.Errorf("%s mutation was not observed", method)
		}
	}
}
