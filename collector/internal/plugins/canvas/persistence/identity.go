package persistence

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/contracts"
)

const (
	maxIdentityFieldBytes = 256
	documentExtension     = ".json"
	documentDirectory     = "workspaces"
	indexName             = documentDirectory + "/index" + documentExtension
)

// ValidateSession rejects identities that cannot be stored or round-tripped.
//
// Storage never uses the raw values as path components, so this check exists to
// reject junk early rather than to make a path safe. Control characters and
// invalid UTF-8 are refused because they cannot survive a JSON round trip
// unambiguously.
func ValidateSession(session contracts.SessionIdentity) error {
	if err := validateIdentityField("agent", session.Agent); err != nil {
		return err
	}
	return validateIdentityField("id", session.ID)
}

func validateIdentityField(field, value string) error {
	if value == "" {
		return fmt.Errorf("%w: %s is empty", ErrInvalidSession, field)
	}
	if len(value) > maxIdentityFieldBytes {
		return fmt.Errorf("%w: %s is too long", ErrInvalidSession, field)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("%w: %s is not valid UTF-8", ErrInvalidSession, field)
	}
	if strings.ContainsFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return fmt.Errorf("%w: %s contains control characters", ErrInvalidSession, field)
	}
	return nil
}

// documentName returns the scoped file name for a session.
//
// The name is a digest of the exact identity rather than the identity itself.
// That keeps user-controlled text out of the filesystem entirely, so no agent
// or session ID can collide through case-insensitive comparison or Unicode
// normalization on macOS, and no value can act as a path component. The exact
// identity is preserved inside the document and the index.
func documentName(session contracts.SessionIdentity) string {
	digest := sha256.Sum256([]byte(session.Agent + "\x00" + session.ID))
	return documentDirectory + "/" + hex.EncodeToString(digest[:]) + documentExtension
}
