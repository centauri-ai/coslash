// Package artifacts owns workflow artifact validation and promotion.
//
// Agents write only into their own attempt output directory. This package is
// the only writer of the promoted blob store and the manifest, so a prompt that
// says "skip validation" is text, while promotion is a gate the text cannot
// reach.
package artifacts

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/runfs"
)

// Size bounds by artifact class.
const (
	MaxMarkdownBytes       int64 = 1 << 20
	MaxJSONBytes           int64 = 256 << 10
	MaxControllerBlobBytes int64 = 8 << 20
)

// Storage layout inside a run root. The embedded `.fleetlog/run/**` protocol is
// preserved for parity with the legacy agents.
const (
	artifactsDirectory = ".fleetlog/run/artifacts"
	blobsDirectory     = artifactsDirectory + "/blobs"
	manifestName       = artifactsDirectory + "/manifest.jsonl"
)

// Producer identifies what created an artifact.
type Producer struct {
	ComponentID string `json:"componentId"`
	Instance    int    `json:"instance"`
	SeatID      string `json:"seatId,omitempty"`
	Attempt     int    `json:"attempt,omitempty"`
}

// Record is one promoted artifact. Path is relative to the run root.
type Record struct {
	ArtifactID string    `json:"artifactId"`
	Kind       string    `json:"kind"`
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	Sha256     string    `json:"sha256"`
	Bytes      int64     `json:"bytes"`
	CreatedAt  time.Time `json:"createdAt"`
	Producer   Producer  `json:"producer"`
}

// Validator inspects candidate bytes that already passed the generic gates.
// Product packages supply schema checks; nil accepts any well-formed candidate.
type Validator func(contents []byte) error

// Store promotes validated candidates into a content-addressed blob store and
// records each promotion in an append-only manifest.
type Store struct {
	scope    *runfs.Scope
	manifest *runfs.EventLog
	now      func() time.Time
}

// Options configures a Store. Now defaults to time.Now.
type Options struct {
	Now func() time.Time
}

