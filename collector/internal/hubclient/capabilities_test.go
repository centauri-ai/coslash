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
	"slices"
	"strings"
	"testing"
	"testing/iotest"
)

const hubCapabilitiesJSON = `{"product":"coslash-server","serverId":"server","displayName":"Hub","protocolVersions":["v2","v3"],"snapshotVersions":["session-snapshot/v1"],"maxSnapshotBytes":262144,"fullSessionVersions":["full-session-record/v1"],"maxFullSessionBytes":67108864,"maxRequestBytes":1048576,"backupVersions":["session-backup/v1"],"backupUploadVersions":["backup-upload/v1"],"maxBackupBytes":1073741824,"maxBackupChunkBytes":128,"backupWorkspaceBytes":53687091200,"backupUploadExpiresSeconds":86400,"pairingUrl":"https://hub.example/pair","teamUrl":"https://hub.example/team"}`

type capabilityLoaderTest struct {
	name string
	load func(*Client, context.Context) (any, error)
	zero any
	want any
}

var capabilityLoaders = []capabilityLoaderTest{
	{
		name: "backup",
		load: func(client *Client, ctx context.Context) (any, error) { return client.loadBackupCapabilities(ctx) },
		zero: BackupCapability{},
		want: BackupCapability{
			ServerID: "server", MaxBackupBytes: 1073741824, MaxBackupChunkBytes: 128,
			BackupWorkspaceBytes: 53687091200, BackupUploadExpiresSeconds: 86400,
		},
	},
	{
		name: "full-session",
		load: func(client *Client, ctx context.Context) (any, error) { return client.fullSessionCapabilities(ctx) },
		zero: hubCapabilities{},
		want: hubCapabilities{
			Product: "coslash-server", ServerID: "server", DisplayName: "Hub", ProtocolVersions: []string{"v2", "v3"},
			SnapshotVersions: []string{"session-snapshot/v1"}, MaxSnapshotBytes: 262144,
			FullSessionVersions: []string{"full-session-record/v1"}, MaxFullSessionBytes: 67108864, MaxRequestBytes: 1048576,
			BackupVersions: []string{"session-backup/v1"}, BackupUploadVersions: []string{"backup-upload/v1"},
			MaxBackupBytes: 1073741824, MaxBackupChunkBytes: 128, BackupWorkspaceBytes: 53687091200,
			BackupUploadExpiresSeconds: 86400, PairingURL: "https://hub.example/pair", TeamURL: "https://hub.example/team",
		},
	},
}

type capabilityBody struct {
	reader    io.Reader
	bytesRead int
	closes    int
	closeErr  error
}

func (body *capabilityBody) Read(buffer []byte) (int, error) {
	count, err := body.reader.Read(buffer)
	body.bytesRead += count
	return count, err
}

func (body *capabilityBody) Close() error {
	body.closes++
	return body.closeErr
}

func checkCapabilityFailure(t *testing.T, loader capabilityLoaderTest, value any, err error, code string, retryable bool, diagnostic string) {
	t.Helper()
	var failure *fullSessionCapabilityError
	if !errors.As(err, &failure) {
		t.Fatalf("expected typed capability failure, got %v", err)
	}
	gotCode, gotRetryable := classifyCapabilityFailure(err)
	if gotCode != code || gotRetryable != retryable || !strings.Contains(err.Error(), diagnostic) || !reflect.DeepEqual(value, loader.zero) {
		t.Fatalf("value=%#v error=%v code=%s retryable=%t", value, err, gotCode, gotRetryable)
	}
}

