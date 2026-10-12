package hubclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/url"
	"os"
	"strings"
	"syscall"
	"testing"
)

func TestClassifyHubFailure(t *testing.T) {
	tests := []struct {
		name  string
		err   error
		cause string
		text  string
	}{
		{name: "dns name not found", err: &net.DNSError{Err: "no such host", Name: "hub.example.test", IsNotFound: true}, cause: FailureDNS},
		{name: "connection refused", err: &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}, cause: FailureConnect},
		{name: "connection timeout", err: &net.OpError{Op: "dial", Net: "tcp", Err: os.ErrDeadlineExceeded}, cause: FailureConnect, text: "Connection timed out."},
		{name: "request deadline", err: context.DeadlineExceeded, cause: FailureConnect, text: "Connection timed out."},
		{name: "no route", err: &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ENETUNREACH}, cause: FailureConnect},
		{name: "unknown certificate authority", err: x509.UnknownAuthorityError{}, cause: FailureTLS},
		{name: "tls alert", err: tls.AlertError(40), cause: FailureTLS},
		{name: "proxy connect", err: &url.Error{Op: "Post", URL: "https://hub.example.test", Err: errors.New("proxyconnect tcp: proxy returned 502")}, cause: FailureProxy},
		{name: "proxy auth required", err: V4Problem{Code: "proxy_auth_required", HTTPStatus: 407}, cause: FailureProxy},
		{name: "service unavailable", err: V4Problem{Code: "maintenance", HTTPStatus: 503}, cause: FailureHubUnavailable},
		{name: "rate limited", err: V4Problem{Code: "rate_limited", HTTPStatus: 429}, cause: FailureHubUnavailable},
		{name: "problem coded forbidden", err: V4Problem{Code: "device_removed", HTTPStatus: 403}, cause: FailureRejected, text: "no longer paired"},
		{name: "problem coded upgrade required", err: V4Problem{Code: "client_update_required", HTTPStatus: 426}, cause: FailureRejected, text: "version is too old"},
		{name: "uncoded client error", err: V4Problem{Code: "http_403", HTTPStatus: 403}},
		{name: "canceled", err: errors.New("context canceled")},
		{name: "local error", err: errors.New("queue write failed")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			failure, ok := ClassifyHubFailure(test.err)
			if test.cause == "" {
				if ok {
					t.Fatalf("unexpected failure classification: %+v", failure)
				}
				return
			}
			if !ok || failure.Cause != test.cause {
				t.Fatalf("ClassifyHubFailure() = %+v, %t; want cause %q", failure, ok, test.cause)
			}
			if test.text != "" && !strings.Contains(failure.Message, test.text) {
				t.Fatalf("message = %q, want %q", failure.Message, test.text)
			}
		})
	}
}

func TestClassifyHubContactSuccess(t *testing.T) {
	failure, ok := ClassifyHubContact(nil)
	if !ok || failure.Cause != FailureOK || failure.Message != "Hub contact succeeded." {
		t.Fatalf("ClassifyHubContact(nil) = %+v, %t", failure, ok)
	}
}

func TestClassifyHubFailureRedactsSecrets(t *testing.T) {
	err := &url.Error{Op: "Get", URL: "https://user:password@hub.example.test/v4?token=query-secret", Err: errors.New("proxyconnect tcp via https://user:password@proxy.example.test/?token=query-secret Authorization: Device auth-header-secret token=header-secret Device abcdef123456")}
	failure, ok := ClassifyHubFailure(err)
	if !ok || failure.Cause != FailureProxy {
		t.Fatalf("classification = %+v, %t", failure, ok)
	}
	for _, secret := range []string{"password", "query-secret", "auth-header-secret", "header-secret", "abcdef123456"} {
		if strings.Contains(failure.RawError, secret) {
			t.Fatalf("raw error leaked %q: %q", secret, failure.RawError)
		}
	}
	if failure.RawError == "" {
		t.Fatal("raw error is empty")
	}
}