// NewStore binds a store to a scope rooted at one run root.
//
// The blob directory is created here, once, rather than on demand during each
// promotion. Two concurrent promotions would otherwise both try to create it,
// and the loser of that race fails on an already-created parent.
func NewStore(ctx context.Context, scope *runfs.Scope, options Options) (*Store, error) {
	if scope == nil {
		return nil, newError(CodeRunNotReady, "an artifact store requires a run scope")
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	if err := scope.MkdirAll(ctx, blobsDirectory); err != nil {
		return nil, newError(CodeRunNotReady, "the artifact blob directory could not be created").
			withDetail(err.Error()).withCause(err)
	}
	manifest, err := runfs.NewEventLog(scope, manifestName, runfs.EventLogOptions{Now: now})
	if err != nil {
		return nil, newError(CodeManifestFailed, "the artifact manifest could not be opened").
			withDetail(err.Error()).withCause(err)
	}
	return &Store{scope: scope, manifest: manifest, now: now}, nil
}

// CandidateOptions locates and bounds one agent-written candidate.
//
// Directory and Name are relative to the run root. The caller owns the attempt
// layout, so this package stays free of DaGama and Atlas vocabulary.
type CandidateOptions struct {
	// Directory is the attempt output directory the candidate must live in.
	Directory string
	// Name is the candidate's basename.
	Name string
	// MaxBytes bounds the candidate. Zero selects MaxMarkdownBytes.
	MaxBytes int64
	// RequireUTF8 rejects bytes that are not valid UTF-8. Markdown and JSON
	// candidates set this; binary candidates do not.
	RequireUTF8 bool
	// Validate runs after the generic gates pass.
	Validate Validator
}

// ReadCandidate validates and returns a candidate without promoting it.
//
// Every gate below is a refusal, not a repair: a substituted replacement
// character would no longer match the digest this package is about to attest.
func (s *Store) ReadCandidate(ctx context.Context, options CandidateOptions) ([]byte, error) {
	if err := validArtifactName(options.Name); err != nil {
		return nil, err
	}
	directory, err := cleanRelativeDirectory(options.Directory)
	if err != nil {
		return nil, err
	}
	limit := options.MaxBytes
	if limit <= 0 {
		limit = MaxMarkdownBytes
	}

	candidate := path.Join(directory, options.Name)
	// runfs refuses traversal and every symlinked component below the run root,
	// so containment is settled by the cleaned relative path alone. The legacy
	// realpath-and-compare dance existed only because its filesystem helper had
	// no scoped root.
	if !withinDirectory(candidate, directory) {
		return nil, newError(CodeInvalidOutput, "the artifact path escapes the attempt output directory")
	}

	contents, err := s.scope.ReadFile(ctx, candidate)
	if err != nil {
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return nil, newError(CodeMissingOutput, "required artifact missing: "+options.Name).withCause(err)
		case errors.Is(err, runfs.ErrSymlink):
			return nil, newError(CodeInvalidOutput, "the artifact path must not be a symbolic link").withCause(err)
		case errors.Is(err, runfs.ErrNotRegular):
			return nil, newError(CodeInvalidOutput, "the artifact path must be a regular file").withCause(err)
		case errors.Is(err, runfs.ErrTooLarge):
			return nil, newError(CodeInvalidOutput,
				fmt.Sprintf("the artifact is over the %d byte limit", limit)).withCause(err)
		default:
			return nil, newError(CodeInvalidOutput, "the artifact could not be read").
				withDetail(err.Error()).withCause(err)
		}
	}

	if len(contents) == 0 {
		return nil, newError(CodeInvalidOutput, "the artifact is empty")
	}
	if int64(len(contents)) > limit {
		return nil, newError(CodeInvalidOutput,
			fmt.Sprintf("the artifact is over the %d byte limit", limit))
	}
	if options.RequireUTF8 && !utf8.Valid(contents) {
		return nil, newError(CodeInvalidOutput, "the artifact is not valid UTF-8")
	}
	if options.Validate != nil {
		if err := options.Validate(contents); err != nil {
			return nil, newError(CodeInvalidOutput, "the artifact failed schema validation").
				withDetail(err.Error()).withCause(err)
		}
	}
	return contents, nil
}

// PromoteOptions promotes bytes already held by the controller.
type PromoteOptions struct {
	Name string
	Kind string
	// Extension names the content-addressed blob suffix, for example ".md",
	// ".json", or ".patch". A missing leading dot is added.
	Extension string
	Producer  Producer
	// MaxBytes bounds the promotion. Zero selects MaxControllerBlobBytes, or
	// MaxJSONBytes for a ".json" extension.
	MaxBytes int64
}

// PromoteCandidate validates an agent-written candidate and promotes it.
func (s *Store) PromoteCandidate(
	ctx context.Context,
	candidate CandidateOptions,
	promote PromoteOptions,
) (Record, error) {
	contents, err := s.ReadCandidate(ctx, candidate)
	if err != nil {
		return Record{}, err
	}
	return s.Promote(ctx, contents, promote)
}

