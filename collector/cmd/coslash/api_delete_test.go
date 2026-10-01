package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/centauri-ai/coslash/collector/internal/collector"
	"github.com/centauri-ai/coslash/collector/internal/remote"
	reviewpkg "github.com/centauri-ai/coslash/collector/internal/review"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/synthesis"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/httpsec"
	"github.com/centauri-ai/coslash/collector/internal/vendors/claude"
	"github.com/centauri-ai/coslash/collector/internal/vendors/codex"
	"github.com/centauri-ai/coslash/collector/internal/vendors/cursor"
	"github.com/centauri-ai/coslash/collector/internal/vendors/opencode"
)

const apiDeleteID = "12345678-1234-4234-8234-123456789abc"

func guardedDelete(callback func(context.Context, string, string) error) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/sessions", newDeleteSessionHandler(callback))
	return (httpsec.Guard{Addr: "127.0.0.1:8787", Token: "test-token"}).Wrap(mux)
}

func deleteRequest(ctx context.Context, query string) *http.Request {
	r := httptest.NewRequest(http.MethodDelete, "http://127.0.0.1:8787/api/sessions?"+query, nil).WithContext(ctx)
	r.Header.Set("X-Coslash-Token", "test-token")
	return r
}

func TestDeleteSessionRejectsBeforeAdapter(t *testing.T) {
	query := "source=local&agent=claude&id=" + apiDeleteID
	for _, tc := range []struct {
		name, query, token, origin, method string
		status                             int
		code                               string
	}{
		{"token", query, "", "", "DELETE", 401, ""},
		{"origin", query, "test-token", "https://evil.example", "DELETE", 403, ""},
		{"method", query, "test-token", "", "POST", 405, ""},
		{"missing source", "agent=claude&id=" + apiDeleteID, "test-token", "", "DELETE", 400, "invalid_session"},
		{"empty source", "source=&agent=claude&id=" + apiDeleteID, "test-token", "", "DELETE", 400, "invalid_session"},
		{"invalid source", "source=bad&agent=claude&id=" + apiDeleteID, "test-token", "", "DELETE", 400, "invalid_session"},
		{"duplicate source", query + "&source=local", "test-token", "", "DELETE", 400, "invalid_session"},
		{"duplicate agent", query + "&agent=codex", "test-token", "", "DELETE", 400, "invalid_session"},
		{"duplicate id", query + "&id=" + apiDeleteID, "test-token", "", "DELETE", 400, "invalid_session"},
		{"missing agent", "source=local&id=" + apiDeleteID, "test-token", "", "DELETE", 400, "invalid_session"},
		{"unknown agent", "source=local&agent=other&id=" + apiDeleteID, "test-token", "", "DELETE", 400, "invalid_session"},
		{"empty id", "source=local&agent=claude&id=", "test-token", "", "DELETE", 400, "invalid_session"},
		{"path id", "source=local&agent=claude&id=../" + apiDeleteID, "test-token", "", "DELETE", 400, "invalid_session"},
		{"malformed query", query + "&ignored=%zz", "test-token", "", "DELETE", 400, "invalid_session"},
		{"cursor version", "source=local&agent=cursor&id=12345678-1234-7234-8234-123456789abc", "test-token", "", "DELETE", 400, "invalid_session"},
		{"opencode UUID", "source=local&agent=opencode&id=" + apiDeleteID, "test-token", "", "DELETE", 400, "invalid_session"},
		{"remote", "source=r_123456789abcdef0&agent=claude&id=" + apiDeleteID, "test-token", "", "DELETE", 409, "remote_action_unsupported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			handler := guardedDelete(func(context.Context, string, string) error { calls++; return nil })
			r := deleteRequest(context.Background(), tc.query)
			r.Method = tc.method
			r.Header.Set("X-Coslash-Token", tc.token)
			r.Header.Set("Origin", tc.origin)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			assertDeleteResponse(t, w, tc.status, tc.code)
			if calls != 0 {
				t.Fatalf("adapter called %d times", calls)
			}
		})
	}
}

func assertDeleteResponse(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if code == "" {
		if status == 204 && w.Body.Len() != 0 {
			t.Fatalf("204 has body: %s", w.Body.String())
		}
		return
	}
	var body apiErrorBody
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Code != code || strings.Contains(body.Error, "private path") || body.Error == "" {
		t.Fatalf("body=%s error=%v", w.Body.String(), err)
	}
}

