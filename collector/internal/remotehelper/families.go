package remotehelper

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

// family is one replacement unit: the files whose facts compose one card, the
// sessions those files carry, and the fingerprint that decides whether the Mac
// already holds its facts.
type family struct {
	id           string
	files        []string
	sessionIDs   []string
	fingerprints []vendors.FileFingerprint
	fingerprint  string
	inWindow     bool
	skipReason   string
}

// scan is one vendor's enumeration result. Complete is the deletion gate: it is
// true only when the whole allowlisted tree was listed without a skip, a limit,
// or a missing-root surprise, because absence can only be proven by a scan that
// saw everything.
type scan struct {
	families       map[string]*family
	order          []string
	candidateFiles int
	selectedFiles  int
	complete       bool
	incompleteWhy  string
}

func newScan() *scan {
	return &scan{families: map[string]*family{}}
}

func (s *scan) family(id string) *family {
	existing, ok := s.families[id]
	if ok {
		return existing
	}
	created := &family{id: id}
	s.families[id] = created
	s.order = append(s.order, id)
	return created
}

// add records one discovered file against its family. Fingerprints stay sorted
// and unique by key so the aggregate is order-independent.
func (s *scan) add(
	familyID, sessionID string,
	fingerprint vendors.FileFingerprint,
	file string,
	selected bool,
) {
	item := s.family(familyID)
	item.files = append(item.files, file)
	if sessionID != "" {
		item.sessionIDs = append(item.sessionIDs, sessionID)
	}
	item.fingerprints = append(item.fingerprints, fingerprint)
	item.inWindow = item.inWindow || selected
}

// finish computes each family's aggregate fingerprint. Metadata facts join the
// hash so a session that only changed liveness or name still recollects, which
// a file-only fingerprint would report as unchanged forever.
func (s *scan) finish(metadata *vendors.SessionMetadata) {
	for _, item := range s.families {
		sort.Strings(item.files)
		sort.Strings(item.sessionIDs)
		sort.Slice(item.fingerprints, func(i, j int) bool {
			return item.fingerprints[i].Key < item.fingerprints[j].Key
		})
		item.fingerprints = dedupeFingerprints(item.fingerprints)
		item.sessionIDs = dedupeStrings(item.sessionIDs)
		item.fingerprint = aggregateFingerprint(item, metadata)
	}
}

func (s *scan) sortedFamilies() []*family {
	result := make([]*family, 0, len(s.families))
	for _, id := range s.order {
		result = append(result, s.families[id])
	}
	sort.Slice(result, func(i, j int) bool { return result[i].id < result[j].id })
	return result
}

// inventory is the bounded authoritative family list a complete scan can prove.
func (s *scan) inventory(limit int) ([]string, bool) {
	ids := make([]string, 0, len(s.families))
	for id := range s.families {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if !s.complete || len(ids) > limit {
		return nil, false
	}
	return ids, true
}

// aggregateFingerprint is comparison data only. It is a digest, never a path,
// and the helper never resolves one that arrives in a request.
func aggregateFingerprint(item *family, metadata *vendors.SessionMetadata) string {
	digest := sha256.New()
	fmt.Fprintf(digest, "v1\n%s\n%s\n", vendors.ParserVersion, item.id)
	for _, fingerprint := range item.fingerprints {
		fmt.Fprintf(
			digest, "f\t%s\t%s\t%s\n", fingerprint.Key,
			strconv.FormatInt(fingerprint.Size, 10),
			strconv.FormatInt(fingerprint.ModifiedAtMs, 10),
		)
	}
	if metadata != nil {
		for _, id := range item.sessionIDs {
			if name, ok := metadata.Names[id]; ok {
				fmt.Fprintf(digest, "n\t%s\t%s\n", id, name)
			}
			if status, ok := metadata.Live[id]; ok {
				fmt.Fprintf(digest, "l\t%s\t%s\n", id, status)
			}
		}
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func dedupeFingerprints(items []vendors.FileFingerprint) []vendors.FileFingerprint {
	result := items[:0]
	previous := ""
	for _, item := range items {
		if item.Key == previous {
			continue
		}
		previous = item.Key
		result = append(result, item)
	}
	return result
}

func dedupeStrings(items []string) []string {
	result := items[:0]
	previous := ""
	for index, item := range items {
		if index > 0 && item == previous {
			continue
		}
		previous = item
		result = append(result, item)
	}
	return result
}
