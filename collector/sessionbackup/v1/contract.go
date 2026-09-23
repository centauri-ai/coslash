// Package sessionbackupv1 defines the portable complete-session backup
// manifest. Artifact bytes stay separate from the canonical manifest.
package sessionbackupv1

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"reflect"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
)

const (
	SchemaVersion       = "session-backup/v1"
	CanonicalVersion    = "coslash-canonical-json/v1"
	DatabaseRowsVersion = "session-backup-db-rows/v1"
	ParsedRecordVersion = "full-session-record/v1"
	ManifestFileName    = "manifest.json"
	MaxManifestBytes    = 64 << 20
	MaxArtifactBytes    = 512 << 20
	MaxTotalBytes       = 4 << 30
	MaxMembers          = fullsessionv1.MaxItems
	MaxArtifacts        = fullsessionv1.MaxItems

	SourceLocal = "local"
	SourceSSH   = "ssh"

	ArtifactSourceCodex   = "codex"
	ArtifactSourceCoSlash = "coslash"

	KindRawTranscript       = "raw-transcript"
	KindRawSidecar          = "raw-sidecar"
	KindRawMetadataRows     = "raw-metadata-rows"
	KindParsedSessionRecord = "parsed-session-record"
	KindExactChangeBody     = "exact-change-body"
	KindSessionEnrichment   = "session-enrichment"
	KindSynthesis           = "synthesis"

	EncodingIdentity = "identity"

	ProblemUnsupported    = "complete_backup_unsupported"
	ProblemUnavailable    = "artifact_unavailable"
	ProblemUnreadable     = "artifact_unreadable"
	ProblemUnstable       = "artifact_unstable"
	ProblemUnattributable = "artifact_unattributable"
	ProblemInvalid        = "artifact_invalid"
)

var (
	ErrInvalid    = errors.New("invalid session backup")
	ErrIncomplete = errors.New("incomplete session backup")

	ArtifactKinds = []string{
		KindRawTranscript,
		KindRawSidecar,
		KindParsedSessionRecord,
		KindExactChangeBody,
		KindSessionEnrichment,
		KindSynthesis,
	}
)

type Manifest struct {
	SchemaVersion        string             `json:"schemaVersion"`
	CanonicalVersion     string             `json:"canonicalVersion"`
	RequiredVersions     []string           `json:"requiredVersions"`
	Source               SourceIdentity     `json:"source"`
	Repository           RepositoryIdentity `json:"repository"`
	Producer             ProducerIdentity   `json:"producer"`
	Family               FamilyIdentity     `json:"family"`
	Members              []Member           `json:"members"`
	Artifacts            []Artifact         `json:"artifacts"`
	Summary              Summary            `json:"summary"`
	CaptureProblems      []CaptureProblem   `json:"captureProblems"`
	CompleteBackupSHA256 string             `json:"completeBackupSha256"`
}

type SourceIdentity struct {
	Kind           string `json:"kind"`
	SourceID       string `json:"sourceId"`
	Agent          string `json:"agent"`
	SourceRevision string `json:"sourceRevision"`
}

type RepositoryIdentity struct {
	Canonical string `json:"canonical"`
	VCS       string `json:"vcs"`
}

type ProducerIdentity struct {
	Name          string `json:"name"`
	Version       string `json:"version"`
	ParserVersion string `json:"parserVersion"`
}

type FamilyIdentity struct {
	FamilyID     string `json:"familyId"`
	RootMemberID string `json:"rootMemberId"`
}

type Member struct {
	Ordinal             int    `json:"ordinal"`
	MemberID            string `json:"memberId"`
	ParentMemberID      string `json:"parentMemberId"`
	SourceRevision      string `json:"sourceRevision"`
	SynthesisRevisionMs int64  `json:"synthesisRevisionMs"`
}

type Artifact struct {
	Ordinal     int    `json:"ordinal"`
	LogicalName string `json:"logicalName"`
	MemberID    string `json:"memberId"`
	Source      string `json:"source"`
	Kind        string `json:"kind"`
	SourceKey   string `json:"sourceKey"`
	MediaType   string `json:"mediaType"`
	Encoding    string `json:"encoding"`
	ByteLength  int64  `json:"byteLength"`
	SHA256      string `json:"sha256"`
}