// Promote writes bytes into the content-addressed blob store and appends the
// manifest record.
//
// Promotion is immutable: a blob name is its own digest, so an existing blob
// with the same name already holds identical bytes. The digest of an existing
// blob is verified rather than assumed, because a truncated or externally
// corrupted blob would otherwise be silently attested as valid.
func (s *Store) Promote(ctx context.Context, contents []byte, options PromoteOptions) (Record, error) {
	if err := validArtifactName(options.Name); err != nil {
		return Record{}, err
	}
	if options.Kind == "" {
		return Record{}, newError(CodeInvalidOutput, "the artifact kind is required")
	}
	if err := validProducer(options.Producer); err != nil {
		return Record{}, err
	}
	extension, err := normalizeExtension(options.Extension)
	if err != nil {
		return Record{}, err
	}
	limit := options.MaxBytes
	if limit <= 0 {
		limit = MaxControllerBlobBytes
		if extension == ".json" {
			limit = MaxJSONBytes
		}
	}
	if len(contents) == 0 {
		return Record{}, newError(CodeInvalidOutput, "the artifact is empty")
	}
	if int64(len(contents)) > limit {
		return Record{}, newError(CodeInvalidOutput,
			fmt.Sprintf("the artifact is over the %d byte limit", limit))
	}

	sum := sha256.Sum256(contents)
	digest := hex.EncodeToString(sum[:])
	blobName := digest + extension
	blobPath := blobsDirectory + "/" + blobName

	existing, readErr := s.scope.ReadFile(ctx, blobPath)
	switch {
	case readErr == nil:
		existingSum := sha256.Sum256(existing)
		if hex.EncodeToString(existingSum[:]) != digest {
			return Record{}, newError(CodeBlobConflict,
				"a promoted artifact blob no longer matches its digest")
		}
	case errors.Is(readErr, fs.ErrNotExist):
		if err := s.scope.AtomicWrite(ctx, blobPath, contents); err != nil {
			return Record{}, newError(CodeInvalidOutput, "the artifact blob could not be written").
				withDetail(err.Error()).withCause(err)
		}
	default:
		return Record{}, newError(CodeInvalidOutput, "the artifact blob could not be inspected").
			withDetail(readErr.Error()).withCause(readErr)
	}

	artifactID, err := newArtifactID()
	if err != nil {
		return Record{}, newError(CodeInvalidOutput, "an artifact identifier could not be generated").
			withDetail(err.Error()).withCause(err)
	}

	record := Record{
		ArtifactID: artifactID,
		Kind:       options.Kind,
		Name:       options.Name,
		Path:       blobPath,
		Sha256:     digest,
		Bytes:      int64(len(contents)),
		CreatedAt:  s.now().UTC(),
		Producer:   options.Producer,
	}

	// The blob is written before the manifest entry, so a crash between the two
	// leaves an unreferenced blob rather than a manifest entry pointing at
	// nothing. Content addressing makes the orphan harmless and reusable.
	if _, err := s.manifest.Append(ctx, "artifact_promoted", record); err != nil {
		return Record{}, newError(CodeManifestFailed, "the artifact manifest could not be appended").
			withDetail(err.Error()).withCause(err)
	}
	return record, nil
}

// List returns the promoted records in manifest order.
func (s *Store) List(ctx context.Context) ([]Record, error) {
	result, err := s.manifest.Read(ctx)
	if err != nil {
		return nil, newError(CodeManifestFailed, "the artifact manifest could not be read").
			withDetail(err.Error()).withCause(err)
	}
	records := make([]Record, 0, len(result.Events))
	for _, event := range result.Events {
		if event.Type != "artifact_promoted" {
			continue
		}
		var record Record
		if err := json.Unmarshal(event.Data, &record); err != nil {
			return nil, newError(CodeManifestFailed, "the artifact manifest is corrupt").
				withDetail(err.Error()).withCause(err)
		}
		records = append(records, record)
	}
	return records, nil
}

// Latest returns the most recent promotion of one artifact name.
func (s *Store) Latest(ctx context.Context, name string) (Record, bool, error) {
	records, err := s.List(ctx)
	if err != nil {
		return Record{}, false, err
	}
	for index := len(records) - 1; index >= 0; index-- {
		if records[index].Name == name {
			return records[index], true, nil
		}
	}
	return Record{}, false, nil
}

