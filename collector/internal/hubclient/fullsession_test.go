package hubclient

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
	"github.com/centauri-ai/coslash/collector/internal/fullsessionexport"
)

func fullSessionRecordForTest(t *testing.T) fullsessionv1.Record {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "fullsession", "v1", "testdata", "fixtures", "valid", "codex.json"))
	if err != nil {
		t.Fatal(err)
	}
	record, err := fullsessionv1.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func fullSessionCapability(max int) string {
	return `{"product":"coslash-server","serverId":"server","displayName":"Hub","protocolVersions":["v1","v2"],"snapshotVersions":["session-snapshot/v1"],"maxSnapshotBytes":262144,"fullSessionVersions":["full-session-record/v1"],"maxFullSessionBytes":` +
		fmt.Sprint(max) + `,"maxRequestBytes":1048576,"pairingUrl":"https://hub.example/pair","teamUrl":"https://hub.example"}`
}

func TestFullSessionPreviewGatesContentOnDestinationCapabilityAndSize(t *testing.T) {
	record := fullSessionRecordForTest(t)
	selection := FullSessionSelection{SourceID: record.SourceID, Agent: record.Agent, SessionID: record.SessionID, RevisionID: record.RevisionID}
	for _, test := range []struct {
		name, capability string
		wantState        string
		wantLoads        int
	}{
		{name: "incompatible", capability: `{}`, wantState: "incompatible_server", wantLoads: 0},
		{name: "oversized", capability: fullSessionCapability(1), wantState: "oversized", wantLoads: 1},
		{name: "ready", capability: fullSessionCapability(1 << 20), wantState: "ready", wantLoads: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			loads := 0
			posts := 0
			base, _ := url.Parse("https://hub.example")
			client := Client{
				BaseURL: base, Credentials: &memoryCredentials{},
				HTTP: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
					if request.Method == http.MethodPost {
						posts++
					}
					switch request.URL.Path {
					case "/v1/share-destination":
						return response(http.StatusOK, `{"contractVersion":"hub-share/v1","state":"ready","destination":{"workspaceId":"workspace","workspaceName":"Compiler Team","currentMemberCount":2,"resultingMemberCount":2,"currentApprovedSessionCount":0,"historyDisclosure":"Current members","credentialState":"paired"},"configured":true}`), nil
					case "/.well-known/coslash-server":
						return response(http.StatusOK, test.capability), nil
					default:
						t.Fatalf("unexpected request %s", request.URL.Path)
						return nil, nil
					}
				})},
				LoadFullSession: func(sourceID, agent, sessionID, revisionID string) (*fullsessionv1.Record, fullsessionexport.Repository, error) {
					loads++
					copy := record
					return &copy, fullsessionexport.Repository{Canonical: "github.com/centauri-ai/coslash"}, nil
				},
			}
			preview := client.PreviewFullSession(context.Background(), selection)
			if preview.State != test.wantState || loads != test.wantLoads || posts != 0 {
				t.Fatalf("preview=%#v loads=%d posts=%d", preview, loads, posts)
			}
			if preview.State != "ready" && preview.Envelope != nil {
				t.Fatal("error preview leaked the full envelope")
			}
			if preview.State == "ready" && (!preview.ApprovalAllowed || preview.RecordBytes != 2318 || preview.PayloadBytes != 2620 || preview.Envelope == nil) {
				t.Fatalf("ready preview = %#v", preview)
			}
		})
	}
}

func TestFullSessionPreviewRequiresPairedDestinationBeforeLoadingContent(t *testing.T) {
	record := fullSessionRecordForTest(t)
	loads := 0
	capabilityRequests := 0
	base, _ := url.Parse("https://hub.example")
	client := Client{
		BaseURL: base, Credentials: &memoryCredentials{},
		LoadFullSession: func(string, string, string, string) (*fullsessionv1.Record, fullsessionexport.Repository, error) {
			loads++
			return &record, fullsessionexport.Repository{Canonical: "github.com/centauri-ai/coslash"}, nil
		},
		HTTP: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.URL.Path == "/.well-known/coslash-server" {
				capabilityRequests++
			}
			return response(http.StatusOK, `{"contractVersion":"hub-share/v1","state":"pairing_required","configured":true}`), nil
		})},
	}
	preview := client.PreviewFullSession(context.Background(), FullSessionSelection{
		SourceID: record.SourceID, Agent: record.Agent, SessionID: record.SessionID, RevisionID: record.RevisionID,
	})
	if preview.State != "unavailable" || preview.Problem == nil || preview.Problem.Code != "unauthorized" ||
		preview.Envelope != nil || loads != 0 || capabilityRequests != 0 {
		t.Fatalf("preview=%#v loads=%d capabilityRequests=%d", preview, loads, capabilityRequests)
	}
}

