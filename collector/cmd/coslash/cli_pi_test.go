package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLocalPiCLISelectorsAndSend(t *testing.T) {
	const id = "../custom id:opaque"
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path)
		if r.Header.Get("X-Coslash-Token") != "secret" {
			t.Error("missing local authentication")
		}
		switch r.URL.Path {
		case "/api/sessions":
			if r.URL.Query().Has("id") && (r.URL.Query().Get("agent") != "pi" || r.URL.Query().Get("id") != id) {
				t.Error("exact Pi selector changed")
			}
			io.WriteString(w, `[{"agent":"pi","id":"../custom id:opaque","mtime":300}]`)
		case "/api/handoff":
			if r.URL.Query().Get("agent") != "pi" || r.URL.Query().Get("id") != id {
				t.Error("Pi opaque source identity changed")
			}
			io.WriteString(w, "# Synthetic Pi handoff")
		case "/api/send":
			if r.Method != http.MethodPost || r.URL.Query().Get("to") != "pi" || r.URL.Query().Get("id") != id {
				t.Error("Pi send target or source identity changed")
			}
			body, _ := io.ReadAll(r.Body)
			if string(body) != "private task" {
				t.Error("task changed")
			}
			w.WriteHeader(http.StatusNoContent)
		case "/api/reviews":
			if r.Method != http.MethodPost || r.URL.Query().Get("reviewer") != "pi" || r.URL.Query().Get("agent") != "pi" || r.URL.Query().Get("id") != id {
				t.Error("Pi review target or opaque source identity changed")
			}
			w.WriteHeader(http.StatusAccepted)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	}))
	defer server.Close()
	writeTestRuntime(t, server.URL, "secret")
	for _, args := range [][]string{{"sessions", "--agent", "pi", "--recent", "1", "--json"}, {"sessions", "pi:" + id, "--json"}, {"handoff", "pi:" + id}, {"send", "pi:" + id, "--to", "pi", "private task"}} {
		var stdout, stderr bytes.Buffer
		if code := runCLI(&stdout, &stderr, args); code != 0 {
			t.Fatalf("%q: code=%d stderr=%s", args, code, stderr.String())
		}
		if stdout.Len() == 0 || (args[0] == "sessions" && !strings.Contains(stdout.String(), "pi:"+id)) {
			t.Fatalf("%q: missing success output", args)
		}
	}
	if len(requests) != 4 {
		t.Fatalf("requests=%v", requests)
	}
	var stdout, stderr bytes.Buffer
	if code := runCLI(&stdout, &stderr, []string{"review", "pi:" + id, "--with", "pi"}); code != 0 || len(requests) != 5 || !strings.Contains(stdout.String(), "Success: started pi review") {
		t.Fatalf("Pi review failed: %s", stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := runCLI(&stdout, &stderr, []string{"review", "pi:" + id, "--with", "invalid"}); code == 0 || !strings.Contains(stderr.String(), "--with must be") || len(requests) != 5 {
		t.Fatal("unknown reviewer reached the API")
	}
}
