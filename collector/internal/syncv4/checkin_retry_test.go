package syncv4

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
)

func TestRejectedCheckInBackoffBoundsRequestsAndResetsAfterSuccess(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound, http.StatusUnprocessableEntity} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
			responseStatus := atomic.Int32{}
			responseStatus.Store(int32(status))
			requests := atomic.Int32{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/v4/devices/me/check-in" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				if responseStatus.Load() == http.StatusOK {
					_, _ = io.WriteString(w, `{"configVersion":1,"config":{"paused":false,"deviceOff":false,"leaveOut":[],"agentKnowledge":true},"minVersion":"0.0.3"}`)
					return
				}
				w.WriteHeader(int(responseStatus.Load()))
				_, _ = io.WriteString(w, `{"code":"invalid_query"}`)
			}))
			defer server.Close()
			baseURL, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			queue, err := Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			runner := &Runner{
				Queue: queue,
				Hub:   &hubclient.Client{BaseURL: baseURL, Credentials: fixedCredential{}, CollectorVersion: "0.0.5"},
				Now:   func() time.Time { return now },
			}

			for _, wantRequests := range []int32{1, 2, 3} {
				err := runner.refreshConsent(context.Background())
				if !errors.Is(err, ErrStaleConsent) {
					t.Fatalf("rejected check-in error = %v", err)
				}
				var problem hubclient.V4Problem
				if !errors.As(err, &problem) || problem.HTTPStatus != status {
					t.Fatalf("rejected check-in problem = %+v, err=%v", problem, err)
				}
				if got := requests.Load(); got != wantRequests {
					t.Fatalf("requests after attempt %d = %d, want %d", wantRequests, got, wantRequests)
				}
				wantBackoff := checkInRejectionBackoff(int(wantRequests))
				if problem.RetryAfter != wantBackoff {
					t.Fatalf("attempt %d retry-after = %s, want %s", wantRequests, problem.RetryAfter, wantBackoff)
				}
				if wantRequests == 1 && DeferReason(err) != "hub_rejected:invalid_query" {
					t.Fatalf("visible rejection reason = %q", DeferReason(err))
				}

				// Repeated sync passes during the same interval are rejected locally;
				// only the simulated proxy's counter observes an HTTP request.
				for range 20 {
					blocked := runner.refreshConsent(context.Background())
					if !errors.Is(blocked, ErrStaleConsent) {
						t.Fatalf("blocked retry error = %v", blocked)
					}
					if got := requests.Load(); got != wantRequests {
						t.Fatalf("requests inside backoff = %d, want %d", got, wantRequests)
					}
				}
				if wantRequests < 3 {
					now = now.Add(wantBackoff)
				}
			}

			responseStatus.Store(http.StatusOK)
			now = now.Add(checkInRejectionBackoff(3))
			if err := runner.refreshConsent(context.Background()); err != nil {
				t.Fatalf("successful check-in after rejection: %v", err)
			}
			if got := requests.Load(); got != 4 {
				t.Fatalf("requests after recovery = %d, want 4", got)
			}

			responseStatus.Store(int32(status))
			err = runner.refreshConsent(context.Background())
			var problem hubclient.V4Problem
			if !errors.As(err, &problem) || problem.RetryAfter != time.Minute {
				t.Fatalf("backoff after success = %+v, err=%v; want one minute", problem, err)
			}
			if got := requests.Load(); got != 5 {
				t.Fatalf("requests after reset = %d, want 5", got)
			}
		})
	}
}

func TestCheckInRejectionBackoffCapsAtOneHour(t *testing.T) {
	for _, test := range []struct {
		attempt int
		want    time.Duration
	}{{1, time.Minute}, {2, 2 * time.Minute}, {3, 4 * time.Minute}, {4, 8 * time.Minute},
		{5, 16 * time.Minute}, {6, 32 * time.Minute}, {7, time.Hour}, {8, time.Hour}, {100, time.Hour}} {
		if got := checkInRejectionBackoff(test.attempt); got != test.want {
			t.Errorf("backoff(%d) = %s, want %s", test.attempt, got, test.want)
		}
	}
}

func TestRetryableCheckInFailuresKeepTheHeartbeatCadence(t *testing.T) {
	for _, problem := range []hubclient.V4Problem{
		{Code: "unauthorized", HTTPStatus: http.StatusUnauthorized},
		{Code: "temporary_unavailable", HTTPStatus: http.StatusServiceUnavailable},
		{Code: "timeout", HTTPStatus: http.StatusRequestTimeout},
		{Code: "rate_limited", HTTPStatus: http.StatusTooManyRequests},
	} {
		runner := &Runner{Now: func() time.Time { return time.Unix(1_792_000_000, 0) }}
		err := runner.recordCheckInFailure(problem)
		if err != problem {
			t.Fatalf("retryable problem changed: got %v, want %v", err, problem)
		}
		if until := runner.checkInRetryUntil.Load(); until != 0 {
			t.Errorf("retryable problem set permanent rejection deadline: %d", until)
		}
		if got := runner.checkInRejections.Load(); got != 0 {
			t.Errorf("retryable problem incremented rejection count to %d", got)
		}
	}
}
