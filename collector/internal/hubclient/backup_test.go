package hubclient

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/sessionbackupproducer"
	sessionbackupv1 "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
)

func TestBackupChunkPlanPreservesReviewedExactBytes(t *testing.T) {
	manager, prepared := openBackupFixture(t)
	client := Client{Backup: manager}
	plan, err := client.backupChunkPlan(prepared, minBackupChunkBytes)
	if err != nil || len(plan) == 0 {
		t.Fatalf("plan=%#v err=%v", plan, err)
	}
	var total int64
	for _, chunk := range plan {
		body, err := client.readBackupChunk(prepared, chunk)
		sum := sha256.Sum256(body)
		if err != nil || int64(len(body)) != chunk.ByteCount || chunk.ByteCount > minBackupChunkBytes ||
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

func TestBackupChunkPlanRejectsUnsafeHubBoundsBeforeReading(t *testing.T) {
	manager, prepared := openBackupFixture(t)
	client := Client{Backup: manager}
	for _, chunkBytes := range []int64{1, maxBackupChunkBytes + 1} {
		if _, err := client.backupChunkPlan(prepared, chunkBytes); err == nil {
			t.Fatalf("chunkBytes=%d was accepted", chunkBytes)
		}
	}
	copy := *prepared
	copy.Manifest = prepared.Manifest
	copy.Manifest.Artifacts = append([]sessionbackupv1.Artifact(nil), prepared.Manifest.Artifacts...)
	copy.Manifest.Artifacts[0].ByteLength = sessionbackupv1.MaxArtifactBytes + 1
	if _, err := client.backupChunkPlan(&copy, minBackupChunkBytes); err == nil {
		t.Fatal("oversized artifact reached chunk allocation")
	}
	copy.Manifest.Artifacts = make([]sessionbackupv1.Artifact, maxBackupChunks+1)
	if _, err := client.backupChunkPlan(&copy, minBackupChunkBytes); err == nil {
		t.Fatal("unbounded chunk plan was accepted")
	}
}

func TestBackupCreateRequestEnforcesExactAdvertisedSize(t *testing.T) {
	manager, prepared := openBackupFixture(t)
	client := Client{Backup: manager}
	plan, err := client.backupChunkPlan(prepared, minBackupChunkBytes)
	if err != nil {
		t.Fatal(err)
	}
	body, err := client.backupCreateRequest(prepared, prepared.BundleID, plan)
	if err != nil {
		t.Fatal(err)
	}
	item := BackupShareItemRequest{Consent: BackupConsent{
		CompleteBackupSHA256: prepared.BundleID,
		MaxRequestBytes:      int64(len(body) - 1),
	}}
	_, problem, err := client.createOrResumeBackup(context.Background(), "credential", item, prepared, plan)
	if err == nil || problem.Code != "backup_capacity_exceeded" {
		t.Fatalf("problem=%#v err=%v", problem, err)
	}
}

func TestValidBackupUploadResultRequiresCompleteRouteIdentity(t *testing.T) {
	hash := strings.Repeat("a", 64)
	valid := &backupUploadResult{
		RevisionID: "revision", CompleteBackupSHA256: hash, RepositoryID: "repository",
		SharedAt: time.Unix(1, 0), RevisionURL: "/revisions/revision",
	}
	if !validBackupUploadResult(valid, hash) {
		t.Fatal("complete upload result was rejected")
	}
	for _, mutate := range []func(*backupUploadResult){
		func(result *backupUploadResult) { result.RevisionID = "" },
		func(result *backupUploadResult) { result.RepositoryID = "" },
		func(result *backupUploadResult) { result.SharedAt = time.Time{} },
		func(result *backupUploadResult) { result.RevisionURL = "" },
	} {
		candidate := *valid
		mutate(&candidate)
		if validBackupUploadResult(&candidate, hash) {
			t.Fatalf("incomplete result was accepted: %#v", candidate)
		}
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
