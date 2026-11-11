package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/httpsec"
	"github.com/centauri-ai/coslash/collector/internal/launch"
	"github.com/centauri-ai/coslash/collector/internal/remote"
	reviewpkg "github.com/centauri-ai/coslash/collector/internal/review"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/synthesis"
	"github.com/centauri-ai/coslash/collector/internal/vendors/claude"
)

func TestHeadlessSendUsesCanonicalContextAndRejectsInvalidInput(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	var logs reviewLogBuffer
	previous := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previous) })
	name := "Canonical session"
	found := &session.Session{Agent: "codex", ID: "source", Name: &name, WorkingDirectory: t.TempDir()}
	load := func(agent, id string) (*session.Session, error) {
		if agent != "codex" || id != "source" {
			t.Fatal("source changed")
		}
		return found, nil
	}
	called := false
	send := func(_ context.Context, target, cwd, handoff, task, access string) (launch.SendResult, error) {
		called = true
		if target != "claude" || cwd != found.WorkingDirectory || !strings.Contains(handoff, name) || task != "--private task" || access != "workspace-write" {
			t.Fatal("canonical context/task/access changed")
		}
		return launch.SendResult{Output: "Findings"}, nil
	}
	query := "/api/send?agent=codex&id=source&to=claude&access=workspace-write"
	response := &sendDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	handleSend(response, httptest.NewRequest(http.MethodPost, query, strings.NewReader("--private task")), settings.Open(), load, func(string) bool { return true }, nil, send)
	var result launch.SendResult
	if response.Code != 200 || !called || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Output != "Findings" {
		t.Fatalf("send: %d %s", response.Code, response.Body.String())
	}
	for _, test := range []struct{ query, body string }{{query, " "}, {query, strings.Repeat("x", launch.MaxHandoffBytes+1)}, {query, "\xff"}, {query + "&source=remote", "task"}, {strings.Replace(query, "to=claude", "to=cursor", 1), "task"}, {strings.Replace(query, "workspace-write", "full", 1), "task"}} {
		called = false
		response = &sendDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
		handleSend(response, httptest.NewRequest(http.MethodPost, test.query, strings.NewReader(test.body)), settings.Open(), load, func(string) bool { return true }, nil, send)
		if response.Code < 400 || called {
			t.Fatalf("invalid request ran: %d", response.Code)
		}
	}
	for _, test := range []struct {
		err    error
		status int
	}{{errors.Join(launch.ErrSendBusy, errors.New("private failure")), 429}, {errors.Join(launch.ErrSendPermission, errors.New("private failure")), 409}, {errors.New("private failure\x1b\u202e"), 500}, {context.DeadlineExceeded, 504}, {launch.ErrSendInvalid, 400}, {errors.Join(launch.ErrSendInvalid, launch.ErrSendConfigTooLarge), 400}} {
		response = &sendDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
		handleSend(response, httptest.NewRequest(http.MethodPost, query, strings.NewReader("task")), settings.Open(), load, func(string) bool { return true }, nil, func(context.Context, string, string, string, string, string) (launch.SendResult, error) {
			return launch.SendResult{}, test.err
		})
		if response.Code != test.status || strings.Contains(response.Body.String(), "private failure") {
			t.Fatalf("failure: %d %s", response.Code, response.Body.String())
		}
		if errors.Is(test.err, launch.ErrSendConfigTooLarge) && !strings.Contains(response.Body.String(), "send_configuration_too_large") {
			t.Fatalf("lost configuration limit: %s", response.Body.String())
		}
	}
	if !strings.Contains(logs.String(), "private failure") || strings.ContainsAny(logs.String(), "\x1b\u202e") || !strings.Contains(logs.String(), "context deadline exceeded") {
		t.Fatalf("missing or unsafe diagnostic: %q", logs.String())
	}
}

type reviewLogBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *reviewLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}
func (b *reviewLogBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.buffer.String() }