type ArtifactCount struct {
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

type Summary struct {
	ArtifactCount  int             `json:"artifactCount"`
	ArtifactCounts []ArtifactCount `json:"artifactCounts"`
	TotalBytes     int64           `json:"totalBytes"`
}

type CaptureProblem struct {
	Code      string `json:"code"`
	MemberID  string `json:"memberId"`
	Kind      string `json:"kind"`
	Retryable bool   `json:"retryable"`
}

type SupportStatus struct {
	Agent        string
	SourceKind   string
	Supported    bool
	BlockingCode string
}

var CoverageMatrix = []SupportStatus{
	{Agent: "codex", SourceKind: SourceLocal, Supported: true},
	{Agent: "codex", SourceKind: SourceSSH, Supported: true},
	{Agent: "claude", SourceKind: SourceLocal, BlockingCode: ProblemUnsupported},
	{Agent: "claude", SourceKind: SourceSSH, BlockingCode: ProblemUnsupported},
	{Agent: "cursor", SourceKind: SourceLocal, BlockingCode: ProblemUnsupported},
	{Agent: "cursor", SourceKind: SourceSSH, BlockingCode: ProblemUnsupported},
	{Agent: "opencode", SourceKind: SourceLocal, BlockingCode: ProblemUnsupported},
	{Agent: "opencode", SourceKind: SourceSSH, BlockingCode: ProblemUnsupported},
}

// Freeze orders a complete family deterministically, fills byte evidence, and
// computes the family revision over the canonical manifest with an empty hash.
func Freeze(manifest Manifest, blobs map[string][]byte) (Manifest, error) {
	manifest = cloneManifest(manifest)
	manifest.SchemaVersion = SchemaVersion
	manifest.CanonicalVersion = CanonicalVersion
	manifest.CompleteBackupSHA256 = ""
	if len(manifest.CaptureProblems) != 0 {
		return Manifest{}, fmt.Errorf("%w: capture problem blocks completion", ErrIncomplete)
	}
	manifest.CaptureProblems = []CaptureProblem{}
	if err := orderMembers(&manifest); err != nil {
		return Manifest{}, err
	}
	if len(manifest.Members) > MaxMembers || len(manifest.Artifacts) > MaxArtifacts {
		return Manifest{}, fmt.Errorf("%w: manifest collection exceeds item limit", ErrInvalid)
	}
	memberOrdinal := make(map[string]int, len(manifest.Members))
	for index := range manifest.Members {
		manifest.Members[index].Ordinal = index
		memberOrdinal[manifest.Members[index].MemberID] = index
	}
	sort.Slice(manifest.Artifacts, func(i, j int) bool {
		left, right := manifest.Artifacts[i], manifest.Artifacts[j]
		if memberOrdinal[left.MemberID] != memberOrdinal[right.MemberID] {
			return memberOrdinal[left.MemberID] < memberOrdinal[right.MemberID]
		}
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		return left.LogicalName < right.LogicalName
	})
	seen := map[string]bool{}
	var totalBytes int64
	for index := range manifest.Artifacts {
		artifact := &manifest.Artifacts[index]
		if seen[artifact.LogicalName] {
			return Manifest{}, fmt.Errorf("%w: duplicate artifact name", ErrInvalid)
		}
		blob, ok := blobs[artifact.LogicalName]
		if !ok {
			return Manifest{}, fmt.Errorf("%w: artifact %q unavailable", ErrIncomplete, artifact.LogicalName)
		}
		if int64(len(blob)) > MaxArtifactBytes || totalBytes > MaxTotalBytes-int64(len(blob)) {
			return Manifest{}, fmt.Errorf("%w: artifact byte limit exceeded", ErrInvalid)
		}
		totalBytes += int64(len(blob))
		artifact.Ordinal = index
		artifact.ByteLength = int64(len(blob))
		sum := sha256.Sum256(blob)
		artifact.SHA256 = hex.EncodeToString(sum[:])
		seen[artifact.LogicalName] = true
	}
	if len(seen) != len(blobs) {
		return Manifest{}, fmt.Errorf("%w: unmanifested artifact bytes", ErrInvalid)
	}
	if err := validateArtifactContents(manifest, blobs); err != nil {
		return Manifest{}, err
	}
	manifest.Summary = summarize(manifest.Artifacts)
	sort.Strings(manifest.RequiredVersions)
	if err := validate(manifest, false); err != nil {
		return Manifest{}, err
	}
	preimage, err := json.Marshal(manifest)
	if err != nil {
		return Manifest{}, err
	}
	if int64(len(preimage))+sha256.Size*2 > MaxManifestBytes {
		return Manifest{}, fmt.Errorf("%w: manifest exceeds byte limit", ErrInvalid)
	}
	sum := sha256.Sum256(preimage)
	manifest.CompleteBackupSHA256 = hex.EncodeToString(sum[:])
	return manifest, Validate(manifest)
}

func Marshal(manifest Manifest) ([]byte, error) {
	if err := Validate(manifest); err != nil {
		return nil, err
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > MaxManifestBytes {
		return nil, fmt.Errorf("%w: manifest exceeds byte limit", ErrInvalid)
	}
	return data, nil
}

func Validate(manifest Manifest) error {
	if err := validate(manifest, true); err != nil {
		return err
	}
	copy := cloneManifest(manifest)
	copy.CompleteBackupSHA256 = ""
	preimage, err := json.Marshal(copy)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(preimage)
	if manifest.CompleteBackupSHA256 != hex.EncodeToString(sum[:]) {
		return fmt.Errorf("%w: complete backup hash mismatch", ErrInvalid)
	}
	return nil
}

func validate(manifest Manifest, requireHash bool) error {
	if manifest.SchemaVersion != SchemaVersion || manifest.CanonicalVersion != CanonicalVersion {
		return fmt.Errorf("%w: unsupported manifest semantics", ErrInvalid)
	}
	knownVersions := map[string]bool{SchemaVersion: true, ParsedRecordVersion: true}
	previous := ""
	for _, version := range manifest.RequiredVersions {
		if !knownVersions[version] || version <= previous {
			return fmt.Errorf("%w: unknown or unsorted required version", ErrInvalid)
		}
		previous = version
	}
	if !contains(manifest.RequiredVersions, SchemaVersion) || !contains(manifest.RequiredVersions, ParsedRecordVersion) {
		return fmt.Errorf("%w: required version missing", ErrInvalid)
	}
	if manifest.Source.Agent != "codex" || (manifest.Source.Kind != SourceLocal && manifest.Source.Kind != SourceSSH) ||
		!identifier(manifest.Source.SourceID) || !identifier(manifest.Source.SourceRevision) {
		return fmt.Errorf("%w: invalid source identity", ErrInvalid)
	}
	if !plainText(manifest.Repository.Canonical) || manifest.Repository.Canonical == "" || manifest.Repository.VCS != "git" ||
		!identifier(manifest.Producer.Name) || !identifier(manifest.Producer.Version) || !identifier(manifest.Producer.ParserVersion) ||
		!identifier(manifest.Family.FamilyID) || !identifier(manifest.Family.RootMemberID) {
		return fmt.Errorf("%w: invalid repository, producer, or family identity", ErrInvalid)
	}
	if manifest.CaptureProblems == nil || len(manifest.CaptureProblems) != 0 {
		return fmt.Errorf("%w: completed manifest contains capture problems", ErrIncomplete)
	}
	if len(manifest.Members) == 0 || len(manifest.Members) > MaxMembers || len(manifest.Artifacts) > MaxArtifacts ||
		manifest.Members[0].MemberID != manifest.Family.RootMemberID || manifest.Members[0].ParentMemberID != "" {
		return fmt.Errorf("%w: invalid family root", ErrInvalid)
	}
	members := map[string]int{}
	seenMembers := map[string]bool{}
	for index, member := range manifest.Members {
		if member.Ordinal != index || !identifier(member.MemberID) || !identifier(member.SourceRevision) ||
			member.SynthesisRevisionMs < 0 || member.SynthesisRevisionMs > fullsessionv1.MaxSessionTimestampMs || seenMembers[member.MemberID] {
			return fmt.Errorf("%w: invalid member", ErrInvalid)
		}
		if index > 0 {
			parent, ok := members[member.ParentMemberID]
			if !ok || parent >= index {
				return fmt.Errorf("%w: member parent does not precede child", ErrInvalid)
			}
		}
		members[member.MemberID] = index
		seenMembers[member.MemberID] = true
	}
	canonicalMembers := Manifest{Family: manifest.Family, Members: append([]Member(nil), manifest.Members...)}
	if err := orderMembers(&canonicalMembers); err != nil || !reflect.DeepEqual(canonicalMembers.Members, manifest.Members) {
		return fmt.Errorf("%w: members are not deterministically ordered", ErrInvalid)
	}
	if len(manifest.Artifacts) == 0 {
		return fmt.Errorf("%w: no artifacts", ErrIncomplete)
	}
	seenNames := map[string]bool{}
	seenSourceKeys := map[string]bool{}
	memberKinds := make(map[string]map[string]int, len(manifest.Members))
	previousMember := -1
	previousKind, previousName := "", ""
	var totalBytes int64
	for index, artifact := range manifest.Artifacts {
		memberPosition, ok := members[artifact.MemberID]
		if seenNames[artifact.LogicalName] {
			return fmt.Errorf("%w: duplicate artifact name", ErrInvalid)
		}
		if !logicalName(artifact.LogicalName) {
			return fmt.Errorf("%w: unsafe artifact name", ErrInvalid)
		}
		if artifact.Ordinal != index || !ok ||
			(artifact.Source != ArtifactSourceCodex && artifact.Source != ArtifactSourceCoSlash) || !contains(ArtifactKinds, artifact.Kind) ||
			artifact.SourceKey == "" || !plainText(artifact.SourceKey) || artifact.MediaType == "" || !plainText(artifact.MediaType) || artifact.Encoding != EncodingIdentity ||
			artifact.ByteLength < 0 || artifact.ByteLength > MaxArtifactBytes || !digest(artifact.SHA256) {
			return fmt.Errorf("%w: invalid artifact", ErrInvalid)
		}
		if (strings.HasPrefix(artifact.Kind, "raw-") && artifact.Source != ArtifactSourceCodex) ||
			(!strings.HasPrefix(artifact.Kind, "raw-") && artifact.Source != ArtifactSourceCoSlash) {
			return fmt.Errorf("%w: artifact source does not match kind", ErrInvalid)
		}
		sourceKey := artifact.MemberID + "\x00" + artifact.Kind + "\x00" + artifact.SourceKey
		if seenSourceKeys[sourceKey] {
			return fmt.Errorf("%w: duplicate artifact source key", ErrInvalid)
		}
		if totalBytes > MaxTotalBytes-artifact.ByteLength {
			return fmt.Errorf("%w: artifact byte total exceeds limit", ErrInvalid)
		}
		totalBytes += artifact.ByteLength
		if memberPosition < previousMember || (memberPosition == previousMember &&
			(artifact.Kind < previousKind || (artifact.Kind == previousKind && artifact.LogicalName <= previousName))) {
			return fmt.Errorf("%w: artifacts are not deterministically ordered", ErrInvalid)
		}
		seenNames[artifact.LogicalName] = true
		seenSourceKeys[sourceKey] = true
		if memberKinds[artifact.MemberID] == nil {
			memberKinds[artifact.MemberID] = map[string]int{}
		}
		memberKinds[artifact.MemberID][artifact.Kind]++
		previousMember, previousKind, previousName = memberPosition, artifact.Kind, artifact.LogicalName
	}
	for _, member := range manifest.Members {
		kinds := memberKinds[member.MemberID]
		if kinds[KindParsedSessionRecord] == 0 || kinds[KindRawTranscript] == 0 || kinds[KindSessionEnrichment] == 0 {
			return fmt.Errorf("%w: member %q lacks required artifacts", ErrIncomplete, member.MemberID)
		}
		if kinds[KindParsedSessionRecord] != 1 || kinds[KindSessionEnrichment] != 1 {
			return fmt.Errorf("%w: member %q has duplicate singleton artifacts", ErrInvalid, member.MemberID)
		}
		if member.SynthesisRevisionMs > 0 && kinds[KindSynthesis] == 0 {
			return fmt.Errorf("%w: member %q lacks required synthesis", ErrIncomplete, member.MemberID)
		}
		if kinds[KindSynthesis] > 1 || (member.SynthesisRevisionMs == 0 && kinds[KindSynthesis] != 0) {
			return fmt.Errorf("%w: member %q has inconsistent synthesis artifacts", ErrInvalid, member.MemberID)
		}
	}
	if !reflect.DeepEqual(manifest.Summary, summarize(manifest.Artifacts)) {
		return fmt.Errorf("%w: summary mismatch", ErrInvalid)
	}
	if requireHash && !digest(manifest.CompleteBackupSHA256) {
		return fmt.Errorf("%w: invalid complete backup hash", ErrInvalid)
	}
	if !requireHash && manifest.CompleteBackupSHA256 != "" {
		return fmt.Errorf("%w: hash must be empty while freezing", ErrInvalid)
	}
	return nil
}

func validateArtifactContents(manifest Manifest, blobs map[string][]byte) error {
	members := make(map[string]Member, len(manifest.Members))
	for _, member := range manifest.Members {
		members[member.MemberID] = member
	}
	records := make(map[string]fullsessionv1.Record, len(manifest.Members))
	changeArtifacts := make(map[string]map[string][]byte, len(manifest.Members))
	for _, artifact := range manifest.Artifacts {
		blob := blobs[artifact.LogicalName]
		member := members[artifact.MemberID]
		switch artifact.Kind {
		case KindParsedSessionRecord:
			record, err := fullsessionv1.Decode(blob)
			if err != nil {
				return fmt.Errorf("%w: parsed record %q: %v", ErrInvalid, artifact.LogicalName, err)
			}
			if !parsedRecordIdentityMatches(manifest, member, artifact, record) {
				return fmt.Errorf("%w: parsed record %q identity mismatch", ErrInvalid, artifact.LogicalName)
			}
			records[member.MemberID] = record
		case KindSessionEnrichment:
			if _, err := DecodeEnrichment(blob); err != nil {
				return fmt.Errorf("%w: enrichment %q: %v", ErrInvalid, artifact.LogicalName, err)
			}
		case KindSynthesis:
			record, err := DecodeSynthesisRecord(blob)
			if err != nil {
				return fmt.Errorf("%w: synthesis %q: %v", ErrInvalid, artifact.LogicalName, err)
			}
			if record.Agent != manifest.Source.Agent || record.SessionID != member.MemberID || record.Revision != member.SynthesisRevisionMs {
				return fmt.Errorf("%w: synthesis %q identity mismatch", ErrInvalid, artifact.LogicalName)
			}
		case KindExactChangeBody:
			if changeArtifacts[member.MemberID] == nil {
				changeArtifacts[member.MemberID] = map[string][]byte{}
			}
			if _, exists := changeArtifacts[member.MemberID][artifact.SourceKey]; exists {
				return fmt.Errorf("%w: duplicate exact change body", ErrInvalid)
			}
			changeArtifacts[member.MemberID][artifact.SourceKey] = blob
		}
	}
	for _, member := range manifest.Members {
		record, ok := records[member.MemberID]
		if !ok {
			continue
		}
		remaining := changeArtifacts[member.MemberID]
		for _, edit := range record.Session.FileEdits {
			for _, change := range edit.Changes {
				blob, exists := remaining[change.ID]
				if !exists {
					return fmt.Errorf("%w: member %q lacks exact change body %q", ErrIncomplete, member.MemberID, change.ID)
				}
				if !bytes.Equal(blob, []byte(change.Text)) {
					return fmt.Errorf("%w: exact change body %q mismatch", ErrInvalid, change.ID)
				}
				delete(remaining, change.ID)
			}
		}
		if len(remaining) != 0 {
			return fmt.Errorf("%w: member %q has unreferenced exact change body", ErrInvalid, member.MemberID)
		}
	}
	return nil
}

func parsedRecordIdentityMatches(manifest Manifest, member Member, artifact Artifact, record fullsessionv1.Record) bool {
	return record.SourceID == manifest.Source.SourceID && record.Agent == manifest.Source.Agent &&
		record.SessionID == member.MemberID && record.ParentSessionID == member.ParentMemberID && record.RevisionID == artifact.SourceKey
}

func orderMembers(manifest *Manifest) error {
	byParent := map[string][]Member{}
	seen := map[string]bool{}
	for _, member := range manifest.Members {
		if seen[member.MemberID] {
			return fmt.Errorf("%w: duplicate member", ErrInvalid)
		}
		seen[member.MemberID] = true
		byParent[member.ParentMemberID] = append(byParent[member.ParentMemberID], member)
	}
	for parent := range byParent {
		sort.Slice(byParent[parent], func(i, j int) bool { return byParent[parent][i].MemberID < byParent[parent][j].MemberID })
	}
	root, ok := findMember(manifest.Members, manifest.Family.RootMemberID)
	if !ok || root.ParentMemberID != "" {
		return fmt.Errorf("%w: family root missing", ErrInvalid)
	}
	ordered := make([]Member, 0, len(manifest.Members))
	visiting := map[string]bool{}
	var walk func(Member) error
	walk = func(member Member) error {
		if visiting[member.MemberID] {
			return fmt.Errorf("%w: member cycle", ErrInvalid)
		}
		visiting[member.MemberID] = true
		ordered = append(ordered, member)
		for _, child := range byParent[member.MemberID] {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(root); err != nil {
		return err
	}
	if len(ordered) != len(manifest.Members) {
		return fmt.Errorf("%w: member is outside root family", ErrInvalid)
	}
	manifest.Members = ordered
	return nil
}

func summarize(artifacts []Artifact) Summary {
	counts := map[string]int{}
	var total int64
	for _, artifact := range artifacts {
		counts[artifact.Kind]++
		total += artifact.ByteLength
	}
	kinds := make([]string, 0, len(counts))
	for kind := range counts {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	summary := Summary{ArtifactCount: len(artifacts), TotalBytes: total}
	for _, kind := range kinds {
		summary.ArtifactCounts = append(summary.ArtifactCounts, ArtifactCount{Kind: kind, Count: counts[kind]})
	}
	return summary
}

func cloneManifest(manifest Manifest) Manifest {
	manifest.RequiredVersions = append([]string(nil), manifest.RequiredVersions...)
	manifest.Members = append([]Member(nil), manifest.Members...)
	manifest.Artifacts = append([]Artifact(nil), manifest.Artifacts...)
	manifest.Summary.ArtifactCounts = append([]ArtifactCount(nil), manifest.Summary.ArtifactCounts...)
	if manifest.CaptureProblems != nil {
		manifest.CaptureProblems = append([]CaptureProblem{}, manifest.CaptureProblems...)
	}
	return manifest
}

func findMember(members []Member, id string) (Member, bool) {
	for _, member := range members {
		if member.MemberID == id {
			return member, true
		}
	}
	return Member{}, false
}

func logicalName(value string) bool {
	if value == "" || len(value) > 4096 || !utf8.ValidString(value) || strings.Contains(value, "\\") || strings.HasPrefix(value, "/") || path.Clean(value) != value ||
		(len(value) >= 3 && unicode.IsLetter(rune(value[0])) && value[1] == ':' && value[2] == '/') {
		return false
	}
	for _, r := range value {
		if r <= 0x1f || r == 0x7f {
			return false
		}
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func identifier(value string) bool {
	if value == "" || len(value) > 512 || strings.TrimSpace(value) != value || strings.ContainsAny(value, "/\\") || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if r <= 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func plainText(value string) bool {
	return utf8.ValidString(value) && len(value) <= 1<<20 && strings.TrimSpace(value) == value
}

func digest(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
