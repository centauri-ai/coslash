package syncv4

import (
	"context"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/sessionbackupproducer"
	sessionbackupv1 "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
)

func TestCuratedArtifactsSelectsOnlyParsedRecordsForSupportedAgents(t *testing.T) {
	for _, agent := range []string{"codex", "claude", "cursor", "opencode"} {
		t.Run(agent, func(t *testing.T) {
			prepared := &sessionbackupproducer.Prepared{Manifest: sessionbackupv1.Manifest{
				Source:  sessionbackupv1.SourceIdentity{Agent: agent},
				Members: []sessionbackupv1.Member{{MemberID: "root"}, {MemberID: "child"}},
				Artifacts: []sessionbackupv1.Artifact{
					{Kind: sessionbackupv1.KindRawTranscript, LogicalName: "root/raw/transcript"},
					{Kind: sessionbackupv1.KindParsedSessionRecord, MemberID: "root", LogicalName: "root/record"},
					{Kind: sessionbackupv1.KindRawSidecar, LogicalName: "root/raw/sidecar"},
					{Kind: sessionbackupv1.KindRawMetadataRows, LogicalName: "root/raw/rows"},
					{Kind: sessionbackupv1.KindExactChangeBody, LogicalName: "root/change"},
					{Kind: sessionbackupv1.KindSessionEnrichment, LogicalName: "root/enrichment"},
					{Kind: sessionbackupv1.KindSynthesis, LogicalName: "root/synthesis"},
					{Kind: sessionbackupv1.KindParsedSessionRecord, MemberID: "child", LogicalName: "child/record"},
				},
			}}

			got := curatedArtifacts(prepared)
			if len(got) != 2 || got[0].LogicalName != "root/record" || got[1].LogicalName != "child/record" {
				t.Fatalf("curated artifacts = %#v", got)
			}
			for _, artifact := range got {
				if artifact.Kind != sessionbackupv1.KindParsedSessionRecord {
					t.Fatalf("non-record artifact selected: %#v", artifact)
				}
			}
		})
	}
}

func TestPrepareEntryRebuildsLegacyRawManifest(t *testing.T) {
	manager, prepared, _ := fixtureBundle(t)
	queue, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	entry := Entry{Key: "fixture", Selection: prepared.Selection, BundleID: prepared.BundleID,
		Session: hubclient.V4Session{Agent: prepared.Selection.Agent}, Activity: now.UnixMilli(), Recent: true}
	legacy := hubclient.V4Manifest{ContentSHA256: "legacy"}
	for ordinal, artifact := range prepared.Manifest.Artifacts {
		legacy.Artifacts = append(legacy.Artifacts, hubclient.V4Artifact{
			Ordinal: ordinal, Kind: artifact.Kind, Bytes: artifact.ByteLength, SHA256: artifact.SHA256,
		})
	}
	entry.Manifest = &legacy
	if err := queue.Merge([]Entry{entry}, now); err != nil {
		t.Fatal(err)
	}
	runner := &Runner{Queue: queue, Backup: manager}
	if err := runner.prepareEntry(context.Background(), &entry); err != nil {
		t.Fatal(err)
	}
	if !matchesCuratedManifest(entry.Manifest, curatedArtifacts(prepared)) {
		t.Fatalf("rebuilt manifest = %#v", entry.Manifest)
	}
}

func TestTransferDropsLegacyRawManifestBeforeStatusOrChunkRequests(t *testing.T) {
	manager, prepared, _ := fixtureBundle(t)
	queue, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	entry := Entry{Key: "fixture", Selection: prepared.Selection, BundleID: prepared.BundleID,
		Session: hubclient.V4Session{Agent: prepared.Selection.Agent}, Activity: now.UnixMilli(), Recent: true,
		UploadID: "legacy-upload", SessionID: "legacy-session"}
	legacy := hubclient.V4Manifest{ContentSHA256: "legacy"}
	for ordinal, artifact := range prepared.Manifest.Artifacts {
		legacy.Artifacts = append(legacy.Artifacts, hubclient.V4Artifact{
			Ordinal: ordinal, Kind: artifact.Kind, Bytes: artifact.ByteLength, SHA256: artifact.SHA256,
		})
	}
	entry.Manifest = &legacy
	if err := queue.Merge([]Entry{entry}, now); err != nil {
		t.Fatal(err)
	}
	runner := &Runner{Queue: queue, Backup: manager, Now: func() time.Time { return now }, checkedAt: now}
	stored := queue.Entries()[0]
	if err := runner.transfer(context.Background(), &stored); err != nil {
		t.Fatal(err)
	}
	stored = queue.Entries()[0]
	if stored.UploadID != "" || stored.SessionID != "" || stored.Manifest != nil || stored.Attempt != 1 {
		t.Fatalf("legacy upload state not reset: %+v", stored)
	}
}
