package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
	"github.com/centauri-ai/coslash/collector/internal/fullsessionexport"
	"github.com/centauri-ai/coslash/collector/internal/hubclient"
)

type fixedHubCredential string

func (credential fixedHubCredential) Load(context.Context) (string, error) {
	return string(credential), nil
}

func (fixedHubCredential) Save(context.Context, string) error { return nil }

func TestFullSessionLocalAPIPreservesConsentBytesAndRetryIdentity(t *testing.T) {
	recordData, err := os.ReadFile(filepath.Join("..", "..", "fullsession", "v1", "testdata", "fixtures", "valid", "codex.json"))
	if err != nil {
		t.Fatal(err)
	}
	record, err := fullsessionv1.Decode(recordData)
	if err != nil {
		t.Fatal(err)
	}
	repository := fullsessionexport.Repository{Canonical: "github.com/centauri-ai/coslash"}
	payload, recordBytes, hash, err := fullsessionexport.Marshal(record, repository)
	if err != nil {
		t.Fatal(err)
	}

	uploadKeys := []string{}
	acceptedKeys := map[string]bool{}
	hub := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/v1/share-destination":
			if request.Header.Get("Authorization") != "Device device-credential" {
				http.Error(response, "bad authorization", http.StatusUnauthorized)
				return
			}
			_, _ = io.WriteString(response, `{"contractVersion":"hub-share/v1","state":"ready","destination":{"workspaceId":"workspace","workspaceName":"Compiler Team","currentMemberCount":2,"resultingMemberCount":2,"currentApprovedSessionCount":0,"historyDisclosure":"Current members","credentialState":"paired","audienceVersion":"audience-v1"},"configured":true}`)
		case "/.well-known/coslash-server":
			_, _ = fmt.Fprintf(response, `{"product":"coslash-server","serverId":"server","displayName":"Hub","protocolVersions":["v1","v2"],"snapshotVersions":["session-snapshot/v1"],"maxSnapshotBytes":262144,"fullSessionVersions":["full-session-record/v1"],"maxFullSessionBytes":%d,"maxRequestBytes":1048576,"pairingUrl":"%s/pair","teamUrl":"%s"}`, 1<<20, hubURL(request), hubURL(request))
		case "/v2/session-revisions":
			key := request.Header.Get("Idempotency-Key")
			if key != "full-session-local-api-0001" || request.Header.Get("Coslash-Destination-Workspace-Id") != "workspace" ||
				request.Header.Get("Coslash-Destination-Audience-Version") != "audience-v1" ||
				request.Header.Get("Content-Type") != fullsessionexport.MediaType || request.Header.Get("Content-Encoding") != "gzip" {
				http.Error(response, "bad upload headers", http.StatusBadRequest)
				return
			}
			reader, readErr := gzip.NewReader(request.Body)
			if readErr != nil {
				http.Error(response, "bad gzip", http.StatusBadRequest)
				return
			}
			uploaded, readErr := io.ReadAll(reader)
			closeErr := reader.Close()
			if readErr != nil || closeErr != nil || !bytes.Equal(uploaded, payload) {
				http.Error(response, "canonical bytes changed", http.StatusBadRequest)
				return
			}
			deduplicated := acceptedKeys[key]
			acceptedKeys[key] = true
			uploadKeys = append(uploadKeys, key)
			if !deduplicated {
				response.WriteHeader(http.StatusCreated)
			}
			_ = json.NewEncoder(response).Encode(map[string]any{
				"sourceId": record.SourceID, "agent": record.Agent, "sessionId": record.SessionID,
				"revisionId": record.RevisionID, "repositoryId": "repository", "byteCount": len(recordBytes),
				"contentSha256": hash, "deduplicated": deduplicated, "sharedAt": time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC),
				"revisionUrl": "/v2/sources/" + record.SourceID + "/agents/" + record.Agent + "/sessions/" + record.SessionID + "/revisions/" + record.RevisionID,
			})
		default:
			http.NotFound(response, request)
		}
	}))
	defer hub.Close()
	baseURL, err := url.Parse(hub.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := &hubclient.Client{
		BaseURL: baseURL, Credentials: fixedHubCredential("device-credential"),
		LoadFullSession: func(sourceID, agent, sessionID, revisionID string) (*fullsessionv1.Record, fullsessionexport.Repository, error) {
			if sourceID != record.SourceID || agent != record.Agent || sessionID != record.SessionID || revisionID != record.RevisionID {
				return nil, fullsessionexport.Repository{}, fmt.Errorf("unexpected selection")
			}
			copy := record
			return &copy, repository, nil
		},
	}
	api := http.NewServeMux()
	registerHubRoutes(api, client, nil, nil)

	previewRequest := httptest.NewRequest(http.MethodGet, "/api/hub/full-session-preview?source="+record.SourceID+"&agent="+record.Agent+"&id="+record.SessionID+"&revision="+record.RevisionID, nil)
	previewResponse := httptest.NewRecorder()
	api.ServeHTTP(previewResponse, previewRequest)
	var preview hubclient.FullSessionPreview
	if previewResponse.Code != http.StatusOK || json.Unmarshal(previewResponse.Body.Bytes(), &preview) != nil ||
		preview.State != "ready" || preview.Envelope == nil || !preview.ApprovalAllowed {
		t.Fatalf("preview status=%d body=%s", previewResponse.Code, previewResponse.Body.String())
	}
	share := hubclient.FullSessionShareRequest{
		ContractVersion: fullsessionexport.ShareVersion, Selection: preview.Selection,
		IdempotencyKey: "full-session-local-api-0001",
		Consent: hubclient.FullSessionConsent{
			PreviewContractVersion: fullsessionexport.PreviewVersion, RevisionID: preview.Selection.RevisionID,
			RecordSHA256: preview.RecordSHA256, RecordBytes: preview.RecordBytes, PayloadBytes: preview.PayloadBytes,
			Repository: preview.Envelope.Repository, DestinationWorkspaceID: "workspace",
			DestinationName: "Compiler Team", AudienceMemberCount: 2, AudienceVersion: preview.AudienceVersion,
		},
	}
	shareBody, err := json.Marshal(share)
	if err != nil {
		t.Fatal(err)
	}
	for attempt, wantState := range []string{"accepted", "already_accepted"} {
		request := httptest.NewRequest(http.MethodPost, "/api/hub/full-session-shares", bytes.NewReader(shareBody))
		response := httptest.NewRecorder()
		api.ServeHTTP(response, request)
		var result hubclient.FullSessionShareResult
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil ||
			result.State != wantState || result.IdempotencyKey != share.IdempotencyKey || result.Route == nil ||
			result.ContentSHA256 != hash || result.ByteCount != len(recordBytes) {
			t.Fatalf("attempt %d status=%d body=%s", attempt+1, response.Code, response.Body.String())
		}
	}
	if len(uploadKeys) != 2 || uploadKeys[0] != share.IdempotencyKey || uploadKeys[1] != share.IdempotencyKey || len(acceptedKeys) != 1 {
		t.Fatalf("upload keys=%#v accepted=%#v", uploadKeys, acceptedKeys)
	}
}

func hubURL(request *http.Request) string {
	return "http://" + request.Host
}