func capabilityJSONWith(t *testing.T, changes map[string]any, omitted ...string) string {
	t.Helper()
	var fields map[string]any
	if err := json.Unmarshal([]byte(hubCapabilitiesJSON), &fields); err != nil {
		t.Fatal(err)
	}
	for field, value := range changes {
		fields[field] = value
	}
	for _, field := range omitted {
		delete(fields, field)
	}
	data, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestHubCapabilityLoadersConfiguration(t *testing.T) {
	for _, loader := range capabilityLoaders {
		for _, test := range []struct {
			name       string
			client     *Client
			ctx        context.Context
			code       string
			retryable  bool
			diagnostic string
		}{
			{name: "nil client", ctx: context.Background(), code: "incompatible_server", diagnostic: "Hub server is not configured"},
			{name: "missing URL", client: &Client{}, ctx: context.Background(), code: "incompatible_server", diagnostic: "Hub server is not configured"},
			{name: "nil context", client: &Client{BaseURL: &url.URL{Scheme: "https", Host: "hub.example"}}, code: "temporary_unavailable", retryable: true, diagnostic: "nil Context"},
		} {
			t.Run(loader.name+"/"+test.name, func(t *testing.T) {
				if test.client != nil {
					test.client.HTTP = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
						t.Fatal("invalid configuration issued a request")
						return nil, nil
					})}
				}
				value, err := loader.load(test.client, test.ctx)
				checkCapabilityFailure(t, loader, value, err, test.code, test.retryable, test.diagnostic)
			})
		}
	}
}

func TestHubCapabilityLoadersRequestLifecycle(t *testing.T) {
	for _, loader := range capabilityLoaders {
		t.Run(loader.name, func(t *testing.T) {
			base, _ := url.Parse("https://hub.example/coSlash/?ignored=1#ignored")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			requests := []*http.Request{}
			client := &Client{BaseURL: base, HTTP: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				for _, previous := range requests {
					if previous == request {
						t.Fatal("discovery reused a request")
					}
				}
				requests = append(requests, request)
				if request.Method != http.MethodGet || request.URL.String() != "https://hub.example/coSlash/.well-known/coslash-server" ||
					request.Body != nil || request.Header.Get("Authorization") != "" || request.Context() != ctx {
					t.Fatalf("discovery request=%#v", request)
				}
				if len(requests) == 3 {
					return nil, errors.New("discovery transport failed")
				}
				if len(requests) == 4 {
					cancel()
					return nil, request.Context().Err()
				}
				return response(http.StatusOK, hubCapabilitiesJSON), nil
			})}}
			for range 2 {
				value, err := loader.load(client, ctx)
				if err != nil || !reflect.DeepEqual(value, loader.want) {
					t.Fatalf("value=%#v error=%v", value, err)
				}
			}
			value, err := loader.load(client, ctx)
			checkCapabilityFailure(t, loader, value, err, "network_unavailable", true, "discovery transport failed")
			value, err = loader.load(client, ctx)
			checkCapabilityFailure(t, loader, value, err, "network_unavailable", true, "context canceled")
			if len(requests) != 4 {
				t.Fatalf("requests=%d", len(requests))
			}
		})
	}
}

func TestHubCapabilityLoadersHTTPFailures(t *testing.T) {
	for _, loader := range capabilityLoaders {
		for _, status := range []int{201, 204, 301, 307, 400, 401, 403, 404, 408, 429, 500, 503, 600} {
			t.Run(fmt.Sprintf("%s/%d", loader.name, status), func(t *testing.T) {
				body := &capabilityBody{reader: strings.NewReader("not JSON")}
				base, _ := url.Parse("https://hub.example")
				calls := 0
				client := &Client{BaseURL: base, HTTP: &http.Client{
					CheckRedirect: func(*http.Request, []*http.Request) error { t.Fatal("followed discovery redirect"); return nil },
					Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
						calls++
						return &http.Response{StatusCode: status, Header: http.Header{"Location": {"https://other.example/discovery"}}, Body: body}, nil
					}),
				}}
				code, retryable := "incompatible_server", false
				if status >= 500 || status == 408 || status == 429 {
					code, retryable = "temporary_unavailable", true
				}
				value, err := loader.load(client, context.Background())
				checkCapabilityFailure(t, loader, value, err, code, retryable, fmt.Sprintf("Hub capability request returned %d", status))
				if body.closes != 1 || body.bytesRead != 0 || calls != 1 {
					t.Fatalf("closes=%d bytesRead=%d requests=%d", body.closes, body.bytesRead, calls)
				}
			})
		}
	}
}

