package collector

import (
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func mustSucceed(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func finalizeForTest(t *testing.T, parsed []*vendors.ParsedSession, metadata map[string]*vendors.SessionMetadata) []*vendors.ParsedSession {
	t.Helper()
	roots, err := finalizeLocalSessions(t.Context(), parsed, metadata, false)
	mustSucceed(t, err)
	return roots
}

func mustListRemote(t *testing.T, source vendors.ReadSource, collections map[string]vendors.RemoteCollection, since int64) []*session.Session {
	t.Helper()
	sessions, err := ListRemote(t.Context(), source, collections, since)
	mustSucceed(t, err)
	return sessions
}
