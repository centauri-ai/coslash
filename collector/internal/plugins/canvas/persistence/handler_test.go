package persistence

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/contracts"
)

func request(t *testing.T, handler http.Handler, method, target string, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequestWithContext(t.Context(), method, target, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func decodeFailure(t *testing.T, recorder *httptest.ResponseRecorder) contracts.ErrorResponse {
	t.Helper()
	var failure contracts.ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &failure); err != nil {
		t.Fatalf("decode failure body %q: %v", recorder.Body.String(), err)
	}
	if failure.OK {
		t.Fatal("failure body reported ok=true")
	}
	return failure
}

func workspaceURL(session contracts.SessionIdentity) string {
	return workspaceRoutePrefix + url.PathEscape(session.Agent) + "/" + url.PathEscape(session.ID)
}

func TestHandlerLoadAndSaveRoundTrip(t *testing.T) {
	t.Parallel()
	store, _ := newStore(t, Options{})
	handler := store.Handler()
	target := workspaceURL(testSession())

	recorder := request(t, handler, http.MethodGet, target, "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200: %s", recorder.Code, recorder.Body)
	}
	var empty contracts.WorkspaceDocument
	if err := json.Unmarshal(recorder.Body.Bytes(), &empty); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if empty.Revision != 0 || string(empty.State) != "null" {
		t.Fatalf("empty document = %+v, want revision 0 and null state", empty)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if got := recorder.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}

	recorder = request(t, handler, http.MethodPut, target,
		`{"schemaVersion":1,"expectedRevision":0,"state":{"pinIds":["goal"]}}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200: %s", recorder.Code, recorder.Body)
	}
	var saved contracts.WorkspaceDocument
	if err := json.Unmarshal(recorder.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if saved.Revision != 1 {
		t.Fatalf("revision = %d, want 1", saved.Revision)
	}
	if saved.Session != testSession() {
		t.Fatalf("session = %+v, want %+v", saved.Session, testSession())
	}

	recorder = request(t, handler, http.MethodGet, target, "")
	if err := json.Unmarshal(recorder.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(saved.State) != `{"pinIds":["goal"]}` {
		t.Fatalf("state = %s", saved.State)
	}
}

func TestHandlerConflictCarriesActualRevision(t *testing.T) {
	t.Parallel()
	store, _ := newStore(t, Options{})
	handler := store.Handler()
	target := workspaceURL(testSession())

	request(t, handler, http.MethodPut, target, `{"schemaVersion":1,"expectedRevision":0,"state":{"a":1}}`)
	request(t, handler, http.MethodPut, target, `{"schemaVersion":1,"expectedRevision":1,"state":{"a":2}}`)

	recorder := request(t, handler, http.MethodPut, target, `{"schemaVersion":1,"expectedRevision":1,"state":{"a":3}}`)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", recorder.Code, recorder.Body)
	}
	failure := decodeFailure(t, recorder)
	if failure.Code != CodeRevisionConflict {
		t.Fatalf("code = %s, want %s", failure.Code, CodeRevisionConflict)
	}
	if failure.ActualRevision == nil || *failure.ActualRevision != 2 {
		t.Fatalf("actualRevision = %v, want 2", failure.ActualRevision)
	}
	if failure.Field != "expectedRevision" {
		t.Fatalf("field = %q, want expectedRevision", failure.Field)
	}
}

func TestHandlerRejectsInvalidRequests(t *testing.T) {
	t.Parallel()
	store, _ := newStore(t, Options{MaxStateBytes: 256})
	handler := store.Handler()
	target := workspaceURL(testSession())

	cases := []struct {
		name   string
		method string
		target string
		body   string
		status int
		code   string
	}{
		{"unsupportedMethod", http.MethodDelete, target, "", http.StatusMethodNotAllowed, CodeMethodNotAllowed},
		{"malformedBody", http.MethodPut, target, "{not json", http.StatusBadRequest, CodeMalformedRequest},
		{"unknownField", http.MethodPut, target, `{"schemaVersion":1,"expectedRevision":0,"state":{},"extra":1}`, http.StatusBadRequest, CodeMalformedRequest},
		{"trailingContent", http.MethodPut, target, `{"schemaVersion":1,"expectedRevision":0,"state":{}}{}`, http.StatusBadRequest, CodeMalformedRequest},
		{"badSchema", http.MethodPut, target, `{"schemaVersion":99,"expectedRevision":0,"state":{}}`, http.StatusBadRequest, CodeSchemaUnsupported},
		{"emptyState", http.MethodPut, target, `{"schemaVersion":1,"expectedRevision":0}`, http.StatusBadRequest, CodeInvalidState},
		{
			"oversizedState", http.MethodPut, target,
			fmt.Sprintf(`{"schemaVersion":1,"expectedRevision":0,"state":{"blob":%q}}`, strings.Repeat("x", 512)),
			http.StatusRequestEntityTooLarge, CodeStateTooLarge,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := request(t, handler, testCase.method, testCase.target, testCase.body)
			if recorder.Code != testCase.status {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, testCase.status, recorder.Body)
			}
			failure := decodeFailure(t, recorder)
			if failure.Code != testCase.code {
				t.Fatalf("code = %s, want %s", failure.Code, testCase.code)
			}
		})
	}
}

func TestHandlerRejectsInvalidIdentity(t *testing.T) {
	t.Parallel()
	store, _ := newStore(t, Options{})
	handler := store.Handler()

	for name, target := range map[string]string{
		"controlCharacter": workspaceRoutePrefix + "claude/" + url.PathEscape("a\x00b"),
		"oversized":        workspaceRoutePrefix + "claude/" + strings.Repeat("a", maxIdentityFieldBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			recorder := request(t, handler, http.MethodGet, target, "")
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body)
			}
			if failure := decodeFailure(t, recorder); failure.Code != CodeInvalidSession {
				t.Fatalf("code = %s, want %s", failure.Code, CodeInvalidSession)
			}
		})
	}
}

func TestHandlerRejectsNonJSONContentType(t *testing.T) {
	t.Parallel()
	store, _ := newStore(t, Options{})
	handler := store.Handler()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPut, workspaceURL(testSession()),
		strings.NewReader(`{"schemaVersion":1,"expectedRevision":0,"state":{}}`))
	req.Header.Set("Content-Type", "text/plain")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415", recorder.Code)
	}
	if failure := decodeFailure(t, recorder); failure.Code != CodeUnsupportedContent {
		t.Fatalf("code = %s, want %s", failure.Code, CodeUnsupportedContent)
	}
}

func TestHandlerRejectsOversizedBodyBeforeDecoding(t *testing.T) {
	t.Parallel()
	store, _ := newStore(t, Options{MaxStateBytes: 128})
	handler := store.Handler()

	body := fmt.Sprintf(`{"schemaVersion":1,"expectedRevision":0,"state":{"blob":%q}}`,
		strings.Repeat("x", int(128+requestHeadroom)+1024))
	recorder := request(t, handler, http.MethodPut, workspaceURL(testSession()), body)
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413: %s", recorder.Code, recorder.Body)
	}
	if failure := decodeFailure(t, recorder); failure.Code != CodeRequestTooLarge {
		t.Fatalf("code = %s, want %s", failure.Code, CodeRequestTooLarge)
	}
}

func TestHandlerErrorsNeverLeakPrivatePaths(t *testing.T) {
	t.Parallel()
	store, root := newStore(t, Options{MaxStateBytes: 128})
	handler := store.Handler()

	bodies := []string{
		"{not json",
		`{"schemaVersion":99,"expectedRevision":0,"state":{}}`,
		fmt.Sprintf(`{"schemaVersion":1,"expectedRevision":0,"state":{"blob":%q}}`, strings.Repeat("x", 512)),
	}
	for _, body := range bodies {
		recorder := request(t, handler, http.MethodPut, workspaceURL(testSession()), body)
		payload := recorder.Body.String()
		if strings.Contains(payload, root) || strings.Contains(payload, "/var") || strings.Contains(payload, "runfs:") {
			t.Fatalf("error body leaked internals: %s", payload)
		}
	}
}

func TestRegisterMountsOnlyTheWorkspacePrefix(t *testing.T) {
	t.Parallel()
	store, _ := newStore(t, Options{})
	mux := http.NewServeMux()
	store.Register(mux)

	recorder := request(t, mux, http.MethodGet, workspaceURL(testSession()), "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("workspace status = %d, want 200", recorder.Code)
	}

	for _, target := range []string{"/api/canvas/sessions/claude/abc", "/api/sessions", "/"} {
		recorder := request(t, mux, http.MethodGet, target, "")
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404; the package registered outside its route group", target, recorder.Code)
		}
	}
}
