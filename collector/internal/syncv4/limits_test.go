package syncv4

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/sessionbackupproducer"
	sessionbackupv1 "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
)

// artifactLimitBundle is the accepted Codex fixture family in a spool, with
// exact change bodies added to its root until it has the given artifact count.
func artifactLimitBundle(t *testing.T, artifacts int) (*sessionbackupproducer.Manager, *sessionbackupproducer.Prepared) {
	t.Helper()
	fixture := filepath.Join("..", "..", "sessionbackup", "v1", "testdata", "fixtures", "valid", "family")
	data, err := os.ReadFile(filepath.Join(fixture, sessionbackupv1.ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := sessionbackupv1.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	blobs := map[string][]byte{}
	for _, artifact := range manifest.Artifacts {
		if blobs[artifact.LogicalName], err = os.ReadFile(filepath.Join(fixture, filepath.FromSlash(artifact.LogicalName))); err != nil {
			t.Fatal(err)
		}
	}
	for index := 0; len(manifest.Artifacts) < artifacts; index++ {
		name := fmt.Sprintf("members/root/processed/bulk-change-%06d.txt", index)
		manifest.Artifacts = append(manifest.Artifacts, sessionbackupv1.Artifact{LogicalName: name, MemberID: manifest.Family.RootMemberID,
			Source: "coslash", Kind: sessionbackupv1.KindExactChangeBody, SourceKey: fmt.Sprintf("bulk-change-%06d", index),
			MediaType: "text/plain; charset=utf-8", Encoding: "identity"})
		blobs[name] = []byte(fmt.Sprintf("change %06d\n", index))
	}
	evidence := make(map[string]sessionbackupv1.ArtifactEvidence, len(blobs))
	for name, blob := range blobs {
		sum := sha256.Sum256(blob)
		evidence[name] = sessionbackupv1.ArtifactEvidence{ByteLength: int64(len(blob)), SHA256: hex.EncodeToString(sum[:])}
	}
	frozen, err := sessionbackupv1.FreezeEvidence(manifest, evidence)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := sessionbackupv1.Marshal(frozen)
	if err != nil {
		t.Fatal(err)
	}
	spool := t.TempDir()
	bundle := filepath.Join(spool, frozen.CompleteBackupSHA256)
	write := func(name string, body []byte) {
		path := filepath.Join(bundle, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for name, blob := range blobs {
		write(name, blob)
	}
	write(sessionbackupv1.ManifestFileName, encoded)
	manager := sessionbackupproducer.New(sessionbackupproducer.Options{Root: spool})
	prepared, err := manager.Open(frozen.CompleteBackupSHA256)
	if err != nil {
		t.Fatal(err)
	}
	return manager, prepared
}

// V4's artifact limit applies to curated records, regardless of the number of
// source artifacts retained in the local prepared bundle.
func TestV4ManifestFiltersLargePreparedArtifactFamilies(t *testing.T) {
	if v4MaxArtifacts != 4096 || v4MaxArtifactChunks != 256 || v4MaxManifestChunks != 8192 {
		t.Fatalf("limits = %d artifacts, %d chunks each, %d chunks", v4MaxArtifacts, v4MaxArtifactChunks, v4MaxManifestChunks)
	}
	manager, prepared := artifactLimitBundle(t, v4MaxArtifacts)
	runner := &Runner{Backup: manager}
	started := time.Now()
	wire, err := runner.manifest(prepared)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("built the wire manifest for %d artifacts in %s", len(wire.Artifacts), time.Since(started))
	curated := curatedArtifacts(prepared)
	if len(prepared.Manifest.Artifacts) != v4MaxArtifacts || len(wire.Artifacts) != len(curated) || len(wire.ContentSHA256) != 64 {
		t.Fatalf("wire manifest has %d artifacts, content %q", len(wire.Artifacts), wire.ContentSHA256)
	}
	for ordinal, artifact := range wire.Artifacts {
		declared := curated[ordinal]
		if artifact.Ordinal != ordinal || artifact.Kind != sessionbackupv1.KindParsedSessionRecord || artifact.Bytes != declared.ByteLength ||
			artifact.SHA256 != declared.SHA256 || len(artifact.Chunks) != 1 || artifact.Chunks[0].SHA256 != declared.SHA256 {
			t.Fatalf("artifact %d = %+v, curated record %+v", ordinal, artifact, declared)
		}
	}

	// The bundle reader decodes the manifest once and still reads only
	// declared artifacts of a bundle that exists.
	reader, err := manager.Reader(prepared.BundleID)
	if err != nil {
		t.Fatal(err)
	}
	last := prepared.Manifest.Artifacts[len(prepared.Manifest.Artifacts)-1]
	body := make([]byte, last.ByteLength)
	if n, err := reader.Read(last.LogicalName, 0, body); err != nil || int64(n) != last.ByteLength {
		t.Fatalf("read %s: %d bytes, %v", last.LogicalName, n, err)
	}
	for _, read := range []struct {
		name   string
		offset int64
	}{{sessionbackupv1.ManifestFileName, 0}, {"members/root/processed/undeclared.txt", 0}, {last.LogicalName, -1}} {
		if _, err := reader.Read(read.name, read.offset, body); !errors.Is(err, sessionbackupproducer.ErrNotPrepared) {
			t.Fatalf("read %s at %d: %v", read.name, read.offset, err)
		}
	}
	if err := manager.Discard(prepared.BundleID); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Read(last.LogicalName, 0, body); !errors.Is(err, sessionbackupproducer.ErrNotPrepared) {
		t.Fatalf("read after discard: %v", err)
	}

	manager, prepared = artifactLimitBundle(t, v4MaxArtifacts+1)
	runner = &Runner{Backup: manager}
	wire, err = runner.manifest(prepared)
	if err != nil || len(wire.Artifacts) != len(curatedArtifacts(prepared)) {
		t.Fatalf("4,097 prepared source artifacts produced %d V4 records: %v", len(wire.Artifacts), err)
	}

	tooManyRecords := &sessionbackupproducer.Prepared{Manifest: sessionbackupv1.Manifest{
		Artifacts: make([]sessionbackupv1.Artifact, v4MaxArtifacts+1),
	}}
	for index := range tooManyRecords.Manifest.Artifacts {
		tooManyRecords.Manifest.Artifacts[index].Kind = sessionbackupv1.KindParsedSessionRecord
	}
	if _, err := (&Runner{}).manifest(tooManyRecords); err == nil || err.Error() != "v4 artifact count unsupported" {
		t.Fatalf("4,097 curated records: %v", err)
	}
}

// limitHub is a Hub that records how a large upload is sent.
type limitHub struct {
	t                             *testing.T
	manifest                      hubclient.V4Manifest
	received                      map[[2]int]bool
	puts, confirms, finalizations int
}

func (h *limitHub) V4Binding(context.Context) (string, error) { return "", errors.New("unused") }
func (h *limitHub) V4CheckIn(context.Context, hubclient.V4Queue, int64, []hubclient.V4CommandResult, []string, []hubclient.V4LogEntry) (hubclient.V4CheckIn, error) {
	return hubclient.V4CheckIn{}, errors.New("unused")
}
func (h *limitHub) V4Create(context.Context, hubclient.V4Create) (hubclient.V4Status, error) {
	return hubclient.V4Status{}, errors.New("unused")
}
func (h *limitHub) V4Status(context.Context, string) (hubclient.V4Status, error) {
	status := hubclient.V4Status{UploadID: "upload-limit", SessionID: "ses_limit", State: "open"}
	for _, artifact := range h.manifest.Artifacts {
		for _, chunk := range artifact.Chunks {
			if !h.received[[2]int{artifact.Ordinal, chunk.Ordinal}] {
				status.Missing = append(status.Missing, hubclient.V4Missing{ArtifactOrdinal: artifact.Ordinal, ChunkOrdinal: chunk.Ordinal,
					Offset: chunk.Offset, Bytes: chunk.Bytes, SHA256: chunk.SHA256})
			}
		}
	}
	return status, nil
}
func (h *limitHub) V4PutChunk(_ context.Context, _ string, missing hubclient.V4Missing, body io.Reader) error {
	data, err := io.ReadAll(body)
	sum := sha256.Sum256(data)
	if err != nil || int64(len(data)) != missing.Bytes || hex.EncodeToString(sum[:]) != missing.SHA256 {
		h.t.Errorf("chunk %d/%d changed", missing.ArtifactOrdinal, missing.ChunkOrdinal)
	}
	h.puts++
	return nil
}
func (h *limitHub) V4Confirm(_ context.Context, _ string, missing ...hubclient.V4Missing) (hubclient.V4Status, error) {
	var bytes int64
	for _, chunk := range missing {
		h.received[[2]int{chunk.ArtifactOrdinal, chunk.ChunkOrdinal}] = true
		bytes += chunk.Bytes
	}
	if len(missing) < 1 || len(missing) > hubclient.V4MaxConfirm || (len(missing) > 1 && bytes > chunkBytes) {
		h.t.Errorf("confirm of %d chunks and %d bytes", len(missing), bytes)
	}
	h.confirms++
	return hubclient.V4Status{UploadID: "upload-limit", SessionID: "ses_limit", State: "open"}, nil
}
func (h *limitHub) V4Finalize(context.Context, string) (hubclient.V4Status, error) {
	h.finalizations++
	if status, _ := h.V4Status(context.Background(), ""); len(status.Missing) != 0 {
		h.t.Errorf("finalized with %d chunks missing", len(status.Missing))
	}
	return hubclient.V4Status{UploadID: "upload-limit", SessionID: "ses_limit", State: "finalizing"}, nil
}

// Chunk confirmation remains capped at 50 coordinates per request. Curated
// records no longer create one upload artifact per exact change body.
func TestV4ConfirmChunksInBatches(t *testing.T) {
	hub := &limitHub{t: t, received: map[[2]int]bool{}}
	runner := &Runner{Hub: hub}
	sent := make([]hubclient.V4Missing, 82)
	for index := range sent {
		sent[index] = hubclient.V4Missing{ArtifactOrdinal: 0, ChunkOrdinal: index, Bytes: 1, SHA256: "fixture"}
	}
	if err := runner.confirmChunks(context.Background(), "upload-limit", sent); err != nil {
		t.Fatal(err)
	}
	want := (len(sent) + hubclient.V4MaxConfirm - 1) / hubclient.V4MaxConfirm
	if hub.confirms != want || len(hub.received) != len(sent) {
		t.Fatalf("confirmed=%d confirms=%d (want %d)", len(hub.received), hub.confirms, want)
	}
}