func TestHubCapabilityLoadersDecodeBoundaries(t *testing.T) {
	const limit = 1 << 20
	readFailure := errors.New("discovery read failed")
	for _, loader := range capabilityLoaders {
		for _, test := range []struct {
			name       string
			reader     io.Reader
			closeErr   error
			diagnostic string
		}{
			{name: "valid", reader: strings.NewReader(hubCapabilitiesJSON)},
			{name: "close error ignored", reader: strings.NewReader(hubCapabilitiesJSON), closeErr: errors.New("close failed")},
			{name: "malformed", reader: strings.NewReader(`{`), diagnostic: "unexpected EOF"},
			{name: "unknown", reader: strings.NewReader(capabilityJSONWith(t, map[string]any{"unknown": 1})), diagnostic: `unknown field "unknown"`},
			{name: "trailing value", reader: strings.NewReader(hubCapabilitiesJSON + `{}`), diagnostic: "multiple JSON values"},
			{name: "trailing garbage", reader: strings.NewReader(hubCapabilitiesJSON + `x`), diagnostic: "multiple JSON values"},
			{name: "read error", reader: iotest.ErrReader(readFailure), diagnostic: readFailure.Error()},
			{name: "canceled read", reader: iotest.ErrReader(context.Canceled), diagnostic: "context canceled"},
			{name: "second read error", reader: io.MultiReader(strings.NewReader(hubCapabilitiesJSON), iotest.ErrReader(readFailure)), diagnostic: "multiple JSON values"},
			{name: "integer overflow", reader: strings.NewReader(strings.Replace(hubCapabilitiesJSON, `"maxSnapshotBytes":262144`, `"maxSnapshotBytes":9223372036854775808`, 1)), diagnostic: "cannot unmarshal number"},
			{name: "exact limit", reader: strings.NewReader(strings.Repeat(" ", limit-len(hubCapabilitiesJSON)) + hubCapabilitiesJSON)},
			{name: "incomplete at limit", reader: strings.NewReader(strings.Repeat(" ", limit-len(hubCapabilitiesJSON)+1) + hubCapabilitiesJSON), diagnostic: "unexpected EOF"},
			{name: "unconsumed trailer beyond limit", reader: strings.NewReader(hubCapabilitiesJSON + strings.Repeat(" ", limit-len(hubCapabilitiesJSON)) + `{}`)},
		} {
			t.Run(loader.name+"/"+test.name, func(t *testing.T) {
				body := &capabilityBody{reader: test.reader, closeErr: test.closeErr}
				base, _ := url.Parse("https://hub.example")
				client := &Client{BaseURL: base, HTTP: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
				})}}
				value, err := loader.load(client, context.Background())
				if test.diagnostic != "" {
					checkCapabilityFailure(t, loader, value, err, "temporary_unavailable", true, test.diagnostic)
				} else if err != nil || !reflect.DeepEqual(value, loader.want) {
					t.Fatalf("value=%#v error=%v", value, err)
				}
				if body.closes != 1 || body.bytesRead > limit {
					t.Fatalf("closes=%d bytesRead=%d", body.closes, body.bytesRead)
				}
			})
		}
	}
}

func TestHubCapabilityLoadersFieldTypes(t *testing.T) {
	var fields map[string]any
	if err := json.Unmarshal([]byte(hubCapabilitiesJSON), &fields); err != nil {
		t.Fatal(err)
	}
	fieldNames := make([]string, 0, len(fields))
	for field := range fields {
		fieldNames = append(fieldNames, field)
	}
	slices.Sort(fieldNames)
	for _, loader := range capabilityLoaders {
		for _, field := range fieldNames {
			t.Run(loader.name+"/"+field, func(t *testing.T) {
				base, _ := url.Parse("https://hub.example")
				client := &Client{BaseURL: base, HTTP: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					return response(http.StatusOK, capabilityJSONWith(t, map[string]any{field: map[string]any{}})), nil
				})}}
				value, err := loader.load(client, context.Background())
				checkCapabilityFailure(t, loader, value, err, "temporary_unavailable", true, "cannot unmarshal object")
			})
		}
	}
}

