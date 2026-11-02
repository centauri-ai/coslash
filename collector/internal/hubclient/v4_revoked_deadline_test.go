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

// Deleting a revoked credential must outlive a cancelled request but not the
// caller's deadline, which bounds whoever is waiting on the call.
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
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
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
