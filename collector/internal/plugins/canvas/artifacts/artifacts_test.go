package artifacts

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/runfs"
)

const attemptDirectory = ".fleetlog/run/seats/build/1/out"

func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	scope, err := runfs.OpenScope(root, runfs.ScopeOptions{})
	if err != nil {
		t.Fatalf("OpenScope: %v", err)
	}
	t.Cleanup(func() { _ = scope.Close() })

	fixed := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	store, err := NewStore(t.Context(), scope, Options{Now: func() time.Time { return fixed }})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store, root
}

func writeCandidate(t *testing.T, root, name, contents string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(attemptDirectory), name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func codeOf(t *testing.T, err error) string {
	t.Helper()
	var typed *Error
	if !errors.As(err, &typed) {
		t.Fatalf("expected *Error, got %v", err)
	}
	return typed.Code
}

func markdownCandidate(name string) CandidateOptions {
	return CandidateOptions{
		Directory:   attemptDirectory,
		Name:        name,
		MaxBytes:    MaxMarkdownBytes,
		RequireUTF8: true,
	}
}

func markdownPromotion(name string) PromoteOptions {
	return PromoteOptions{
		Name:      name,
		Kind:      "plan",
		Extension: ".md",
		Producer:  Producer{ComponentID: "build", Instance: 1, SeatID: "seat-1", Attempt: 1},
		MaxBytes:  MaxMarkdownBytes,
	}
}

// ---------------------------------------------------------------------------
// Promotion
// ---------------------------------------------------------------------------

func TestPromoteCandidateWritesBlobAndManifest(t *testing.T) {
	store, root := newStore(t)
	writeCandidate(t, root, "PLAN.md", "# Plan\n")

	record, err := store.PromoteCandidate(t.Context(), markdownCandidate("PLAN.md"), markdownPromotion("PLAN.md"))
	if err != nil {
		t.Fatalf("PromoteCandidate: %v", err)
	}
	if record.Bytes != int64(len("# Plan\n")) {
		t.Fatalf("Bytes = %d", record.Bytes)
	}
	if record.Path != blobsDirectory+"/"+record.Sha256+".md" {
		t.Fatalf("Path = %q, want the digest-named blob", record.Path)
	}

	promoted, err := store.ReadPromoted(t.Context(), record.Path)
	if err != nil {
		t.Fatalf("ReadPromoted: %v", err)
	}
	if string(promoted) != "# Plan\n" {
		t.Fatalf("promoted contents = %q", promoted)
	}

	records, err := store.List(t.Context())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(records) != 1 || records[0].ArtifactID != record.ArtifactID {
		t.Fatalf("List = %+v, want the single promotion", records)
	}
}

func TestPromotionIsContentAddressedAndImmutable(t *testing.T) {
	store, root := newStore(t)
	writeCandidate(t, root, "PLAN.md", "same bytes\n")

	first, err := store.PromoteCandidate(t.Context(), markdownCandidate("PLAN.md"), markdownPromotion("PLAN.md"))
	if err != nil {
		t.Fatalf("first PromoteCandidate: %v", err)
	}
	blob := filepath.Join(root, filepath.FromSlash(first.Path))
	before, err := os.Stat(blob)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}

	second, err := store.PromoteCandidate(t.Context(), markdownCandidate("PLAN.md"), markdownPromotion("PLAN.md"))
	if err != nil {
		t.Fatalf("second PromoteCandidate: %v", err)
	}
	if second.Sha256 != first.Sha256 || second.Path != first.Path {
		t.Fatal("identical bytes promoted to a different blob")
	}
	if second.ArtifactID == first.ArtifactID {
		t.Fatal("two promotions share one artifact identifier")
	}
	after, err := os.Stat(blob)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("an existing blob was rewritten instead of reused")
	}

	records, err := store.List(t.Context())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("List returned %d records, want both promotions recorded", len(records))
	}
}

