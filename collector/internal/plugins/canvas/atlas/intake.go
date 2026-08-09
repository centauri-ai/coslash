package atlas

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// MaxSourceBytes bounds a run's problem statement. It is the text a human
// wrote or a ticket they exported, not a corpus; anything larger would push the
// contract and the evidence out of every assembled prompt's budget.
const MaxSourceBytes = 256 << 10

// CaptureSource reads the run's input once, at start, and records exactly what
// it read.
//
// The digest is what makes the run auditable: an operator can ask what a run
// was started from and get the bytes, not a path that has since changed. A file
// source is read here rather than at first use for the same reason.
func CaptureSource(input SourceInput) (CapturedSource, error) {
	title := strings.TrimSpace(input.Title)
	if title == "" || len(title) > 200 {
		return CapturedSource{}, policyError("source.title", "the run title must be between 1 and 200 characters")
	}

	var body []byte
	record := SourceRecord{Kind: input.Kind, Title: title}
	switch input.Kind {
	case "text":
		body = []byte(input.Text)
	case "file":
		path := strings.TrimSpace(input.Path)
		if path == "" || !filepath.IsAbs(path) {
			return CapturedSource{}, policyError("source.path", "the source path must be absolute")
		}
		if path != filepath.Clean(path) {
			return CapturedSource{}, policyError("source.path", "the source path is not canonical")
		}
		// A symlinked source would let the recorded path and the recorded bytes
		// disagree about what was actually read.
		info, err := os.Lstat(path)
		if err != nil {
			if os.IsNotExist(err) {
				return CapturedSource{}, newError(CodeNotFound, "the source file does not exist")
			}
			return CapturedSource{}, newError(CodeUnsafePath, "the source file could not be read").withCause(err)
		}
		if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return CapturedSource{}, policyError("source.path", "the source must be a regular file")
		}
		if info.Size() > MaxSourceBytes {
			return CapturedSource{}, policyError("source.path", "the source file is over the size limit")
		}
		body, err = os.ReadFile(path)
		if err != nil {
			return CapturedSource{}, newError(CodeStorageFailed, "the source file could not be read").withCause(err)
		}
		record.Path = path
	default:
		return CapturedSource{}, policyError("source.kind", "the source kind must be text or file")
	}

	if len(body) == 0 {
		return CapturedSource{}, policyError("source", "the source is empty")
	}
	if int64(len(body)) > MaxSourceBytes {
		return CapturedSource{}, policyError("source", "the source is over the size limit")
	}
	// Invalid UTF-8 cannot survive a JSON round trip unambiguously, and it
	// reaches an agent prompt, so it is refused rather than repaired.
	if !utf8.Valid(body) {
		return CapturedSource{}, policyError("source", "the source is not valid UTF-8")
	}

	digest := sha256.Sum256(body)
	record.Bytes = int64(len(body))
	record.SHA256 = hex.EncodeToString(digest[:])
	return CapturedSource{Record: record, Body: body}, nil
}