func TestFullSessionCapabilityFailuresKeepRetryIdentity(t *testing.T) {
	record := fullSessionRecordForTest(t)
	selection := FullSessionSelection{SourceID: record.SourceID, Agent: record.Agent, SessionID: record.SessionID, RevisionID: record.RevisionID}
	request := FullSessionShareRequest{
		ContractVersion: fullsessionexport.ShareVersion, Selection: selection, IdempotencyKey: "full-session-capability-0001",
		Consent: FullSessionConsent{
			PreviewContractVersion: fullsessionexport.PreviewVersion, RevisionID: record.RevisionID,
			RecordSHA256: "sha256:" + strings.Repeat("a", 64), RecordBytes: 1, PayloadBytes: 1,
			Repository:             fullsessionexport.Repository{Canonical: "github.com/centauri-ai/coslash"},
			DestinationWorkspaceID: "workspace", DestinationName: "Compiler Team", AudienceMemberCount: 2,
		},
	}
	for _, test := range []struct {
		name      string
		status    int
		body      string
		transport error
		wantCode  string
		wantRetry bool
	}{
		{name: "network", transport: &net.DNSError{Err: "temporary", IsTemporary: true}, wantCode: "network_unavailable", wantRetry: true},
		{name: "server error", status: http.StatusInternalServerError, body: `{}`, wantCode: "temporary_unavailable", wantRetry: true},
		{name: "malformed", status: http.StatusOK, body: `{`, wantCode: "temporary_unavailable", wantRetry: true},
		{name: "unsupported", status: http.StatusOK, body: `{}`, wantCode: "incompatible_server", wantRetry: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			loads := 0
			base, _ := url.Parse("https://hub.example")
			client := Client{
				BaseURL: base, Credentials: &memoryCredentials{},
				LoadFullSession: func(string, string, string, string) (*fullsessionv1.Record, fullsessionexport.Repository, error) {
					loads++
					return &record, request.Consent.Repository, nil
				},
				HTTP: &http.Client{Transport: roundTripFunc(func(httpRequest *http.Request) (*http.Response, error) {
					switch httpRequest.URL.Path {
					case "/v1/share-destination":
						return response(http.StatusOK, `{"contractVersion":"hub-share/v1","state":"ready","destination":{"workspaceId":"workspace","workspaceName":"Compiler Team","currentMemberCount":2,"resultingMemberCount":2,"currentApprovedSessionCount":0,"historyDisclosure":"Current members","credentialState":"paired"},"configured":true}`), nil
					case "/.well-known/coslash-server":
						if test.transport != nil {
							return nil, test.transport
						}
						return response(test.status, test.body), nil
					default:
						t.Fatalf("unexpected request %s", httpRequest.URL.Path)
						return nil, nil
					}
				})},
			}
			result := client.ShareFullSession(context.Background(), request)
			if result.State != "failed" || result.Error == nil || result.Error.Code != test.wantCode ||
				result.Error.Retryable != test.wantRetry || result.IdempotencyKey != request.IdempotencyKey || loads != 0 {
				t.Fatalf("result=%#v loads=%d", result, loads)
			}
		})
	}
}