func TestPromotionDetectsACorruptedBlob(t *testing.T) {
	store, root := newStore(t)
	writeCandidate(t, root, "PLAN.md", "original\n")

	record, err := store.PromoteCandidate(t.Context(), markdownCandidate("PLAN.md"), markdownPromotion("PLAN.md"))
	if err != nil {
		t.Fatalf("PromoteCandidate: %v", err)
	}

	// A truncated or externally corrupted blob must not be silently attested as
	// valid just because its filename still claims the original digest.
	blob := filepath.Join(root, filepath.FromSlash(record.Path))
	if err := os.WriteFile(blob, []byte("tampered\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err = store.PromoteCandidate(t.Context(), markdownCandidate("PLAN.md"), markdownPromotion("PLAN.md"))
	if got := codeOf(t, err); got != CodeBlobConflict {
		t.Fatalf("code = %q, want %q", got, CodeBlobConflict)
	}
}

func TestLatestReturnsTheMostRecentPromotion(t *testing.T) {
	store, root := newStore(t)

	writeCandidate(t, root, "PLAN.md", "first\n")
	if _, err := store.PromoteCandidate(t.Context(), markdownCandidate("PLAN.md"), markdownPromotion("PLAN.md")); err != nil {
		t.Fatalf("PromoteCandidate: %v", err)
	}
	writeCandidate(t, root, "PLAN.md", "second\n")
	newest, err := store.PromoteCandidate(t.Context(), markdownCandidate("PLAN.md"), markdownPromotion("PLAN.md"))
	if err != nil {
		t.Fatalf("PromoteCandidate: %v", err)
	}

	found, ok, err := store.Latest(t.Context(), "PLAN.md")
	if err != nil || !ok {
		t.Fatalf("Latest: ok=%v err=%v", ok, err)
	}
	if found.Sha256 != newest.Sha256 {
		t.Fatal("Latest returned an older promotion")
	}

	if _, ok, err := store.Latest(t.Context(), "ABSENT.md"); err != nil || ok {
		t.Fatalf("Latest(absent): ok=%v err=%v", ok, err)
	}
}

// ---------------------------------------------------------------------------
// Candidate gates
// ---------------------------------------------------------------------------

func TestCandidateGates(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T, root string)
		options func() CandidateOptions
		code    string
	}{
		{
			name:    "missing file",
			prepare: func(*testing.T, string) {},
			options: func() CandidateOptions { return markdownCandidate("PLAN.md") },
			code:    CodeMissingOutput,
		},
		{
			name: "empty file",
			prepare: func(t *testing.T, root string) {
				writeCandidate(t, root, "PLAN.md", "")
			},
			options: func() CandidateOptions { return markdownCandidate("PLAN.md") },
			code:    CodeInvalidOutput,
		},
		{
			name: "over the limit",
			prepare: func(t *testing.T, root string) {
				writeCandidate(t, root, "PLAN.md", strings.Repeat("x", 64))
			},
			options: func() CandidateOptions {
				options := markdownCandidate("PLAN.md")
				options.MaxBytes = 16
				return options
			},
			code: CodeInvalidOutput,
		},
		{
			name: "invalid UTF-8",
			prepare: func(t *testing.T, root string) {
				writeCandidate(t, root, "PLAN.md", "\xff\xfe bad\n")
			},
			options: func() CandidateOptions { return markdownCandidate("PLAN.md") },
			code:    CodeInvalidOutput,
		},
		{
			name:    "traversal in the name",
			prepare: func(*testing.T, string) {},
			options: func() CandidateOptions { return markdownCandidate("../../escape.md") },
			code:    CodeInvalidOutput,
		},
		{
			name:    "separator in the name",
			prepare: func(*testing.T, string) {},
			options: func() CandidateOptions { return markdownCandidate("nested/PLAN.md") },
			code:    CodeInvalidOutput,
		},
		{
			name:    "dotfile name",
			prepare: func(*testing.T, string) {},
			options: func() CandidateOptions { return markdownCandidate(".hidden") },
			code:    CodeInvalidOutput,
		},
		{
			name:    "leading dash name",
			prepare: func(*testing.T, string) {},
			options: func() CandidateOptions { return markdownCandidate("-flag.md") },
			code:    CodeInvalidOutput,
		},
		{
			name:    "traversal in the directory",
			prepare: func(*testing.T, string) {},
			options: func() CandidateOptions {
				options := markdownCandidate("PLAN.md")
				options.Directory = "../outside"
				return options
			},
			code: CodeInvalidOutput,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, root := newStore(t)
			test.prepare(t, root)
			_, err := store.ReadCandidate(t.Context(), test.options())
			if got := codeOf(t, err); got != test.code {
				t.Fatalf("code = %q, want %q", got, test.code)
			}
		})
	}
}

