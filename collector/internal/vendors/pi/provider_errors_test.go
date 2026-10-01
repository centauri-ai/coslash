package pi

import (
	"fmt"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

func TestProviderErrorSelectedAncestry(t *testing.T) {
	failure := `{"type":"message","id":"failed","parentId":"u","message":{"role":"assistant","content":[],"stopReason":"error","errorMessage":"Refresh AWS SSO credentials"}}`
	root := userRow("u", "", "goal", "")
	for _, tc := range []struct {
		name, row string
		wantError bool
	}{
		{"pending retry", userRow("next", "failed", "retry", ""), true},
		{"cancelled partial", `{"type":"message","id":"next","parentId":"failed","message":{"role":"assistant","content":"partial answer","stopReason":"aborted"}}`, true},
		{"branch summary", `{"type":"branch_summary","id":"next","parentId":"failed","summary":"retained history"}`, true},
		{"success", `{"type":"message","id":"next","parentId":"failed","message":{"role":"assistant","content":"done","stopReason":"stop"}}`, false},
		{"length", `{"type":"message","id":"next","parentId":"failed","message":{"role":"assistant","content":"done","stopReason":"length"}}`, false},
		{"tool success", `{"type":"message","id":"next","parentId":"failed","message":{"role":"assistant","content":[],"stopReason":"toolUse"}}`, false},
		{"other branch", `{"type":"message","id":"next","parentId":"u","message":{"role":"assistant","content":"other branch"}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := projectionFixture(t, tc.name, "", root, failure, tc.row)
			p, err := projectLeaf(tr, nil, "next", false)
			if err != nil {
				t.Fatal(err)
			}
			if (p.Session.AgentError != nil) != tc.wantError {
				t.Fatalf("error state: %v", p.Session.AgentError)
			}
			if tc.wantError && *p.Session.AgentError != "Refresh AWS SSO credentials" {
				t.Fatal("provider diagnostic lost")
			}
			for _, row := range p.Session.Digest {
				if strings.Contains(row.Description, "Refresh AWS") {
					t.Fatal("provider failure became conversation digest")
				}
			}
		})
	}
	tr := projectionFixture(t, "failure", "", root, failure)
	p, err := projectLeaf(tr, nil, "failed", false)
	if err != nil || p.Session.AgentError == nil || p.Session.Errors != 1 {
		t.Fatalf("empty-content failure lost: %#v, %v", p, err)
	}
	clone := session.Clone(p.Session)
	*clone.AgentError = "changed"
	if *p.Session.AgentError != "Refresh AWS SSO credentials" {
		t.Fatal("clone aliases provider error")
	}
}

func TestProviderErrorDiagnosticBounds(t *testing.T) {
	for _, diagnostic := range []string{"", strings.Repeat("x", 5000)} {
		row := fmt.Sprintf(`{"type":"message","id":"failed","message":{"role":"assistant","content":[],"stopReason":"error","errorMessage":%q}}`, diagnostic)
		tr := projectionFixture(t, "bounded", "", row)
		p, err := projectLeaf(tr, nil, "failed", false)
		if err != nil || p.Session.AgentError == nil || len(*p.Session.AgentError) == 0 || len(*p.Session.AgentError) > 4100 {
			t.Fatalf("unbounded/absent diagnostic: %#v, %v", p, err)
		}
	}
}