// ReadPromoted reads a promoted blob by its manifest path.
func (s *Store) ReadPromoted(ctx context.Context, relativePath string) ([]byte, error) {
	cleaned := path.Clean(relativePath)
	if !strings.HasPrefix(cleaned, blobsDirectory+"/") || strings.Contains(relativePath, "\x00") {
		return nil, newError(CodeUnknownBlob, "the artifact path is not a promoted blob")
	}
	contents, err := s.scope.ReadFile(ctx, cleaned)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, newError(CodeNotFound, "the artifact was not found").withCause(err)
		}
		if errors.Is(err, runfs.ErrSymlink) || errors.Is(err, runfs.ErrNotRegular) {
			return nil, newError(CodeUnsafePath, "the artifact path is unsafe").withCause(err)
		}
		return nil, newError(CodeUnknownBlob, "the artifact could not be read").
			withDetail(err.Error()).withCause(err)
	}
	return contents, nil
}

// JSONObjectValidator accepts only a well-formed JSON object, the shape every
// workflow artifact schema in this suite uses at its top level.
func JSONObjectValidator(contents []byte) error {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(contents, &document); err != nil {
		return fmt.Errorf("artifact is not a JSON object: %w", err)
	}
	return nil
}

func validArtifactName(name string) error {
	if name == "" {
		return newError(CodeInvalidOutput, "the artifact name is required")
	}
	if len(name) > 128 {
		return newError(CodeInvalidOutput, "the artifact name is too long")
	}
	if name != path.Base(name) || name == "." || name == ".." {
		return newError(CodeInvalidOutput, "the artifact name is unsafe")
	}
	if strings.ContainsAny(name, "/\\\x00") || strings.Contains(name, "..") {
		return newError(CodeInvalidOutput, "the artifact name is unsafe")
	}
	if strings.HasPrefix(name, "-") || strings.HasPrefix(name, ".") {
		return newError(CodeInvalidOutput, "the artifact name is unsafe")
	}
	for _, character := range name {
		switch {
		case character >= 'a' && character <= 'z',
			character >= 'A' && character <= 'Z',
			character >= '0' && character <= '9',
			character == '-', character == '_', character == '.':
		default:
			return newError(CodeInvalidOutput, "the artifact name is unsafe")
		}
	}
	return nil
}

func validProducer(producer Producer) error {
	if producer.ComponentID == "" {
		return newError(CodeInvalidOutput, "the artifact producer component is required")
	}
	if len(producer.ComponentID) > 64 || len(producer.SeatID) > 64 {
		return newError(CodeInvalidOutput, "the artifact producer identity is too long")
	}
	if producer.Instance < 0 || producer.Attempt < 0 {
		return newError(CodeInvalidOutput, "the artifact producer counters must not be negative")
	}
	return nil
}

func normalizeExtension(extension string) (string, error) {
	if extension == "" {
		return "", newError(CodeInvalidOutput, "the artifact extension is required")
	}
	if !strings.HasPrefix(extension, ".") {
		extension = "." + extension
	}
	if len(extension) > 16 {
		return "", newError(CodeInvalidOutput, "the artifact extension is too long")
	}
	for _, character := range extension[1:] {
		switch {
		case character >= 'a' && character <= 'z',
			character >= '0' && character <= '9':
		default:
			return "", newError(CodeInvalidOutput, "the artifact extension is unsafe")
		}
	}
	return extension, nil
}

func cleanRelativeDirectory(directory string) (string, error) {
	if directory == "" {
		return "", newError(CodeInvalidOutput, "the attempt output directory is required")
	}
	if strings.Contains(directory, "\x00") || path.IsAbs(directory) {
		return "", newError(CodeInvalidOutput, "the attempt output directory is unsafe")
	}
	cleaned := path.Clean(directory)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", newError(CodeInvalidOutput, "the attempt output directory is unsafe")
	}
	return cleaned, nil
}

func withinDirectory(candidate, directory string) bool {
	cleaned := path.Clean(candidate)
	if directory == "." {
		return !strings.Contains(cleaned, "/")
	}
	return strings.HasPrefix(cleaned, directory+"/")
}

func newArtifactID() (string, error) {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}