func TestHubCapabilityLoadersProtocolRequirements(t *testing.T) {
	for _, loader := range capabilityLoaders {
		required := []string{"product", "protocolVersions", "fullSessionVersions", "maxFullSessionBytes", "maxRequestBytes"}
		diagnostic := "Hub does not advertise compatible full-session v2 support"
		if loader.name == "backup" {
			required = []string{"product", "serverId", "protocolVersions", "backupVersions", "backupUploadVersions", "maxBackupBytes", "maxBackupChunkBytes", "backupWorkspaceBytes", "backupUploadExpiresSeconds"}
			diagnostic = "Hub does not advertise compatible complete-backup support"
		}
		for _, field := range []string{"product", "serverId", "displayName", "protocolVersions", "snapshotVersions", "maxSnapshotBytes", "fullSessionVersions", "maxFullSessionBytes", "maxRequestBytes", "backupVersions", "backupUploadVersions", "maxBackupBytes", "maxBackupChunkBytes", "backupWorkspaceBytes", "backupUploadExpiresSeconds", "pairingUrl", "teamUrl"} {
			for _, replacement := range []string{"omitted", "null", "invalid"} {
				t.Run(loader.name+"/"+field+"/"+replacement, func(t *testing.T) {
					body := capabilityJSONWith(t, nil, field)
					if replacement != "omitted" {
						var value any
						if replacement != "null" {
							switch {
							case strings.Contains(field, "Versions"):
								value = []string{"unsupported"}
							case strings.HasPrefix(field, "max") || field == "backupWorkspaceBytes" || field == "backupUploadExpiresSeconds":
								value = -1
							default:
								value = "unsupported"
								if field == "serverId" {
									value = ""
								}
							}
						}
						body = capabilityJSONWith(t, map[string]any{field: value})
					}
					base, _ := url.Parse("https://hub.example")
					client := &Client{BaseURL: base, HTTP: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
						return response(http.StatusOK, body), nil
					})}}
					value, err := loader.load(client, context.Background())
					if slices.Contains(required, field) {
						checkCapabilityFailure(t, loader, value, err, "incompatible_server", false, diagnostic)
					} else if err != nil {
						t.Fatalf("optional field %s rejected: %v", field, err)
					}
				})
			}
		}
		minimal := `{"product":"coslash-server","protocolVersions":["v2"],"fullSessionVersions":["full-session-record/v1"],"maxFullSessionBytes":1,"maxRequestBytes":1}`
		var minimalWant any = hubCapabilities{Product: "coslash-server", ProtocolVersions: []string{"v2"}, FullSessionVersions: []string{"full-session-record/v1"}, MaxFullSessionBytes: 1, MaxRequestBytes: 1}
		if loader.name == "backup" {
			minimal = `{"product":"coslash-server","serverId":"server","protocolVersions":["v3"],"backupVersions":["session-backup/v1"],"backupUploadVersions":["backup-upload/v1"],"maxBackupBytes":1,"maxBackupChunkBytes":1,"backupWorkspaceBytes":1,"backupUploadExpiresSeconds":1}`
			minimalWant = BackupCapability{ServerID: "server", MaxBackupBytes: 1, MaxBackupChunkBytes: 1, BackupWorkspaceBytes: 1, BackupUploadExpiresSeconds: 1}
		}
		for _, body := range []string{minimal, `{}`, `null`} {
			t.Run(loader.name+"/"+body, func(t *testing.T) {
				base, _ := url.Parse("https://hub.example")
				client := &Client{BaseURL: base, HTTP: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					return response(http.StatusOK, body), nil
				})}}
				value, err := loader.load(client, context.Background())
				if body == minimal {
					if err != nil || !reflect.DeepEqual(value, minimalWant) {
						t.Fatalf("minimal capability value=%#v error=%v", value, err)
					}
				} else {
					checkCapabilityFailure(t, loader, value, err, "incompatible_server", false, diagnostic)
				}
			})
		}
	}
}
