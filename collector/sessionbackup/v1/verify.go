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
	records := map[string]fullsessionv1.Record{}
	syntheses := map[string]SynthesisRecord{}
	blobs := map[string][]byte{}
	kindCounts := map[string]map[string]int{}
	for _, artifact := range manifest.Artifacts {
		reader, err := open(artifact.LogicalName)
		if err != nil {
			return Manifest{}, fmt.Errorf("%w: open artifact %q", ErrIncomplete, artifact.LogicalName)
		}
		blob, readErr := io.ReadAll(io.LimitReader(reader, artifact.ByteLength+1))
		closeErr := reader.Close()
		if readErr != nil || closeErr != nil || int64(len(blob)) != artifact.ByteLength {
			return Manifest{}, fmt.Errorf("%w: artifact %q length mismatch", ErrIncomplete, artifact.LogicalName)
		}
		sum := sha256.Sum256(blob)
		if hex.EncodeToString(sum[:]) != artifact.SHA256 {
			return Manifest{}, fmt.Errorf("%w: artifact %q hash mismatch", ErrInvalid, artifact.LogicalName)
		}
		blobs[artifact.LogicalName] = blob
		if kindCounts[artifact.MemberID] == nil {
			kindCounts[artifact.MemberID] = map[string]int{}
		}
		kindCounts[artifact.MemberID][artifact.Kind]++
		if artifact.Kind == KindParsedSessionRecord {
			record, err := fullsessionv1.Decode(blob)
			if err != nil || record.SourceID != manifest.Source.SourceID || record.Agent != manifest.Source.Agent || record.SessionID != artifact.MemberID {
				return Manifest{}, fmt.Errorf("%w: parsed record provenance mismatch", ErrInvalid)
			}
			records[artifact.MemberID] = record
		}
		if artifact.Kind == KindRawMetadataRows {
			if _, err := DecodeDatabaseRows(blob, artifact.MemberID); err != nil {
				return Manifest{}, err
			}
		}
		if artifact.Kind == KindSessionEnrichment {
			if _, err := DecodeEnrichment(blob); err != nil {
				return Manifest{}, err
			}
		}
		if artifact.Kind == KindSynthesis {
			record, err := DecodeSynthesisRecord(blob)
			if err != nil || record.Agent != manifest.Source.Agent || record.SessionID != artifact.MemberID {
				return Manifest{}, fmt.Errorf("%w: synthesis provenance mismatch", ErrInvalid)
			}
			syntheses[artifact.MemberID] = record
		}
	}
	for _, member := range manifest.Members {
		counts := kindCounts[member.MemberID]
		if counts[KindRawTranscript] == 0 || counts[KindRawMetadataRows] != 0 ||
			counts[KindParsedSessionRecord] > 1 || counts[KindSessionEnrichment] != 1 || counts[KindSynthesis] > 1 {
			return Manifest{}, fmt.Errorf("%w: member artifact coverage is incomplete", ErrIncomplete)
		}
		synthesis, hasSynthesis := syntheses[member.MemberID]
		if (member.SynthesisRevisionMs > 0) != hasSynthesis {
			return Manifest{}, fmt.Errorf("%w: synthesis revision binding mismatch", ErrIncomplete)
		}
		if hasSynthesis && synthesis.Revision != member.SynthesisRevisionMs {
			return Manifest{}, fmt.Errorf("%w: synthesis revision mismatch", ErrInvalid)
		}
		if record, hasRecord := records[member.MemberID]; hasRecord {
			if record.ParentSessionID != member.ParentMemberID {
				return Manifest{}, fmt.Errorf("%w: parsed record family linkage mismatch", ErrInvalid)
			}
			if record.Session.Synthesis != nil && (!hasSynthesis || !reflect.DeepEqual(*record.Session.Synthesis, synthesis.Synthesis)) {
				return Manifest{}, fmt.Errorf("%w: parsed and persisted synthesis mismatch", ErrInvalid)
			}
		}
	}
	if _, ok := records[manifest.Family.RootMemberID]; !ok {
		return Manifest{}, fmt.Errorf("%w: root parsed record is missing", ErrIncomplete)
	}
	foundChanges := map[string]bool{}
	for _, artifact := range manifest.Artifacts {
		if artifact.Kind != KindExactChangeBody {
			continue
		}
		record, ok := records[artifact.MemberID]
		if !ok {
			return Manifest{}, fmt.Errorf("%w: exact change has no parsed member record", ErrInvalid)
		}
		matched := false
		for _, edit := range record.Session.FileEdits {
			for _, change := range edit.Changes {
				if change.ID == artifact.SourceKey && bytes.Equal(blobs[artifact.LogicalName], []byte(change.Text)) {
					matched = true
				}
			}
		}
		if !matched {
			return Manifest{}, fmt.Errorf("%w: exact change body mismatch", ErrInvalid)
		}
		key := artifact.MemberID + "\x00" + artifact.SourceKey
		if foundChanges[key] {
			return Manifest{}, fmt.Errorf("%w: duplicate exact change body", ErrInvalid)
		}
		foundChanges[key] = true
	}
	for memberID, record := range records {
		for _, edit := range record.Session.FileEdits {
			for _, change := range edit.Changes {
				if !foundChanges[memberID+"\x00"+change.ID] {
					return Manifest{}, fmt.Errorf("%w: exact change body is missing", ErrIncomplete)
				}
			}
		}
	}
	return manifest, nil
}

func VerifyDirectory(root string) (Manifest, error) {
	manifestBytes, err := os.ReadFile(filepath.Join(root, ManifestFileName))
	if err != nil {
		return Manifest{}, err
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
