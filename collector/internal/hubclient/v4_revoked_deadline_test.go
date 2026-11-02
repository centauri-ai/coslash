package hubclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

type hangingDeleteCredentials struct{}

func (hangingDeleteCredentials) Load(context.Context) (string, error) { return "cred", nil }
func (hangingDeleteCredentials) Save(context.Context, string) error   { return nil }
func (hangingDeleteCredentials) Delete(context.Context) error         { return nil }
func (hangingDeleteCredentials) DeleteIfMatches(ctx context.Context, _ string) (bool, error) {
	<-ctx.Done()
	return false, ctx.Err()
}

// A caller that waits on the cleanup marks its context; deleting a revoked
// credential then outlives a cancelled request but not that caller's deadline.
func TestRevokedCredentialDeleteKeepsTheCallersDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code":"device_revoked"}`))
	}))
	defer server.Close()
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{BaseURL: base, Credentials: hangingDeleteCredentials{}}
	ctx, cancel := context.WithTimeout(WithBoundedCleanup(context.Background()), 200*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := client.V4ReportUpdate(ctx, V4CommandResult{CommandID: "id", Result: "failed", Error: "update_failed"})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("revoked reply returned no error")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("revoked-credential delete ignored the caller's deadline")
	}
}

type deadlineRecordingCredentials struct {
	hasDeadline, cancelled chan bool
}

func (deadlineRecordingCredentials) Load(context.Context) (string, error) { return "cred", nil }
func (deadlineRecordingCredentials) Save(context.Context, string) error   { return nil }
func (deadlineRecordingCredentials) Delete(context.Context) error         { return nil }
func (c deadlineRecordingCredentials) DeleteIfMatches(ctx context.Context, _ string) (bool, error) {
	_, has := ctx.Deadline()
	c.hasDeadline <- has
	c.cancelled <- ctx.Err() != nil
	return true, nil
}

// An ordinary request's deadline, or its cancellation, must not cut a revoked
// credential's cleanup short: only a caller that marked its context gets that.
func TestRevokedCredentialDeleteIsIndependentOfAnOrdinaryRequestDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code":"device_revoked"}`))
	}))
	defer server.Close()
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name         string
		mark         bool
		wantDeadline bool
	}{{"ordinary", false, false}, {"marked", true, true}} {
		t.Run(test.name, func(t *testing.T) {
			credentials := deadlineRecordingCredentials{hasDeadline: make(chan bool, 1), cancelled: make(chan bool, 1)}
			client := &Client{BaseURL: base, Credentials: credentials}
			parent := context.Background()
			if test.mark {
				parent = WithBoundedCleanup(parent)
			}
			ctx, cancel := context.WithTimeout(parent, time.Hour)
			defer cancel()
			if _, err := client.V4ReportUpdate(ctx, V4CommandResult{CommandID: "id", Result: "failed", Error: "update_failed"}); err == nil {
				t.Fatal("revoked reply returned no error")
			}
			if got := <-credentials.hasDeadline; got != test.wantDeadline {
				t.Fatalf("cleanup context has deadline = %v, want %v", got, test.wantDeadline)
			}
			if <-credentials.cancelled {
				t.Fatal("cleanup context was already cancelled")
			}
		})
	}
}
