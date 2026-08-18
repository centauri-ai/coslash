package hubclient

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/sessionexport"
	"github.com/centauri-ai/coslash/collector/internal/sessionpreview"
)

type credentialMemory struct{ value string }

func (s *credentialMemory) Load(context.Context) (string, error) {
	if s.value == "" {
		return "", ErrNotPaired
	}
	return s.value, nil
}
func (s *credentialMemory) Save(_ context.Context, value string) error { s.value = value; return nil }
func (s *credentialMemory) Delete(context.Context) error               { s.value = ""; return nil }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func testClient(store CredentialStore, transport roundTripFunc) *Client {
	base, _ := url.Parse("https://hub.example.test")
	return &Client{BaseURL: base, Credentials: store, HTTP: &http.Client{Transport: transport}, DeviceName: "Test device", CollectorVersion: "test"}
}

func TestDestinationKeepsCredentialOutOfResponse(t *testing.T) {
	store := &credentialMemory{value: "device-secret"}
	client := testClient(store, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/v1/share-destination" || request.Header.Get("Authorization") != "Device device-secret" {
			t.Fatalf("request=%s authorization=%q", request.URL, request.Header.Get("Authorization"))
		}
		return response(http.StatusOK, `{"contractVersion":"hub-share/v1","state":"ready","destination":{"workspaceId":"10000000-0000-4000-8000-000000000001","workspaceName":"Compiler Team","currentMemberCount":2,"resultingMemberCount":2,"currentApprovedSessionCount":3,"historyDisclosure":"Current audience","credentialState":"paired"}}`), nil
	})
	result, err := client.Destination(context.Background())
	if err != nil || result.Destination == nil || result.Destination.WorkspaceName != "Compiler Team" || !result.Configured {
		t.Fatalf("result=%#v error=%v", result, err)
	}
	encoded, _ := json.Marshal(result)
	if bytes.Contains(encoded, []byte("device-secret")) {
		t.Fatal("destination response exposed the device credential")
	}
}

func TestPairingStoresCredentialOnlyAfterApproval(t *testing.T) {
	store := &credentialMemory{}
	polls := 0
	client := testClient(store, func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/v1/device-authorizations":
			return response(http.StatusCreated, `{"id":"pair-1","deviceCode":"private-device-code","userCode":"ABCD-EFGH","verificationUri":"https://hub.example.test/pair","verificationUriComplete":"https://hub.example.test/pair?code=ABCD-EFGH","expiresAt":"2099-01-01T00:00:00Z","intervalSeconds":2}`), nil
		case "/v1/device-authorizations/token":
			polls++
			if polls == 1 {
				return response(http.StatusBadRequest, `{"code":"pairing_pending","detail":"Approve first"}`), nil
			}
			return response(http.StatusOK, `{"deviceId":"device-1","credential":"stored-secret","tokenType":"Device","scope":"ingest"}`), nil
		default:
			t.Fatalf("unexpected request %s", request.URL.Path)
			return nil, nil
		}
	})
	pairing, err := client.BeginPairing(context.Background())
	if err != nil || pairing.PairingID != "pair-1" || strings.Contains(pairing.VerificationURIComplete, "private-device-code") {
		t.Fatalf("pairing=%#v error=%v", pairing, err)
	}
	pending, err := client.PollPairing(context.Background(), pairing.PairingID)
	if err != nil || pending.State != "pending" || store.value != "" {
		t.Fatalf("pending=%#v credential=%q error=%v", pending, store.value, err)
	}
	paired, err := client.PollPairing(context.Background(), pairing.PairingID)
	if err != nil || paired.State != "paired" || store.value != "stored-secret" {
		t.Fatalf("paired=%#v credential=%q error=%v", paired, store.value, err)
	}
}

func TestShareRebuildsApprovedBytesAndMapsCanonicalRoute(t *testing.T) {
	repository := "github.com/centauri-ai/coslash"
	local := session.Session{
		Agent: "codex", ID: "source-1", Repository: &repository,
		StartedAt: 1_000, LastActivityTime: 2_000, Tokens: map[string]session.ModelTokens{},
	}
	preview := sessionpreview.Build(local, sessionexport.BuildOptions{CollectorVersion: "test"}, local.LastActivityTime)
	if preview.Snapshot == nil {
		t.Fatal("test snapshot did not build")
	}
	store := &credentialMemory{value: "device-secret"}
	client := testClient(store, func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Authorization") != "Device device-secret" ||
			request.Header.Get("Coslash-Destination-Workspace-Id") != "10000000-0000-4000-8000-000000000001" ||
			request.Header.Get("Idempotency-Key") != "hub-share/v1:test-key-0001" {
			t.Fatalf("upload headers=%v", request.Header)
		}
		reader, err := gzip.NewReader(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		payload, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		approved, err := sessionpreview.UploadBytes(preview)
		if err != nil || !bytes.Equal(payload, approved) {
			t.Fatal("upload bytes differ from the approved canonical preview")
		}
		return response(http.StatusCreated, `{"sessionId":"60000000-0000-4000-8000-000000000001","revisionId":"70000000-0000-4000-8000-000000000001","repositoryId":"80000000-0000-4000-8000-000000000001","canonicalWeekStart":"2026-08-17","deduplicated":false,"sharedAt":"2026-08-18T18:00:00Z","briefStatus":"pending","teamUrl":"https://hub.example.test/repos/80000000-0000-4000-8000-000000000001/sessions/2026-08-17"}`), nil
	})
	client.LoadSession = func(id string, revision int64) (*session.Session, error) {
		if id != local.ID || revision != local.LastActivityTime {
			t.Fatalf("load id=%q revision=%d", id, revision)
		}
		copy := local
		return &copy, nil
	}
	result, err := client.Share(context.Background(), ShareRequest{
		ContractVersion: ContractVersion, RequestID: "request-1",
		Items: []ShareItemRequest{{
			LocalSessionID: "codex:source-1", IdempotencyKey: "hub-share/v1:test-key-0001",
			Consent: ConsentBinding{PreviewContractVersion: PreviewVersion, SourceRevision: local.LastActivityTime,
				ContentHash: strings.TrimPrefix(preview.Snapshot.ContentHash, "sha256:"), PayloadBytes: preview.PayloadBytes,
				DestinationWorkspaceID: "10000000-0000-4000-8000-000000000001"},
		}},
	})
	if err != nil || result.State != "succeeded" || len(result.Results) != 1 || result.Results[0].Route == nil || result.Results[0].Route.Path != "/repos/80000000-0000-4000-8000-000000000001/sessions/2026-08-17" {
		t.Fatalf("result=%#v error=%v", result, err)
	}
}