func TestCandidateRefusesSymlink(t *testing.T) {
	store, root := newStore(t)
	secret := filepath.Join(root, "secret.md")
	if err := os.WriteFile(secret, []byte("private\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	attempt := filepath.Join(root, filepath.FromSlash(attemptDirectory))
	if err := os.MkdirAll(attempt, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.Symlink(secret, filepath.Join(attempt, "PLAN.md")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	_, err := store.ReadCandidate(t.Context(), markdownCandidate("PLAN.md"))
	if got := codeOf(t, err); got != CodeInvalidOutput {
		t.Fatalf("code = %q, want %q", got, CodeInvalidOutput)
	}
}

func TestCandidateRefusesSymlinkedParentDirectory(t *testing.T) {
	store, root := newStore(t)
	outside, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outside, "PLAN.md"), []byte("elsewhere\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	parent := filepath.Join(root, ".fleetlog", "run", "seats", "build", "1")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(parent, "out")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	_, err = store.ReadCandidate(t.Context(), markdownCandidate("PLAN.md"))
	if got := codeOf(t, err); got != CodeInvalidOutput {
		t.Fatalf("code = %q, want %q", got, CodeInvalidOutput)
	}
}

func TestValidatorRunsAfterGenericGates(t *testing.T) {
	store, root := newStore(t)
	options := CandidateOptions{
		Directory:   attemptDirectory,
		Name:        "review.json",
		MaxBytes:    MaxJSONBytes,
		RequireUTF8: true,
		Validate:    JSONObjectValidator,
	}

	writeCandidate(t, root, "review.json", "{\"verdict\":\"approved\"}\n")
	if _, err := store.ReadCandidate(t.Context(), options); err != nil {
		t.Fatalf("ReadCandidate: %v", err)
	}

	writeCandidate(t, root, "review.json", "[\"not an object\"]\n")
	if _, err := store.ReadCandidate(t.Context(), options); codeOf(t, err) != CodeInvalidOutput {
		t.Fatal("a non-object JSON candidate was accepted")
	}

	writeCandidate(t, root, "review.json", "null\n")
	if _, err := store.ReadCandidate(t.Context(), options); codeOf(t, err) != CodeInvalidOutput {
		t.Fatal("a null JSON candidate was accepted as an object")
	}

	writeCandidate(t, root, "review.json", "not json at all\n")
	if _, err := store.ReadCandidate(t.Context(), options); codeOf(t, err) != CodeInvalidOutput {
		t.Fatal("a non-JSON candidate was accepted")
	}
}

// ---------------------------------------------------------------------------
// Promotion input gates and reads
// ---------------------------------------------------------------------------

func TestPromoteRefusesUnsafeInputs(t *testing.T) {
	store, _ := newStore(t)
	valid := markdownPromotion("PLAN.md")

	cases := map[string]PromoteOptions{
		"empty name":          func() PromoteOptions { o := valid; o.Name = ""; return o }(),
		"traversal name":      func() PromoteOptions { o := valid; o.Name = "../x.md"; return o }(),
		"missing kind":        func() PromoteOptions { o := valid; o.Kind = ""; return o }(),
		"missing producer":    func() PromoteOptions { o := valid; o.Producer = Producer{}; return o }(),
		"negative attempt":    func() PromoteOptions { o := valid; o.Producer.Attempt = -1; return o }(),
		"missing extension":   func() PromoteOptions { o := valid; o.Extension = ""; return o }(),
		"unsafe extension":    func() PromoteOptions { o := valid; o.Extension = ".../x"; return o }(),
		"oversized extension": func() PromoteOptions { o := valid; o.Extension = strings.Repeat("a", 32); return o }(),
	}
	for name, options := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := store.Promote(t.Context(), []byte("body\n"), options); err == nil {
				t.Fatal("unsafe promotion input was accepted")
			}
		})
	}

	if _, err := store.Promote(t.Context(), nil, valid); codeOf(t, err) != CodeInvalidOutput {
		t.Fatal("an empty promotion was accepted")
	}
	oversize := valid
	oversize.MaxBytes = 4
	if _, err := store.Promote(t.Context(), []byte("far too long"), oversize); codeOf(t, err) != CodeInvalidOutput {
		t.Fatal("an oversized promotion was accepted")
	}
}

func TestReadPromotedRefusesPathsOutsideTheBlobStore(t *testing.T) {
	store, root := newStore(t)
	if err := os.WriteFile(filepath.Join(root, "secret.txt"), []byte("private\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	refused := []string{
		"secret.txt",
		"../secret.txt",
		".fleetlog/run/artifacts/manifest.jsonl",
		".fleetlog/run/artifacts/blobs/../../../secret.txt",
		"/etc/passwd",
	}
	for _, path := range refused {
		if _, err := store.ReadPromoted(t.Context(), path); err == nil {
			t.Errorf("ReadPromoted(%q) succeeded, want a refusal", path)
		}
	}

	missing := blobsDirectory + "/" + strings.Repeat("a", 64) + ".md"
	if _, err := store.ReadPromoted(t.Context(), missing); codeOf(t, err) != CodeNotFound {
		t.Fatal("a missing blob did not report NOT_FOUND")
	}
}

// ---------------------------------------------------------------------------
// Concurrency
// ---------------------------------------------------------------------------

func TestConcurrentPromotionsAllRecord(t *testing.T) {
	store, _ := newStore(t)

	const workers = 8
	var group sync.WaitGroup
	failures := make([]error, workers)
	for index := range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			options := markdownPromotion("PLAN.md")
			options.Producer.Attempt = index + 1
			_, failures[index] = store.Promote(
				context.Background(),
				[]byte(strings.Repeat("body ", index+1)+"\n"),
				options,
			)
		}()
	}
	group.Wait()

	for index, err := range failures {
		if err != nil {
			t.Fatalf("worker %d: %v", index, err)
		}
	}
	records, err := store.List(t.Context())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(records) != workers {
		t.Fatalf("List returned %d records, want %d — the manifest dropped an append", len(records), workers)
	}
	seen := make(map[string]bool, workers)
	for _, record := range records {
		if seen[record.ArtifactID] {
			t.Fatalf("artifact identifier %q was reused", record.ArtifactID)
		}
		seen[record.ArtifactID] = true
	}
}

func TestErrorsWithholdFileContents(t *testing.T) {
	store, root := newStore(t)
	writeCandidate(t, root, "PLAN.md", "SUPER SECRET CONTENTS\n")

	options := markdownCandidate("PLAN.md")
	options.MaxBytes = 4
	_, err := store.ReadCandidate(t.Context(), options)
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if strings.Contains(err.Error(), "SUPER SECRET") {
		t.Fatalf("the client-facing message leaked file contents: %q", err.Error())
	}
	if !errors.Is(err, ErrArtifact) {
		t.Fatal("errors.Is(err, ErrArtifact) = false")
	}
}
