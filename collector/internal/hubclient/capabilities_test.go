package hubclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

const capabilityDocumentForTest = `{"product":"coslash-server","serverId":"server","displayName":"Hub","protocolVersions":["v1","v2","v3"],` +
	`"snapshotVersions":["session-snapshot/v1"],"maxSnapshotBytes":262144,"fullSessionVersions":["full-session-record/v1"],"maxFullSessionBytes":1048576,"maxRequestBytes":2097152,` +
	`"backupVersions":["session-backup/v1"],"backupUploadVersions":["backup-upload/v1"],"maxBackupBytes":1073741824,"maxBackupChunkBytes":128,"backupWorkspaceBytes":53687091200,` +
	`"backupUploadExpiresSeconds":86400,"pairingUrl":"https://hub.example/pair","teamUrl":"https://hub.example"}`

var capabilityLoadersForTest = []struct {
	name string
	load func(*Client, context.Context) (any, error)
}{
	{name: "full-session", load: func(client *Client, ctx context.Context) (any, error) {
		return client.fullSessionCapabilities(ctx)
	}},
	{name: "backup", load: func(client *Client, ctx context.Context) (any, error) {
		return client.loadBackupCapabilities(ctx)
	}},
}

type capabilityBodyForTest struct {
	io.Reader
	readBytes int
	closed    bool
}

func (body *capabilityBodyForTest) Read(buffer []byte) (int, error) {
	count, err := body.Reader.Read(buffer)
	body.readBytes += count
	return count, err
}

func (body *capabilityBodyForTest) Close() error {
	body.closed = true
	return nil
}

func assertCapabilityFailure(t *testing.T, value any, err error, wantCode string, wantRetry bool) {
	t.Helper()
	var failure *fullSessionCapabilityError
	if !errors.As(err, &failure) {
		t.Fatalf("expected typed capability failure, got %v", err)
	}
	code, retry := classifyCapabilityFailure(err)
	if code != wantCode || retry != wantRetry || !reflect.ValueOf(value).IsZero() {
		t.Fatalf("value=%#v code=%q retry=%t err=%v", value, code, retry, err)
	}
}

func TestCapabilityLoadersFetchPolicy(t *testing.T) {
	atLimit := strings.Replace(capabilityDocumentForTest, `"Hub"`, `"`+strings.Repeat("x", (1<<20)-len(capabilityDocumentForTest)+3)+`"`, 1)
	type fetchTest struct {
		name      string
		status    int
		body      string
		transport error
		wantCode  string
		wantRetry bool
	}
	tests := []fetchTest{
		{name: "complete document", status: http.StatusOK, body: capabilityDocumentForTest},
		{name: "trailing whitespace", status: http.StatusOK, body: capabilityDocumentForTest + "\n\t "},
		{name: "at decode limit", status: http.StatusOK, body: atLimit},
		{name: "over decode limit", status: http.StatusOK, body: strings.Replace(atLimit, `"displayName":"`, `"displayName":"x`, 1), wantCode: "temporary_unavailable", wantRetry: true},
		{name: "unknown field", status: http.StatusOK, body: strings.TrimSuffix(capabilityDocumentForTest, "}") + `,"futureField":true}`, wantCode: "temporary_unavailable", wantRetry: true},
		{name: "trailing object", status: http.StatusOK, body: capabilityDocumentForTest + `{}`, wantCode: "temporary_unavailable", wantRetry: true},
		{name: "trailing null", status: http.StatusOK, body: capabilityDocumentForTest + `null`, wantCode: "temporary_unavailable", wantRetry: true},
		{name: "trailing garbage", status: http.StatusOK, body: capabilityDocumentForTest + `!`, wantCode: "temporary_unavailable", wantRetry: true},
		{name: "malformed", status: http.StatusOK, body: `{`, wantCode: "temporary_unavailable", wantRetry: true},
		{name: "wrong field type", status: http.StatusOK, body: strings.Replace(capabilityDocumentForTest, `"maxBackupBytes":1073741824`, `"maxBackupBytes":"1073741824"`, 1), wantCode: "temporary_unavailable", wantRetry: true},
		{name: "wrong full-session field type", status: http.StatusOK, body: strings.Replace(capabilityDocumentForTest, `"maxRequestBytes":2097152`, `"maxRequestBytes":"2097152"`, 1), wantCode: "temporary_unavailable", wantRetry: true},
		{name: "network", transport: errors.New("transport unavailable"), wantCode: "network_unavailable", wantRetry: true},
	}
	for _, status := range []int{
		http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect,
		http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound,
		http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusServiceUnavailable,
	} {
		retry := status >= 500 || status == 408 || status == 429
		code := "incompatible_server"
		if retry {
			code = "temporary_unavailable"
		}
		tests = append(tests, fetchTest{name: fmt.Sprint(status), status: status, body: capabilityDocumentForTest, wantCode: code, wantRetry: retry})
	}
	for _, loader := range capabilityLoadersForTest {
		for _, test := range tests {
			t.Run(loader.name+"/"+test.name, func(t *testing.T) {
				base, _ := url.Parse("https://hub.example/prefix?ignored=query#fragment")
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				body := &capabilityBodyForTest{Reader: strings.NewReader(test.body)}
				calls, redirects := 0, 0
				client := &Client{BaseURL: base, HTTP: &http.Client{
					CheckRedirect: func(*http.Request, []*http.Request) error { redirects++; return errors.New("unexpected redirect") },
					Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
						calls++
						if request.Method != http.MethodGet || request.URL.String() != "https://hub.example/prefix/.well-known/coslash-server" ||
							request.Header.Get("Authorization") != "" || request.Context() != ctx {
							t.Fatalf("unexpected capability request: %#v", request)
						}
						if test.transport != nil {
							return nil, test.transport
						}
						result := response(test.status, "")
						result.Body = body
						result.Header.Set("Location", "https://other.example/capabilities")
						return result, nil
					}),
				}}
				value, err := loader.load(client, ctx)
				if test.wantCode != "" {
					assertCapabilityFailure(t, value, err, test.wantCode, test.wantRetry)
				} else {
					if err != nil {
						t.Fatal(err)
					}
					if loader.name == "full-session" {
						encoded, err := json.Marshal(value)
						if err != nil || string(encoded) != strings.TrimSpace(test.body) {
							t.Fatalf("capability wire document changed: %v", err)
						}
					} else if value != (BackupCapability{ServerID: "server", MaxBackupBytes: 1073741824, MaxBackupChunkBytes: 128,
						BackupWorkspaceBytes: 53687091200, BackupUploadExpiresSeconds: 86400}) {
						t.Fatalf("backup projection = %#v", value)
					}
				}
				if calls != 1 || redirects != 0 || test.transport == nil && !body.closed || body.readBytes > 1<<20 {
					t.Fatalf("calls=%d redirects=%d closed=%t bytes=%d", calls, redirects, body.closed, body.readBytes)
				}
			})
		}
	}
}

