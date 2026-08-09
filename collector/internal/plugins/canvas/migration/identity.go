package migration

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// Deterministic identifier remapping.
//
// A legacy identifier usually survives the move unchanged, and that is the
// preferred outcome: an operator who bookmarked a board id should still find
// it. Two things force a new one — an id that is not a safe path component in
// coSlash, and an id already taken by something this migration did not import.
//
// When a remap is needed it is DERIVED, never allocated. The same legacy id
// always produces the same coSlash id, so a migration interrupted after writing
// a board but before journaling it produces the same id on the next pass and
// recognizes its own work instead of creating a second copy. A random id would
// leave a duplicate behind every interruption.

// remapPrefix keeps a derived id visibly distinct from one an operator chose.
const remapPrefix = "imported-"

// digest is the shared derivation. The namespace is mixed in so the same legacy
// id used for a board and for a run does not collapse to one destination.
func digest(namespace, legacyID string) string {
	sum := sha256.Sum256([]byte(namespace + "\x00" + legacyID))
	return hex.EncodeToString(sum[:])
}

// DerivedID returns the stable coSlash identifier for a legacy one.
func DerivedID(namespace, legacyID string) string {
	return remapPrefix + digest(namespace, legacyID)[:16]
}

// DerivedRunID returns a stable identifier in the shape a run store requires.
//
// A run id is not free-form: both products match `run-<YYYYMMDDthhmmss>-<8 hex>`
// because the timestamp is what makes a directory listing chronological. So a
// remapped run cannot use the generic derived id; it gets a synthetic timestamp
// derived from the same digest, which keeps the result stable across passes
// while still parsing as a run id.
//
// The timestamp is deliberately in the past and constant per legacy id. It is
// an identifier, not a claim about when the run happened — the real timing is
// in the imported log's own events.
func DerivedRunID(legacyID string) string {
	sum := digest("run", legacyID)
	// A fixed date keeps imported runs grouped together in a listing, and the
	// time-of-day component spreads them deterministically within it.
	hour := hexByte(sum[0:2]) % 24
	minute := hexByte(sum[2:4]) % 60
	second := hexByte(sum[4:6]) % 60
	return fmt.Sprintf("run-19700101t%02d%02d%02d-%s", hour, minute, second, sum[6:14])
}

func hexByte(pair string) int {
	value := 0
	for _, character := range pair {
		value <<= 4
		switch {
		case character >= '0' && character <= '9':
			value |= int(character - '0')
		case character >= 'a' && character <= 'f':
			value |= int(character-'a') + 10
		}
	}
	return value
}

// Taken reports whether a candidate identifier is already in use.
type Taken func(candidate string) (bool, error)

// ResolveID picks the identifier a legacy item will be stored under.
//
// `derive` supplies the replacement, because the shape a destination accepts
// differs by kind: a board id is a free-form path component, a run id is not.
//
// It returns the chosen id and the reason it differs from the legacy one, empty
// when the legacy id was kept. The reason is journaled, because an item that
// changed id is the single most confusing thing a migration can do silently.
func ResolveID(
	namespace, legacyID string,
	valid func(string) bool,
	derive func(string) string,
	taken Taken,
) (string, string, error) {
	switch {
	case legacyID == "":
		return "", "", fmt.Errorf("migration: a legacy %s has no identifier", namespace)
	case !valid(legacyID):
		derived := derive(legacyID)
		if !valid(derived) {
			return "", "", fmt.Errorf("migration: no valid %s identifier could be derived from %q", namespace, legacyID)
		}
		return derived, fmt.Sprintf(
			"legacy id %q is not a valid coSlash %s identifier, so it was stored as %q",
			legacyID, namespace, derived), nil
	}

	inUse, err := taken(legacyID)
	if err != nil {
		return "", "", err
	}
	if !inUse {
		return legacyID, "", nil
	}

	// The destination is occupied by something this migration is not importing.
	// Overwriting it is not an option, and neither is dropping the legacy item.
	derived := derive(legacyID)
	derivedTaken, err := taken(derived)
	if err != nil {
		return "", "", err
	}
	if derivedTaken {
		// The derived id is a pure function of the legacy id, so this is our own
		// earlier work. Saying so keeps a resumed pass from reporting a
		// collision it actually caused itself.
		return derived, fmt.Sprintf(
			"%s %q already exists, and the derived id %q is already present too",
			namespace, legacyID, derived), nil
	}
	return derived, fmt.Sprintf(
		"a different %s already occupies id %q, so the legacy one was stored as %q",
		namespace, legacyID, derived), nil
}
