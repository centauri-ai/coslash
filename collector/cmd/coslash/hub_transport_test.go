package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/syncv4"
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

func TestCurrentHubTransportTracksFailureAndSuccessfulRecovery(t *testing.T) {
	queue, err := syncv4.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	client := &hubclient.Client{Credentials: hubTransportTestCredentials{}, HTTP: &http.Client{Transport: onboardingRoundTripper(func(*http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
		}
		return onboardingResponse(http.StatusNoContent, ""), nil
	})}}
	baseURL, err := url.Parse("https://hub.example")
	if err != nil {
		t.Fatal(err)
	}
	client.BaseURL = baseURL
	transport := currentHubTransport{current: func() *hubclient.Client { return client }, queue: queue}
	if err := transport.V4Abort(t.Context(), "synthetic-upload"); err == nil {
		t.Fatal("refused Hub contact unexpectedly succeeded")
	}
	failure := queue.LastHubFailure()
	if failure == nil || failure.Cause != hubclient.FailureConnect || failure.Attempts != 1 {
		t.Fatalf("tracked failure = %+v", failure)
	}
	if err := transport.V4Abort(t.Context(), "synthetic-upload"); err != nil {
		t.Fatalf("successful recovery request: %v", err)
	}
	if failure := queue.LastHubFailure(); failure != nil {
		t.Fatalf("successful Hub contact did not clear failure: %+v", failure)
	}
}

func TestCurrentHubTransportPersistsRevokedWaitAsNotPaired(t *testing.T) {
	root := t.TempDir()
	queue, err := syncv4.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	client := &hubclient.Client{Credentials: hubTransportTestCredentials{}, HTTP: &http.Client{Transport: onboardingRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/v4/devices/me/wait" {
			t.Errorf("wait request = %s %s", r.Method, r.URL.Path)
		}
		return onboardingResponse(http.StatusForbidden, `{"code":"device_revoked"}`), nil
	})}}
	baseURL, err := url.Parse("https://hub.example")
	if err != nil {
		t.Fatal(err)
	}
	client.BaseURL = baseURL
	transport := currentHubTransport{current: func() *hubclient.Client { return client }, queue: queue}
	if _, err := transport.V4Wait(t.Context(), 0); err == nil {
		t.Fatal("revoked wait unexpectedly succeeded")
	} else {
		var problem hubclient.V4Problem
		if !errors.As(err, &problem) || problem.Code != "device_revoked" {
			t.Fatalf("wait error = %v, want device_revoked", err)
		}
	}
	health, err := syncv4.ReadHealth(root)
	if err != nil {
		t.Fatal(err)
	}
	if health.LastCheckInResult != "not_paired" {
		t.Fatalf("last check-in result = %q, want not_paired", health.LastCheckInResult)
	}
}