func TestFullSessionShareUploadsThePreviewedBytesAndReturnsCanonicalRoute(t *testing.T) {
	record := fullSessionRecordForTest(t)
	repository := fullsessionexport.Repository{Canonical: "github.com/centauri-ai/coslash"}
	selection := FullSessionSelection{SourceID: record.SourceID, Agent: record.Agent, SessionID: record.SessionID, RevisionID: record.RevisionID}
	payload, recordBytes, hash, err := fullsessionexport.Marshal(record, repository)
	if err != nil {
		t.Fatal(err)
	}
	request := FullSessionShareRequest{
		ContractVersion: fullsessionexport.ShareVersion, Selection: selection, IdempotencyKey: "full-session-key-0001",
		Consent: FullSessionConsent{
			PreviewContractVersion: fullsessionexport.PreviewVersion, RevisionID: record.RevisionID,
			RecordSHA256: hash, RecordBytes: len(recordBytes), PayloadBytes: len(payload), Repository: repository,
			DestinationWorkspaceID: "workspace", DestinationName: "Compiler Team", AudienceMemberCount: 2,
		},
	}
	base, _ := url.Parse("https://hub.example")
	client := Client{
		BaseURL: base, Credentials: &memoryCredentials{},
		LoadFullSession: func(sourceID, agent, sessionID, revisionID string) (*fullsessionv1.Record, fullsessionexport.Repository, error) {
			copy := record
			return &copy, repository, nil
		},
		HTTP: &http.Client{Transport: roundTripFunc(func(httpRequest *http.Request) (*http.Response, error) {
			switch httpRequest.URL.Path {
			case "/v1/share-destination":
				return response(http.StatusOK, `{"contractVersion":"hub-share/v1","state":"ready","destination":{"workspaceId":"workspace","workspaceName":"Compiler Team","currentMemberCount":2,"resultingMemberCount":2,"currentApprovedSessionCount":0,"historyDisclosure":"Current members","credentialState":"paired"},"configured":true}`), nil
			case "/.well-known/coslash-server":
				return response(http.StatusOK, fullSessionCapability(1<<20)), nil
			case "/v2/session-revisions":
				if httpRequest.Header.Get("Content-Type") != fullsessionexport.MediaType || httpRequest.Header.Get("Content-Encoding") != "gzip" ||
					httpRequest.Header.Get("Idempotency-Key") != request.IdempotencyKey ||
					httpRequest.Header.Get("Coslash-Destination-Workspace-Id") != "workspace" {
					t.Fatalf("upload headers = %#v", httpRequest.Header)
				}
				reader, err := gzip.NewReader(httpRequest.Body)
				if err != nil {
					t.Fatal(err)
				}
				got, err := io.ReadAll(reader)
				if err != nil || string(got) != string(payload) {
					t.Fatalf("uploaded bytes match=%t err=%v", string(got) == string(payload), err)
				}
				body, _ := json.Marshal(fullSessionUploadResponse{
					SourceID: record.SourceID, Agent: record.Agent, SessionID: record.SessionID, RevisionID: record.RevisionID,
					RepositoryID: "repo", ByteCount: len(recordBytes), ContentSHA256: hash,
					SharedAt:    time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC),
					RevisionURL: "/v2/sources/" + record.SourceID + "/agents/" + record.Agent + "/sessions/" + record.SessionID + "/revisions/" + record.RevisionID,
				})
				return response(http.StatusCreated, string(body)), nil
			default:
				t.Fatalf("unexpected request %s %s", httpRequest.Method, httpRequest.URL.Path)
				return nil, nil
			}
		})},
	}
	result := client.ShareFullSession(context.Background(), request)
	if result.State != "accepted" || result.Route == nil || result.Route.HubContractVersion != "full-session-read/v1" ||
		!strings.HasSuffix(result.Route.Path, record.RevisionID) || result.ContentSHA256 != hash {
		t.Fatalf("share result = %#v", result)
	}
}

func TestFullSessionShareRequiresRenewedReviewWhenAudienceChanges(t *testing.T) {
	record := fullSessionRecordForTest(t)
	loads := 0
	base, _ := url.Parse("https://hub.example")
	client := Client{
		BaseURL: base, Credentials: &memoryCredentials{},
		LoadFullSession: func(string, string, string, string) (*fullsessionv1.Record, fullsessionexport.Repository, error) {
			loads++
			return &record, fullsessionexport.Repository{Canonical: "github.com/centauri-ai/coslash"}, nil
		},
		HTTP: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.URL.Path != "/v1/share-destination" {
				t.Fatalf("content or upload request occurred after the audience changed: %s", request.URL.Path)
			}
			return response(http.StatusOK, `{"contractVersion":"hub-share/v1","state":"ready","destination":{"workspaceId":"workspace","workspaceName":"Compiler Team","currentMemberCount":3,"resultingMemberCount":3,"currentApprovedSessionCount":0,"historyDisclosure":"Current members","credentialState":"paired"},"configured":true}`), nil
		})},
	}
	result := client.ShareFullSession(context.Background(), FullSessionShareRequest{
		ContractVersion: fullsessionexport.ShareVersion,
		Selection:       FullSessionSelection{SourceID: record.SourceID, Agent: record.Agent, SessionID: record.SessionID, RevisionID: record.RevisionID},
		IdempotencyKey:  "full-session-key-0001",
		Consent: FullSessionConsent{PreviewContractVersion: fullsessionexport.PreviewVersion, RevisionID: record.RevisionID,
			RecordSHA256: "sha256:" + strings.Repeat("a", 64), RecordBytes: 1, PayloadBytes: 1,
			Repository:             fullsessionexport.Repository{Canonical: "github.com/centauri-ai/coslash"},
			DestinationWorkspaceID: "workspace", DestinationName: "Compiler Team", AudienceMemberCount: 2},
	})
	if result.State != "failed" || result.Error == nil || result.Error.Code != "review_binding_changed" || loads != 0 {
		t.Fatalf("result=%#v loads=%d", result, loads)
	}
}

