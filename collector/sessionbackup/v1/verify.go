package sessionbackupv1

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
)

type OpenArtifact func(logicalName string) (io.ReadCloser, error)

func Decode(data []byte) (Manifest, error) {
	if int64(len(data)) > MaxManifestBytes {
		return Manifest{}, fmt.Errorf("%w: manifest exceeds byte limit", ErrInvalid)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("%w: manifest decode: %v", ErrInvalid, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Manifest{}, fmt.Errorf("%w: trailing manifest value", ErrInvalid)
	}
	if err := Validate(manifest); err != nil {
		return Manifest{}, err
	}
	canonical, err := json.Marshal(manifest)
	if err != nil || !bytes.Equal(canonical, data) {
		return Manifest{}, fmt.Errorf("%w: non-canonical manifest", ErrInvalid)
	}
	return manifest, nil
}

// Verify checks the canonical manifest, every exact artifact byte, parsed
// record provenance and lineage, exact change bodies, enrichment, and synthesis.
func Verify(manifestBytes []byte, open OpenArtifact) (Manifest, error) {
	manifest, err := Decode(manifestBytes)
	if err != nil {
		return Manifest{}, err
	}
	members := make(map[string]Member, len(manifest.Members))
	for _, member := range manifest.Members {
		members[member.MemberID] = member
	}
	records := make(map[string]fullsessionv1.Record, len(manifest.Members))
	changes := make(map[string]map[string]fullsessionv1.FileChange, len(manifest.Members))
	for _, artifact := range manifest.Artifacts {
		if artifact.Kind != KindParsedSessionRecord {
			continue
		}
		blob, err := readAndVerifyArtifact(artifact, open, true)
		if err != nil {
			return Manifest{}, err
		}
		record, err := fullsessionv1.Decode(blob)
		if err != nil {
			return Manifest{}, fmt.Errorf("%w: parsed record is invalid", ErrInvalid)
		}
		if record.ParentSessionID != members[artifact.MemberID].ParentMemberID {
			return Manifest{}, fmt.Errorf("%w: parsed record family linkage mismatch", ErrInvalid)
		}
		if !parsedRecordIdentityMatches(manifest, members[artifact.MemberID], artifact, record) {
			return Manifest{}, fmt.Errorf("%w: parsed record identity mismatch", ErrInvalid)
		}
		records[artifact.MemberID] = record
		changes[artifact.MemberID] = make(map[string]fullsessionv1.FileChange)
		for _, edit := range record.Session.FileEdits {
			for _, change := range edit.Changes {
				changes[artifact.MemberID][change.ID] = change
			}
		}
	}
	syntheses := make(map[string]SynthesisRecord, len(manifest.Members))
	foundChanges := make(map[string]map[string]bool, len(manifest.Members))
	for _, artifact := range manifest.Artifacts {
		if artifact.Kind == KindParsedSessionRecord {
			continue
		}
		capture := artifact.Kind == KindRawMetadataRows || artifact.Kind == KindSessionEnrichment || artifact.Kind == KindSynthesis
		blob, err := readAndVerifyArtifact(artifact, open, capture)
		if err != nil {
			return Manifest{}, err
		}
		switch artifact.Kind {
		case KindRawMetadataRows:
			if _, err := DecodeDatabaseRows(blob, artifact.MemberID); err != nil {
				return Manifest{}, err
			}
		case KindSessionEnrichment:
			if _, err := DecodeEnrichment(blob); err != nil {
				return Manifest{}, err
			}
		case KindSynthesis:
			record, err := DecodeSynthesisRecord(blob)
			if err != nil || record.Agent != manifest.Source.Agent || record.SessionID != artifact.MemberID {
				return Manifest{}, fmt.Errorf("%w: synthesis provenance mismatch", ErrInvalid)
			}
			syntheses[artifact.MemberID] = record
		case KindExactChangeBody:
			change, ok := changes[artifact.MemberID][artifact.SourceKey]
			if !ok || int64(change.ByteCount) != artifact.ByteLength || change.SHA256 != artifact.SHA256 {
				return Manifest{}, fmt.Errorf("%w: exact change body mismatch", ErrInvalid)
			}
			if foundChanges[artifact.MemberID] == nil {
				foundChanges[artifact.MemberID] = map[string]bool{}
			}
			foundChanges[artifact.MemberID][artifact.SourceKey] = true
		}
	}
	for _, member := range manifest.Members {
		synthesis, hasSynthesis := syntheses[member.MemberID]
		if (member.SynthesisRevisionMs > 0) != hasSynthesis {
			return Manifest{}, fmt.Errorf("%w: synthesis revision binding mismatch", ErrIncomplete)
		}
		if hasSynthesis && synthesis.Revision != member.SynthesisRevisionMs {
			return Manifest{}, fmt.Errorf("%w: synthesis revision mismatch", ErrInvalid)
		}
		if record, hasRecord := records[member.MemberID]; hasRecord {
			if record.Session.Synthesis != nil && (!hasSynthesis || !reflect.DeepEqual(*record.Session.Synthesis, synthesis.Synthesis)) {
				return Manifest{}, fmt.Errorf("%w: parsed and persisted synthesis mismatch", ErrInvalid)
			}
		}
		for changeID := range changes[member.MemberID] {
			if !foundChanges[member.MemberID][changeID] {
				return Manifest{}, fmt.Errorf("%w: exact change body is missing", ErrIncomplete)
			}
		}
	}
	return manifest, nil
}

func readAndVerifyArtifact(artifact Artifact, open OpenArtifact, capture bool) ([]byte, error) {
	reader, err := open(artifact.LogicalName)
	if err != nil {
		return nil, fmt.Errorf("%w: open artifact %q", ErrIncomplete, artifact.LogicalName)
	}
	hash := sha256.New()
	limited := io.LimitReader(reader, artifact.ByteLength+1)
	var blob []byte
	var readBytes int64
	if capture {
		if artifact.ByteLength > fullsessionv1.MaxRecordBytes {
			_ = reader.Close()
			return nil, fmt.Errorf("%w: semantic artifact %q exceeds byte limit", ErrInvalid, artifact.LogicalName)
		}
		blob, err = io.ReadAll(io.TeeReader(limited, hash))
		readBytes = int64(len(blob))
	} else {
		readBytes, err = io.Copy(hash, limited)
	}
	closeErr := reader.Close()
	if err != nil || closeErr != nil {
		return nil, fmt.Errorf("%w: read artifact %q", ErrIncomplete, artifact.LogicalName)
	}
	if readBytes != artifact.ByteLength {
		return nil, fmt.Errorf("%w: artifact %q length mismatch", ErrIncomplete, artifact.LogicalName)
	}
	if hex.EncodeToString(hash.Sum(nil)) != artifact.SHA256 {
		return nil, fmt.Errorf("%w: artifact %q hash mismatch", ErrInvalid, artifact.LogicalName)
	}
	return blob, nil
}

func VerifyDirectory(root string) (Manifest, error) {
	manifestFile, err := openRegularArtifact(root, ManifestFileName)
	if err != nil {
		return Manifest{}, err
	}
	manifestBytes, readErr := io.ReadAll(io.LimitReader(manifestFile, MaxManifestBytes+1))
	closeErr := manifestFile.Close()
	if readErr != nil || closeErr != nil {
		return Manifest{}, fmt.Errorf("%w: read manifest", ErrInvalid)
	}
	if int64(len(manifestBytes)) > MaxManifestBytes {
		return Manifest{}, fmt.Errorf("%w: manifest exceeds byte limit", ErrInvalid)
	}
	manifest, err := Verify(manifestBytes, func(name string) (io.ReadCloser, error) {
		if !logicalName(name) {
			return nil, fmt.Errorf("unsafe artifact name")
		}
		return openRegularArtifact(root, name)
	})
	if err != nil {
		return Manifest{}, err
	}
	declared := map[string]bool{ManifestFileName: true}
	for _, artifact := range manifest.Artifacts {
		declared[filepath.FromSlash(artifact.LogicalName)] = true
	}
	err = filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, name)
		if err != nil || !declared[relative] {
			return fmt.Errorf("%w: undeclared bundle file", ErrInvalid)
		}
		return nil
	})
	return manifest, err
}

func openRegularArtifact(root, logicalName string) (io.ReadCloser, error) {
	current := root
	parts := strings.Split(filepath.FromSlash(logicalName), string(filepath.Separator))
	for index, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("artifact path contains a symbolic link")
		}
		if index < len(parts)-1 && !info.IsDir() {
			return nil, fmt.Errorf("artifact parent is not a directory")
		}
		if index == len(parts)-1 && !info.Mode().IsRegular() {
			return nil, fmt.Errorf("artifact is not a regular file")
		}
	}
	return os.Open(current)
}
