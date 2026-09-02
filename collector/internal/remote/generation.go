package remote

import (
	"io"
	"io/fs"
	"log"

	"github.com/centauri-ai/coslash/collector/internal/collector"
	"github.com/centauri-ai/coslash/collector/internal/remoteprotocol"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/codex"
)

// toGeneration turns a durable v2 snapshot into the in-memory shape the pure
// remoteprotocol accumulator operates on.
func toGeneration(cached CachedSnapshotV2) remoteprotocol.Generation {
	gen := remoteprotocol.Generation{
		BaselineID: cached.BaselineID, CoverageSinceMs: cached.CoverageSinceMs,
		Families: make(map[remoteprotocol.FamilyKey]remoteprotocol.CachedFamily, len(cached.Families)),
	}
	for _, family := range cached.Families {
		key := remoteprotocol.FamilyKey{Vendor: family.Vendor, FamilyID: family.FamilyID}
		gen.Families[key] = remoteprotocol.CachedFamily{
			Facts: family.Facts, Fingerprint: family.Fingerprint, StaleReason: family.StaleReason,
			LastSuccessAtMs: family.LastSuccessAtMs,
		}
	}
	return gen
}

// fromGeneration turns a proposed accumulator generation back into the
// durable v2 shape, alongside the coverage and Codex header data collected
// for this refresh.
func fromGeneration(
	gen remoteprotocol.Generation,
	coverage []AgentCoverage,
	fetchedAtMs, roundTripMs int64,
	codexHeaders []CachedCodexHeader,
) CachedSnapshotV2 {
	snapshot := CachedSnapshotV2{
		Version: cacheV2Version, BaselineID: gen.BaselineID, CoverageSinceMs: gen.CoverageSinceMs,
		Coverage:    coverage,
		FetchedAtMs: fetchedAtMs, RoundTripMs: roundTripMs, CodexHeaders: codexHeaders,
	}
	for key, family := range gen.Families {
		snapshot.Families = append(snapshot.Families, CachedFamilyV2{
			Vendor: key.Vendor, FamilyID: key.FamilyID,
			Facts: family.Facts, Fingerprint: family.Fingerprint, StaleReason: family.StaleReason,
			LastSuccessAtMs: family.LastSuccessAtMs,
		})
	}
	return snapshot
}

func mergeMetadataInto(dst *vendors.SessionMetadata, src *vendors.SessionMetadata) {
	if dst == nil || src == nil {
		return
	}
	for id, name := range src.Names {
		dst.Names[id] = name
	}
	for id, status := range src.Live {
		dst.Live[id] = status
	}
}

// composeFromGeneration reconstructs displayable sessions from cached family
// facts without reopening any remote file. freshMetadata, when non-nil for a
// vendor, wins over that vendor's cached per-family metadata fragments since
// liveness in particular can go stale for a family whose transcript fingerprint
// did not change.
func composeFromGeneration(
	gen remoteprotocol.Generation,
	source vendors.ReadSource,
	freshMetadata map[string]*vendors.SessionMetadata,
	since int64,
) []*session.Session {
	parsedByVendor := map[string][]*vendors.ParsedSession{
		vendors.AgentClaude: {}, vendors.AgentCodex: {},
	}
	cachedMetaByVendor := map[string]*vendors.SessionMetadata{
		vendors.AgentClaude: vendors.EmptySessionMetadata(), vendors.AgentCodex: vendors.EmptySessionMetadata(),
	}
	for key, family := range gen.Families {
		parsed, meta, err := family.Facts.Parsed()
		if err != nil {
			log.Printf("remote cache: dropping unreconstructable %s/%s family from display: %v", key.Vendor, key.FamilyID, err)
			continue
		}
		parsedByVendor[key.Vendor] = append(parsedByVendor[key.Vendor], parsed...)
		mergeMetadataInto(cachedMetaByVendor[key.Vendor], meta)
	}
	collections := map[string]vendors.RemoteCollection{}
	for _, vendor := range []string{vendors.AgentClaude, vendors.AgentCodex} {
		metadata := cachedMetaByVendor[vendor]
		if freshMetadata != nil && freshMetadata[vendor] != nil {
			metadata = freshMetadata[vendor]
		}
		collections[vendor] = vendors.RemoteCollection{Sessions: parsedByVendor[vendor], Metadata: metadata}
	}
	return collector.ListRemote(source, collections, since)
}

// nullReadSource treats every path as absent. It composes a snapshot loaded
// from disk before any live connection exists, so optional local-file
// enrichment (such as Claude workflow lookups) degrades the same way it would
// for genuinely missing files.
type nullReadSource struct{}

func (nullReadSource) Open(string) (io.ReadCloser, error)    { return nil, fs.ErrNotExist }
func (nullReadSource) ReadDir(string) ([]fs.DirEntry, error) { return nil, fs.ErrNotExist }
func (nullReadSource) Stat(string) (fs.FileInfo, error)      { return nil, fs.ErrNotExist }

var _ vendors.ReadSource = nullReadSource{}

func codexHeaderCacheFrom(entries []CachedCodexHeader) map[string]codex.CachedHeader {
	result := make(map[string]codex.CachedHeader, len(entries))
	for _, entry := range entries {
		if entry.ParserVersion != codexParserVersion {
			continue
		}
		result[entry.Key] = codex.CachedHeader{
			Fingerprint: vendors.FileFingerprint{Key: entry.Key, Size: entry.Size, ModifiedAtMs: entry.ModifiedAtMs},
			SessionID:   entry.SessionID, ParentID: entry.ParentID,
		}
	}
	return result
}

func codexHeaderCacheTo(cache map[string]codex.CachedHeader) []CachedCodexHeader {
	entries := make([]CachedCodexHeader, 0, len(cache))
	for key, entry := range cache {
		entries = append(entries, CachedCodexHeader{
			Key: key, Size: entry.Fingerprint.Size, ModifiedAtMs: entry.Fingerprint.ModifiedAtMs,
			ParserVersion: codexParserVersion, SessionID: entry.SessionID, ParentID: entry.ParentID,
		})
	}
	return entries
}

func snapshotOrEmpty(snapshot *CachedSnapshotV2) CachedSnapshotV2 {
	if snapshot == nil {
		return CachedSnapshotV2{Version: cacheV2Version}
	}
	return *snapshot
}

func baselineFamilies(snapshot CachedSnapshotV2, vendor string) map[string]CachedFamilyV2 {
	result := map[string]CachedFamilyV2{}
	for _, family := range snapshot.Families {
		if family.Vendor == vendor {
			result[family.FamilyID] = family
		}
	}
	return result
}

func knownFamiliesFor(snapshot CachedSnapshotV2) []remoteprotocol.KnownFamily {
	known := make([]remoteprotocol.KnownFamily, 0, len(snapshot.Families))
	for _, family := range snapshot.Families {
		known = append(known, remoteprotocol.KnownFamily{
			Vendor: family.Vendor, FamilyID: family.FamilyID, Fingerprint: family.Fingerprint,
		})
	}
	return known
}
