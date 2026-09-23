package hubclient

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/sessionbackupproducer"
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
