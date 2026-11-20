package hubclient

import (
	"context"
	"errors"
	"io"

	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

type memoryCredentials struct {
	saved   string
	deleted bool
}

type contextCredentials struct {
	saved  string
	cancel context.CancelFunc
}

type failOnceCredentials struct {
	saved    string
	attempts int
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

func (s *memoryCredentials) Load(context.Context) (string, error) {
	if s.deleted {
		return "", ErrNotPaired
	}
	return "credential", nil
}

func (s *memoryCredentials) Save(_ context.Context, credential string) error {
	s.saved = credential
	s.deleted = false
	return nil
}

func (s *memoryCredentials) Delete(context.Context) error {
	s.deleted = true
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

func (s *contextCredentials) Delete(context.Context) error { return nil }

func (s *failOnceCredentials) Load(context.Context) (string, error) { return "credential", nil }

func (s *failOnceCredentials) Save(_ context.Context, credential string) error {
	s.attempts++
	if s.attempts == 1 {
		return errors.New("keychain unavailable")
	}
	s.saved = credential
	return nil
}

func (s *failOnceCredentials) Delete(context.Context) error { return nil }

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

func TestPollPairingRetriesKeychainSaveWithoutExchangingCredentialAgain(t *testing.T) {
	credentials := &failOnceCredentials{}
	base, _ := url.Parse("https://hub.example")
	tokenRequests := 0
	client := Client{
		BaseURL: base, Credentials: credentials,
		pairings: map[string]pairingSecret{"pair": {deviceCode: "code", expiresAt: time.Now().Add(time.Minute)}},
		HTTP: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			tokenRequests++
			return response(http.StatusOK, `{"deviceId":"device","credential":"one-time-secret","tokenType":"Device","scope":"ingest"}`), nil
		})},
	}
	first, err := client.PollPairing(context.Background(), "pair")
	if err != nil || first.State != PairingStateRetrying {
		t.Fatal("first credential-store failure was not retryable")
	}
	result, err := client.PollPairing(context.Background(), "pair")
	if err != nil || result.State != PairingStatePaired || credentials.saved != "one-time-secret" ||
		credentials.attempts != 2 || tokenRequests != 1 {
		t.Fatal("credential save retry did not complete without a second exchange")
	}
}