func TestShareRejectsChangedSourceBeforeNetwork(t *testing.T) {
	client := testClient(&credentialMemory{value: "device-secret"}, func(*http.Request) (*http.Response, error) {
		t.Fatal("network called for stale consent")
		return nil, nil
	})
	client.LoadSession = func(string, int64) (*session.Session, error) {
		return &session.Session{Agent: "codex", ID: "source-1", LastActivityTime: 3_000}, nil
	}
	result, err := client.Share(context.Background(), ShareRequest{ContractVersion: ContractVersion, RequestID: "request-1", Items: []ShareItemRequest{{
		LocalSessionID: "codex:source-1", IdempotencyKey: "hub-share/v1:test-key-0001",
		Consent: ConsentBinding{PreviewContractVersion: PreviewVersion, SourceRevision: 2_000, ContentHash: "sha256:none", PayloadBytes: 10, DestinationWorkspaceID: "workspace"},
	}}})
	if err != nil || result.State != "failed" || result.Results[0].Error == nil || result.Results[0].Error.Code != "consent_stale" {
		t.Fatalf("result=%#v error=%v", result, err)
	}
}

func TestSharePreservesServerRetryDelay(t *testing.T) {
	local, preview := shareTestSession(t)
	client := testClient(&credentialMemory{value: "device-secret"}, func(*http.Request) (*http.Response, error) {
		result := response(http.StatusTooManyRequests, `{"code":"rate_limited","detail":"Slow down"}`)
		result.Header.Set("Retry-After", "7")
		return result, nil
	})
	client.LoadSession = func(string, int64) (*session.Session, error) { return &local, nil }
	result, err := client.Share(context.Background(), shareTestRequest(local, preview))
	if err != nil || result.Results[0].Error == nil || result.Results[0].Error.Code != "rate_limited" ||
		result.Results[0].Error.RetryAfterSeconds == nil || *result.Results[0].Error.RetryAfterSeconds != 7 {
		t.Fatalf("result=%#v error=%v", result, err)
	}
}

func TestShareClassifiesDeadlineAsTimeout(t *testing.T) {
	local, preview := shareTestSession(t)
	client := testClient(&credentialMemory{value: "device-secret"}, func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	client.LoadSession = func(string, int64) (*session.Session, error) { return &local, nil }
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	result, err := client.Share(ctx, shareTestRequest(local, preview))
	if err != nil || result.Results[0].Error == nil || result.Results[0].Error.Code != "timeout" ||
		!result.Results[0].Error.Retryable {
		t.Fatalf("result=%#v error=%v", result, err)
	}
}

func shareTestSession(t *testing.T) (session.Session, sessionpreview.Response) {
	t.Helper()
	repository := "github.com/centauri-ai/coslash"
	local := session.Session{
		Agent: "codex", ID: "source-1", Repository: &repository,
		StartedAt: 1_000, LastActivityTime: 2_000, Tokens: map[string]session.ModelTokens{},
	}
	preview := sessionpreview.Build(local, sessionexport.BuildOptions{CollectorVersion: "test"}, local.LastActivityTime)
	if preview.Snapshot == nil {
		t.Fatal("test snapshot did not build")
	}
	return local, preview
}

func shareTestRequest(local session.Session, preview sessionpreview.Response) ShareRequest {
	return ShareRequest{
		ContractVersion: ContractVersion,
		RequestID:       "request-1",
		Items: []ShareItemRequest{{
			LocalSessionID: "codex:" + local.ID,
			IdempotencyKey: "hub-share/v1:test-key-0001",
			Consent: ConsentBinding{
				PreviewContractVersion: PreviewVersion,
				SourceRevision:         local.LastActivityTime,
				ContentHash:            strings.TrimPrefix(preview.Snapshot.ContentHash, "sha256:"),
				PayloadBytes:           preview.PayloadBytes,
				DestinationWorkspaceID: "10000000-0000-4000-8000-000000000001",
			},
		}},
	}
}
