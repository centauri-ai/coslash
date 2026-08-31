package hubclient

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

type memoryCredentials struct {
	saved string
}

type contextCredentials struct {
	saved  string
	cancel context.CancelFunc
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func response(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func (s *memoryCredentials) Load(context.Context) (string, error) { return "credential", nil }

func (s *memoryCredentials) Save(_ context.Context, credential string) error {
	s.saved = credential
	return nil
}

func (s *contextCredentials) Load(context.Context) (string, error) { return "credential", nil }

func (s *contextCredentials) Save(ctx context.Context, credential string) error {
	s.cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	s.saved = credential
	return nil
}

func TestEndpointPreservesBasePath(t *testing.T) {
	base, _ := url.Parse("https://hub.example.test/coSlash/")
	client := Client{BaseURL: base}
	if got := client.endpoint("/v1/share-destination"); got != "https://hub.example.test/coSlash/v1/share-destination" {
		t.Fatalf("endpoint = %q", got)
	}
}

func TestHTTPClientRejectsRedirects(t *testing.T) {
	calls := 0
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		result := response(http.StatusTemporaryRedirect, "")
		result.Header.Set("Location", "https://other.example/upload")
		return result, nil
	})
	client := Client{HTTP: &http.Client{Transport: transport}}
	response, err := client.httpClient().Post("https://hub.example/upload", "text/plain", strings.NewReader("secret"))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusTemporaryRedirect || calls != 1 {
		t.Fatalf("status = %d, requests = %d", response.StatusCode, calls)
	}
}

func TestBeginPairingRejectsUntrustedVerificationURL(t *testing.T) {
	credentials := &memoryCredentials{}
	base, _ := url.Parse("https://hub.example")
	client := Client{
		BaseURL: base, Credentials: credentials,
		HTTP: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return response(http.StatusCreated, `{"id":"pair","deviceCode":"private","userCode":"public","verificationUri":"https://evil.example/pair","expiresAt":"2099-01-01T00:00:00Z","intervalSeconds":2}`), nil
		})},
	}
	if _, err := client.BeginPairing(context.Background()); err == nil {
		t.Fatal("untrusted verification URL was accepted")
	}
}

func TestBeginPairingRejectsDeviceCodeInVerificationURLs(t *testing.T) {
	for _, test := range []struct {
		name         string
		verification string
		complete     string
	}{
		{name: "plain", verification: "https://hub.example/pair?device_code=private"},
		{name: "complete", verification: "https://hub.example/pair", complete: "https://hub.example/pair?device_code=private"},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := `{"id":"pair","deviceCode":"private","userCode":"public","verificationUri":"` + test.verification +
				`","verificationUriComplete":"` + test.complete + `","expiresAt":"2099-01-01T00:00:00Z","intervalSeconds":2}`
			base, _ := url.Parse("https://hub.example")
			client := Client{
				BaseURL: base, Credentials: &memoryCredentials{},
				HTTP: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					return response(http.StatusCreated, body), nil
				})},
			}
			if _, err := client.BeginPairing(context.Background()); err == nil {
				t.Fatal("verification URL containing the device code was accepted")
			}
		})
	}
}

func TestPollPairingValidatesCredentialMetadata(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{name: "valid", body: `{"deviceId":"device","credential":"secret","tokenType":"Device","scope":"ingest"}`, want: "secret"},
		{name: "missing device", body: `{"deviceId":"","credential":"secret","tokenType":"Device","scope":"ingest"}`},
		{name: "wrong type", body: `{"deviceId":"device","credential":"secret","tokenType":"Bearer","scope":"ingest"}`},
		{name: "broad scope", body: `{"deviceId":"device","credential":"secret","tokenType":"Device","scope":"ingest admin"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			credentials := &memoryCredentials{}
			base, _ := url.Parse("https://hub.example")
			client := Client{
				BaseURL: base, Credentials: credentials,
				pairings: map[string]pairingSecret{"pair": {deviceCode: "code", expiresAt: time.Now().Add(time.Minute)}},
				HTTP: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					return response(http.StatusOK, test.body), nil
				})},
			}
			_, err := client.PollPairing(context.Background(), "pair")
			if test.want == "" && err == nil {
				t.Fatal("invalid credential metadata was accepted")
			}
			if test.want != "" && (err != nil || credentials.saved != test.want) {
				t.Fatalf("saved = %q, error = %v", credentials.saved, err)
			}
		})
	}
}

func TestPollPairingSavesCredentialAfterRequestCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	credentials := &contextCredentials{cancel: cancel}
	base, _ := url.Parse("https://hub.example")
	client := Client{
		BaseURL: base, Credentials: credentials,
		pairings: map[string]pairingSecret{"pair": {deviceCode: "code", expiresAt: time.Now().Add(time.Minute)}},
		HTTP: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return response(http.StatusOK, `{"deviceId":"device","credential":"secret","tokenType":"Device","scope":"ingest"}`), nil
		})},
	}

	result, err := client.PollPairing(ctx, "pair")
	if err != nil || result.State != "paired" || credentials.saved != "secret" {
		t.Fatalf("result = %#v, saved = %q, error = %v", result, credentials.saved, err)
	}
}

func TestSourceDeletedIsTerminal(t *testing.T) {
	if got := mapProblem("source_deleted"); got != "source_deleted" || retryable(got) {
		t.Fatalf("source_deleted mapped to %q, retryable = %t", got, retryable(got))
	}
}
