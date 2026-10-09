package main

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
)

type hubTransportTestCredentials struct{}

func (hubTransportTestCredentials) Load(context.Context) (string, error) { return "credential", nil }
func (hubTransportTestCredentials) Save(context.Context, string) error   { return nil }
func (hubTransportTestCredentials) Delete(context.Context) error         { return nil }

func TestCurrentHubTransportV4Abort(t *testing.T) {
	client := &hubclient.Client{Credentials: hubTransportTestCredentials{}, HTTP: &http.Client{Transport: onboardingRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodDelete || r.URL.Path != "/v4/uploads/legacy-upload" || r.Header.Get("Authorization") != "Device credential" {
			t.Errorf("abort request = %s %s auth=%q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		return onboardingResponse(http.StatusNoContent, ""), nil
	})}}
	baseURL, err := url.Parse("https://hub.example")
	if err != nil {
		t.Fatal(err)
	}
	client.BaseURL = baseURL
	transport := currentHubTransport{current: func() *hubclient.Client { return client }}

	if err := transport.V4Abort(context.Background(), "legacy-upload"); err != nil {
		t.Fatal(err)
	}
}