func TestCapabilityLoadersKeepFeatureRequirementsSeparate(t *testing.T) {
	type requirementTest struct {
		field       string
		value       any
		fullSession bool
		backup      bool
	}
	tests := []requirementTest{
		{field: "product", value: "other"},
		{field: "serverId", value: "", fullSession: true},
		{field: "protocolVersions", value: []string{"v2"}, fullSession: true},
		{field: "protocolVersions", value: []string{"v3"}, backup: true},
		{field: "protocolVersions", value: []string{"v1"}},
		{field: "fullSessionVersions", value: []string{}, backup: true},
		{field: "backupVersions", value: []string{}, fullSession: true},
		{field: "backupUploadVersions", value: []string{}, fullSession: true},
	}
	for _, field := range []string{"maxFullSessionBytes", "maxRequestBytes", "maxBackupBytes", "maxBackupChunkBytes", "backupWorkspaceBytes", "backupUploadExpiresSeconds"} {
		for _, limit := range []int{0, -1, 1} {
			tests = append(tests, requirementTest{field: field, value: limit,
				fullSession: limit > 0 || field != "maxFullSessionBytes" && field != "maxRequestBytes",
				backup:      limit > 0 || field == "maxFullSessionBytes" || field == "maxRequestBytes"})
		}
	}
	for _, loader := range capabilityLoadersForTest {
		for _, test := range tests {
			t.Run(fmt.Sprintf("%s/%s/%v", loader.name, test.field, test.value), func(t *testing.T) {
				var document map[string]any
				if err := json.Unmarshal([]byte(capabilityDocumentForTest), &document); err != nil {
					t.Fatal(err)
				}
				document[test.field] = test.value
				encoded, err := json.Marshal(document)
				if err != nil {
					t.Fatal(err)
				}
				base, _ := url.Parse("https://hub.example")
				client := &Client{BaseURL: base, HTTP: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					return response(http.StatusOK, string(encoded)), nil
				})}}
				value, err := loader.load(client, context.Background())
				wantValid := test.fullSession
				wantMessage := "Hub does not advertise compatible full-session v2 support"
				if loader.name == "backup" {
					wantValid = test.backup
					wantMessage = "Hub does not advertise compatible complete-backup support"
				}
				if wantValid {
					if err != nil {
						t.Fatal(err)
					}
				} else {
					assertCapabilityFailure(t, value, err, "incompatible_server", false)
					if err.Error() != wantMessage {
						t.Fatalf("feature error = %q", err.Error())
					}
				}
			})
		}
	}
}

func TestCapabilityLoadersConfigurationAndFreshFetch(t *testing.T) {
	for _, loader := range capabilityLoadersForTest {
		t.Run(loader.name, func(t *testing.T) {
			for _, client := range []*Client{nil, {}} {
				value, err := loader.load(client, context.Background())
				assertCapabilityFailure(t, value, err, "incompatible_server", false)
				if err.Error() != "Hub server is not configured" {
					t.Fatalf("configuration error = %q", err.Error())
				}
			}
			client := &Client{BaseURL: &url.URL{Scheme: "https", Host: "invalid\nhost"}}
			value, err := loader.load(client, context.Background())
			assertCapabilityFailure(t, value, err, "temporary_unavailable", true)
			base, _ := url.Parse("https://hub.example")
			client.BaseURL = base
			calls := 0
			client.HTTP = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					return response(http.StatusOK, capabilityDocumentForTest), nil
				}
				return response(http.StatusOK, `{}`), nil
			})}
			if _, err := loader.load(client, context.Background()); err != nil {
				t.Fatal(err)
			}
			value, err = loader.load(client, context.Background())
			assertCapabilityFailure(t, value, err, "incompatible_server", false)
			if calls != 2 {
				t.Fatalf("capability fetches = %d", calls)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			client.HTTP.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if !errors.Is(request.Context().Err(), context.Canceled) {
					t.Fatal("capability request lost cancellation")
				}
				return nil, request.Context().Err()
			})
			value, err = loader.load(client, ctx)
			assertCapabilityFailure(t, value, err, "network_unavailable", true)
		})
	}
}

func TestCapabilityHTTPClientPreservesTimeoutPolicy(t *testing.T) {
	client := &Client{}
	if client.httpClient().Timeout != 30*time.Second {
		t.Fatal("default capability timeout changed")
	}
	client.HTTP = &http.Client{Timeout: 13 * time.Second}
	if client.httpClient().Timeout != client.HTTP.Timeout {
		t.Fatal("configured capability timeout changed")
	}
}