func TestTimedOutFullSessionUploadResolvesWithTheOriginalKey(t *testing.T) {
	record := fullSessionRecordForTest(t)
	repository := fullsessionexport.Repository{Canonical: "github.com/centauri-ai/coslash"}
	payload, recordBytes, hash, err := fullsessionexport.Marshal(record, repository)
	if err != nil {
		t.Fatal(err)
	}
	request := FullSessionShareRequest{
		ContractVersion: fullsessionexport.ShareVersion,
		Selection:       FullSessionSelection{SourceID: record.SourceID, Agent: record.Agent, SessionID: record.SessionID, RevisionID: record.RevisionID},
		IdempotencyKey:  "full-session-timeout-0001",
		Consent: FullSessionConsent{PreviewContractVersion: fullsessionexport.PreviewVersion, RevisionID: record.RevisionID,
			RecordSHA256: hash, RecordBytes: len(recordBytes), PayloadBytes: len(payload), Repository: repository,
			DestinationWorkspaceID: "workspace", DestinationName: "Compiler Team", AudienceMemberCount: 2},
	}
	calls := []string{}
	base, _ := url.Parse("https://hub.example")
	client := Client{
		BaseURL: base, Credentials: &memoryCredentials{},
		LoadFullSession: func(string, string, string, string) (*fullsessionv1.Record, fullsessionexport.Repository, error) {
			copy := record
			return &copy, repository, nil
		},
		HTTP: &http.Client{Transport: roundTripFunc(func(httpRequest *http.Request) (*http.Response, error) {
			calls = append(calls, httpRequest.Method+" "+httpRequest.URL.Path)
			switch httpRequest.URL.Path {
			case "/v1/share-destination":
				return response(http.StatusOK, `{"contractVersion":"hub-share/v1","state":"ready","destination":{"workspaceId":"workspace","workspaceName":"Compiler Team","currentMemberCount":2,"resultingMemberCount":2,"currentApprovedSessionCount":0,"historyDisclosure":"Current members","credentialState":"paired"},"configured":true}`), nil
			case "/.well-known/coslash-server":
				return response(http.StatusOK, fullSessionCapability(1<<20)), nil
			case "/v2/session-revisions":
				return nil, &net.DNSError{IsTimeout: true}
			case "/v2/uploads/status":
				if httpRequest.Header.Get("Idempotency-Key") != request.IdempotencyKey {
					t.Fatalf("lookup key = %q", httpRequest.Header.Get("Idempotency-Key"))
				}
				body, _ := json.Marshal(fullSessionUploadResponse{
					SourceID: record.SourceID, Agent: record.Agent, SessionID: record.SessionID, RevisionID: record.RevisionID,
					RepositoryID: "repo", ByteCount: len(recordBytes), ContentSHA256: hash, Deduplicated: true,
					SharedAt:    time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC),
					RevisionURL: "/v2/sources/" + record.SourceID + "/agents/" + record.Agent + "/sessions/" + record.SessionID + "/revisions/" + record.RevisionID,
				})
				return response(http.StatusOK, string(body)), nil
			default:
				t.Fatalf("unexpected request %s", httpRequest.URL.Path)
				return nil, nil
			}
		})},
	}
	result := client.ShareFullSession(context.Background(), request)
	if result.State != "already_accepted" || !result.Deduplicated || result.Route == nil {
		t.Fatalf("result = %#v", result)
	}
	if strings.Join(calls, ",") != "GET /v1/share-destination,GET /.well-known/coslash-server,POST /v2/session-revisions,GET /v2/uploads/status" {
		t.Fatalf("calls = %#v", calls)
	}
}
