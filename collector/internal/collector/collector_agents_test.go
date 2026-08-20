package collector

import (
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	remoteviewv1 "github.com/centauri-ai/coslash/collector/remoteview/v1"
)

func TestListAgentsSkipsOpenCode(t *testing.T) {
	original := vendorSources
	t.Cleanup(func() { vendorSources = original })

	var collected []string
	vendorSources = []vendorSource{
		{
			name: vendors.AgentClaude,
			collect: func(int64) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error) {
				t.Fatal("unlimited collect used for scoped listing")
				return nil, nil, nil
			},
			collectLimited: func(int64, int) ([]*vendors.ParsedSession, *vendors.SessionMetadata, bool, error) {
				collected = append(collected, vendors.AgentClaude)
				return []*vendors.ParsedSession{{
					Session: &session.Session{
						Agent: vendors.AgentClaude, ID: "c1", StartedAt: 100, LastActivityTime: 100,
						Tokens: map[string]session.ModelTokens{}, SessionDetails: session.SessionDetails{Turns: 1},
					},
				}}, vendors.EmptySessionMetadata(), false, nil
			},
		},
		{
			name: vendors.AgentCodex,
			collectLimited: func(int64, int) ([]*vendors.ParsedSession, *vendors.SessionMetadata, bool, error) {
				collected = append(collected, vendors.AgentCodex)
				return nil, vendors.EmptySessionMetadata(), false, nil
			},
		},
		{
			name: vendors.AgentOpenCode,
			collect: func(int64) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error) {
				collected = append(collected, vendors.AgentOpenCode)
				return nil, vendors.EmptySessionMetadata(), nil
			},
			collectLimited: func(int64, int) ([]*vendors.ParsedSession, *vendors.SessionMetadata, bool, error) {
				t.Fatal("OpenCode limited collect must not run for remote agents")
				return nil, nil, false, nil
			},
		},
	}

	result, err := ListAgents(0, []string{vendors.AgentClaude, vendors.AgentCodex}, remoteviewv1.MaxSessionsPerAgent)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sessions) != 1 || result.Sessions[0].ID != "c1" {
		t.Fatalf("sessions = %#v", result.Sessions)
	}
	for _, name := range collected {
		if name == vendors.AgentOpenCode {
			t.Fatal("OpenCode was collected during ListAgents")
		}
	}
}

func TestListStillCollectsOpenCode(t *testing.T) {
	original := vendorSources
	t.Cleanup(func() { vendorSources = original })

	opencodeSeen := false
	vendorSources = []vendorSource{
		{
			name: vendors.AgentClaude,
			collect: func(int64) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error) {
				return nil, vendors.EmptySessionMetadata(), nil
			},
		},
		{
			name: vendors.AgentCodex,
			collect: func(int64) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error) {
				return nil, vendors.EmptySessionMetadata(), nil
			},
		},
		{
			name: vendors.AgentOpenCode,
			collect: func(int64) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error) {
				opencodeSeen = true
				return []*vendors.ParsedSession{{
					Session: &session.Session{
						Agent: vendors.AgentOpenCode, ID: "o1", StartedAt: 100, LastActivityTime: 100,
						Tokens: map[string]session.ModelTokens{}, SessionDetails: session.SessionDetails{Turns: 1},
					},
				}}, vendors.EmptySessionMetadata(), nil
			},
		},
	}

	sessions, err := List(0)
	if err != nil {
		t.Fatal(err)
	}
	if !opencodeSeen {
		t.Fatal("List did not collect OpenCode")
	}
	if len(sessions) != 1 || sessions[0].Agent != vendors.AgentOpenCode {
		t.Fatalf("sessions = %#v", sessions)
	}
}

func TestGetSessionFactsByAgentUsesExactVendor(t *testing.T) {
	original := vendorSources
	t.Cleanup(func() { vendorSources = original })

	vendorSources = []vendorSource{
		{
			name: vendors.AgentClaude,
			loadFacts: func(id string) (*vendors.ParsedSession, error) {
				if id != "shared" {
					return nil, nil
				}
				return &vendors.ParsedSession{Session: &session.Session{
					Agent: vendors.AgentClaude, ID: "shared", StartedAt: 100, LastActivityTime: 100,
					Tokens: map[string]session.ModelTokens{},
				}}, nil
			},
		},
		{
			name: vendors.AgentCodex,
			loadFacts: func(id string) (*vendors.ParsedSession, error) {
				if id != "shared" {
					return nil, nil
				}
				return &vendors.ParsedSession{Session: &session.Session{
					Agent: vendors.AgentCodex, ID: "shared", StartedAt: 200, LastActivityTime: 200,
					Tokens: map[string]session.ModelTokens{},
				}}, nil
			},
		},
	}

	got, err := GetSessionFactsByAgent(vendors.AgentCodex, "shared")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Agent != vendors.AgentCodex || got.StartedAt != 200 {
		t.Fatalf("got = %#v", got)
	}
}

func TestListAgentsReportsDiscoveryTruncation(t *testing.T) {
	original := vendorSources
	t.Cleanup(func() { vendorSources = original })
	vendorSources = []vendorSource{{
		name: vendors.AgentClaude,
		collectLimited: func(_ int64, maxRoots int) ([]*vendors.ParsedSession, *vendors.SessionMetadata, bool, error) {
			if maxRoots != remoteviewv1.MaxSessionsPerAgent {
				t.Fatalf("maxRoots = %d", maxRoots)
			}
			return nil, vendors.EmptySessionMetadata(), true, nil
		},
	}}
	result, err := ListAgents(0, []string{vendors.AgentClaude}, remoteviewv1.MaxSessionsPerAgent)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Truncated {
		t.Fatal("expected discovery truncation")
	}
}