func TestDeleteSessionStatusAndIdentity(t *testing.T) {
	for _, vendor := range []struct {
		agent, id                                    string
		missing, active, unverified, failed, invalid error
	}{
		{"claude", apiDeleteID, claude.ErrSessionMissing, claude.ErrSessionActive, claude.ErrSessionUnverified, claude.ErrSessionFailed, claude.ErrSessionInvalid},
		{"codex", apiDeleteID, codex.ErrSessionMissing, codex.ErrSessionActive, codex.ErrSessionUnverified, codex.ErrSessionDeleteFailed, codex.ErrSessionInvalid},
		{"cursor", apiDeleteID, cursor.ErrDeleteMissing, cursor.ErrDeleteActive, cursor.ErrDeleteUnverified, cursor.ErrDeleteFailed, cursor.ErrDeleteInvalid},
		{"opencode", "ses_target", opencode.ErrSessionMissing, opencode.ErrSessionActive, opencode.ErrSessionUnverified, opencode.ErrSessionDeleteFailed, opencode.ErrInvalidSession},
	} {
		t.Run(vendor.agent+"/remote same ID", func(t *testing.T) {
			handler := guardedDelete(func(context.Context, string, string) error { t.Fatal("remote reached adapter"); return nil })
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, deleteRequest(context.Background(), "source=r_123456789abcdef0&agent="+vendor.agent+"&id="+vendor.id))
			assertDeleteResponse(t, w, 409, "remote_action_unsupported")
		})
		for _, tc := range []struct {
			name   string
			err    error
			status int
			code   string
		}{
			{"success", nil, 204, ""}, {"missing", vendor.missing, 404, "session_missing"},
			{"active after listing", vendor.active, 409, "session_active"}, {"unverified", vendor.unverified, 409, "session_unverified"},
			{"failed", vendor.failed, 500, "session_delete_failed"}, {"invalid", vendor.invalid, 400, "invalid_session"},
			{"unknown", errors.New("private path"), 500, "session_delete_failed"},
			{"partial", errors.Join(vendor.missing, vendor.unverified, vendor.failed), 500, "session_delete_failed"},
		} {
			t.Run(vendor.agent+"/"+tc.name, func(t *testing.T) {
				calls := 0
				handler := guardedDelete(func(ctx context.Context, agent, id string) error {
					calls++
					if agent != vendor.agent || id != vendor.id || ctx.Err() != nil {
						t.Fatal("identity/context changed")
					}
					if tc.err == nil {
						return nil
					}
					return fmt.Errorf("private path: %w", tc.err)
				})
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, deleteRequest(context.Background(), "source=local&agent="+vendor.agent+"&id="+vendor.id))
				assertDeleteResponse(t, w, tc.status, tc.code)
				if calls != 1 {
					t.Fatalf("adapter calls=%d", calls)
				}
			})
		}
	}
}

func TestDeleteSessionCancellationAndSerialization(t *testing.T) {
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	calls := 0
	handler := guardedDelete(func(ctx context.Context, _, _ string) error {
		calls++
		close(entered)
		<-release
		return nil
	})
	query := "source=local&agent=claude&id=" + apiDeleteID
	go func() {
		defer close(done)
		handler.ServeHTTP(httptest.NewRecorder(), deleteRequest(context.Background(), query))
	}()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	waiting := &deleteWaitingContext{Context: ctx, waiting: make(chan struct{})}
	w := httptest.NewRecorder()
	waitingDone := make(chan struct{})
	go func() { defer close(waitingDone); handler.ServeHTTP(w, deleteRequest(waiting, query)) }()
	<-waiting.waiting
	cancel()
	<-waitingDone
	assertDeleteResponse(t, w, 500, "session_delete_failed")
	if calls != 1 {
		t.Fatalf("waiting cancellation: calls=%d", calls)
	}
	close(release)
	<-done
	ctx, cancel = context.WithCancel(context.Background())
	callbackEntered, callbackDone := make(chan struct{}), make(chan struct{})
	handler = guardedDelete(func(ctx context.Context, _, _ string) error { close(callbackEntered); <-ctx.Done(); return ctx.Err() })
	go func() {
		defer close(callbackDone)
		handler.ServeHTTP(httptest.NewRecorder(), deleteRequest(ctx, query))
	}()
	<-callbackEntered
	cancel()
	<-callbackDone
}

type deleteWaitingContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (ctx *deleteWaitingContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.waiting) })
	return ctx.Context.Done()
}