func TestSendCLIPrintsCompletedOutputAndPreservesFlagLookingTask(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.URL.Path != "/api/send" || string(body) != "--access workspace-write" || r.Header.Get("X-Coslash-Token") != "secret" || r.URL.Query().Has("access") {
			t.Error("task became an option or authentication was lost")
		}
		writeJSON(w, launch.SendResult{Output: "Findings", SessionID: "01234567-89ab-4def-8123-456789abcdef"})
	}))
	defer server.Close()
	writeTestRuntime(t, server.URL, "secret")
	var stdout, stderr bytes.Buffer
	if code := runCLI(&stdout, &stderr, []string{"send", "codex:source", "--to", "claude", "--", "--access", "workspace-write"}); code != 0 || !strings.Contains(stdout.String(), "Findings") || !strings.Contains(stdout.String(), "claude:01234567") {
		t.Fatalf("CLI: %d %s %s", code, stdout.String(), stderr.String())
	}
}

func TestServerShutdownCancelsHeadlessSend(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("COSLASH_HOME", t.TempDir())
	bin := t.TempDir()
	name := "claude"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(bin, name), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	const id = "11111111-2222-3333-4444-555555555555"
	projects := filepath.Join(claude.ProjectsRoot(home), "test")
	if err := os.MkdirAll(projects, 0o700); err != nil {
		t.Fatal(err)
	}
	row, _ := json.Marshal(map[string]any{"sessionId": id, "uuid": "user", "cwd": t.TempDir(), "timestamp": "2026-11-10T00:00:00Z", "type": "user", "message": map[string]string{"content": "Fixture task"}})
	if err := os.WriteFile(filepath.Join(projects, id+".jsonl"), append(row, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	cancelled := make(chan struct{})
	send := func(ctx context.Context, _, _, _, _, _ string) (launch.SendResult, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return launch.SendResult{}, ctx.Err()
	}
	ts := httptest.NewUnstartedServer(nil)
	server := newServer(httpsec.Guard{Addr: ts.Listener.Addr().String(), Token: "secret"}, synthesis.NewManager(nil, nil), reviewpkg.NewManager(nil), settings.Open(), remote.NewManager(remote.Options{}), nil, serverServices{headlessSend: send})
	ts.Config = server
	ts.Start()
	defer ts.Close()
	done := make(chan error, 1)
	go func() {
		request, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/send?agent=claude&id="+id+"&to=claude", strings.NewReader("task"))
		request.Header.Set("X-Coslash-Token", "secret")
		response, err := ts.Client().Do(request)
		if err == nil {
			_ = response.Body.Close()
		}
		done <- err
	}()
	<-started
	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-cancelled
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSendCLIDistinguishesHTTPAndTransportFailures(t *testing.T) {
	for _, failure := range []string{"http", "disconnect", "partial-response"} {
		t.Run(failure, func(t *testing.T) {
			var posts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				posts.Add(1)
				if failure != "http" {
					connection, writer, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					if failure == "partial-response" {
						_, _ = writer.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 50\r\n\r\n{")
						_ = writer.Flush()
					}
					_ = connection.Close()
					return
				}
				http.Error(w, "busy", http.StatusTooManyRequests)
			}))
			defer server.Close()
			writeTestRuntime(t, server.URL, "secret")
			var stdout bytes.Buffer
			err := runSend(&stdout, []string{"codex:source", "--to", "claude", "task"})
			if err == nil || posts.Load() != 1 {
				t.Fatalf("request was retried: %d %v", posts.Load(), err)
			}
			if failure == "http" && err.Error() != "busy" {
				t.Fatalf("HTTP rejection given ambiguous-result advice: %v", err)
			}
			if failure != "http" && runtime.GOOS == "linux" && !strings.Contains(err.Error(), "do not automatically repeat") {
				t.Fatalf("transport advice missing: %v", err)
			}
			if failure == "partial-response" && !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatalf("read cause was lost: %v", err)
			}
		})
	}
}