func closedAPIDeleteFixture(t *testing.T) (string, string, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Unix fixture process command")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("COSLASH_HOME", filepath.Join(home, ".coslash"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "ps"), []byte("#!/bin/sh\nprintf '1 init init\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	project := filepath.Join(home, ".claude", "projects", "fixture")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	neighbor := "12345678-1234-4234-8234-123456789abd"
	for _, id := range []string{apiDeleteID, neighbor} {
		body := `{"sessionId":"` + id + `","type":"user","uuid":"row-` + id + `","timestamp":"2026-01-01T00:00:00Z","message":{"content":"disposable"}}` + "\n"
		if err := os.WriteFile(filepath.Join(project, id+".jsonl"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return home, filepath.Join(project, apiDeleteID+".jsonl"), filepath.Join(project, neighbor+".jsonl")
}

func TestDeleteSessionRealFixtureAndFreshReads(t *testing.T) {
	_, root, neighbor := closedAPIDeleteFixture(t)
	before, err := os.ReadFile(neighbor)
	if err != nil {
		t.Fatal(err)
	}
	found, err := collector.GetSessionDetail("claude", apiDeleteID)
	if err != nil || found == nil {
		t.Fatalf("fixture detail before: %v %v", found, err)
	}
	if err := synthesis.NewCache().Store("claude", apiDeleteID, synthesis.Record{Revision: found.LastActivityTime, Synthesis: session.SessionSynthesis{}}); err != nil {
		t.Fatal(err)
	}
	manager := synthesis.NewManager(nil)
	if manager.LookupLatest("claude", apiDeleteID) == nil {
		t.Fatal("synthesis fixture not cached")
	}
	reviews := reviewpkg.NewManager(nil)
	handler := (httpsec.Guard{Addr: "127.0.0.1:8787", Token: "test-token"}).Wrap(routes(manager, reviews, settings.Open(), remote.NewManager(remote.Options{}), nil))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, deleteRequest(context.Background(), "source=local&agent=claude&id="+apiDeleteID))
	assertDeleteResponse(t, w, 204, "")
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("root survived: %v", err)
	}
	after, err := os.ReadFile(neighbor)
	if err != nil || string(after) != string(before) {
		t.Fatalf("neighbor changed: %v", err)
	}
	found, err = collector.GetSessionDetail("claude", apiDeleteID)
	if err != nil || found != nil {
		t.Fatalf("deleted detail: %v %v", found, err)
	}
	list, err := collector.List(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	kept := false
	for _, s := range list {
		if s.Agent == "claude" && s.ID == apiDeleteID {
			t.Fatal("deleted session listed")
		}
		if s.Agent == "claude" && strings.HasSuffix(neighbor, s.ID+".jsonl") {
			kept = true
		}
	}
	if !kept {
		t.Fatal("neighbor not listed")
	}
	for _, path := range []string{
		"/api/session-detail?source=local&agent=claude&session=" + apiDeleteID + "&revision=latest",
		"/api/synthesis?source=local&agent=claude&id=" + apiDeleteID,
		"/api/reviews?source=local&agent=claude&id=" + apiDeleteID,
	} {
		r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8787"+path, nil)
		r.Header.Set("X-Coslash-Token", "test-token")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 404 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
}

func TestDeleteSessionPartialRetryWithoutTranscript(t *testing.T) {
	home, root, _ := closedAPIDeleteFixture(t)
	residue := filepath.Join(home, ".claude", "session-env", apiDeleteID, "environment")
	if err := os.MkdirAll(filepath.Dir(residue), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(residue, []byte("disposable"), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	handler := guardedDelete(func(ctx context.Context, agent, id string) error {
		calls++
		if calls == 1 {
			if err := os.Remove(root); err != nil {
				t.Fatal(err)
			}
			return errors.Join(claude.ErrSessionFailed, claude.ErrSessionMissing)
		}
		return deleteLocalSession(ctx, agent, id)
	})
	query := "source=local&agent=claude&id=" + apiDeleteID
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, deleteRequest(context.Background(), query))
	assertDeleteResponse(t, w, 500, "session_delete_failed")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, deleteRequest(context.Background(), query))
	assertDeleteResponse(t, w, 204, "")
	if calls != 2 {
		t.Fatalf("retry adapter calls=%d", calls)
	}
	if _, err := os.Stat(residue); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("residue survived: %v", err)
	}
}
