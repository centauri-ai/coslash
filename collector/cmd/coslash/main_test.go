package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseOptions(t *testing.T) {
	for _, test := range []struct {
		name      string
		arguments []string
		want      options
	}{
		{"defaults", nil, options{port: defaultPort}},
		{"port", []string{"--port", "9000"}, options{port: 9000}},
		{"single dash", []string{"-port=9000"}, options{port: 9000}},
		{"no-open", []string{"--no-open"}, options{port: defaultPort, noOpen: true}},
		{"version", []string{"--version"}, options{port: defaultPort, showVersion: true}},
		{"combined", []string{"--port", "9000", "--no-open"}, options{port: 9000, noOpen: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			opts, err := parseOptions(test.arguments)
			if err != nil {
				t.Fatalf("parseOptions(%q): %v", test.arguments, err)
			}
			if opts != test.want {
				t.Errorf("parseOptions(%q) = %+v, want %+v", test.arguments, opts, test.want)
			}
		})
	}
}

func TestParseOptionsRejectsBadInput(t *testing.T) {
	for _, test := range []struct {
		name      string
		arguments []string
		want      string
	}{
		{"port zero", []string{"--port", "0"}, "--port must be between"},
		{"port too large", []string{"--port", "70000"}, "--port must be between"},
		{"port not a number", []string{"--port", "http"}, "invalid value"},
		{"unknown flag", []string{"--open"}, "flag provided but not defined"},
		{"stray argument", []string{"start"}, `unexpected argument "start"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parseOptions(test.arguments); err == nil {
				t.Fatalf("parseOptions(%q) succeeded, want an error", test.arguments)
			} else if !strings.Contains(err.Error(), test.want) {
				t.Errorf("error = %q, want it to contain %q", err, test.want)
			}
		})
	}
}

func TestAPIRoutesTakePrecedenceOverTheFrontend(t *testing.T) {
	mux := routes(nil)
	for _, test := range []struct {
		method string
		target string
		want   string
	}{
		{http.MethodGet, "/api/sessions", "/api/sessions"},
		{http.MethodGet, "/api/synthesis?id=abc", "GET /api/synthesis"},
		{http.MethodPost, "/api/launch?id=abc", "POST /api/launch"},
		{http.MethodGet, "/api/unrouted", "/api/"},
		{http.MethodGet, "/coslash", "/"},
		{http.MethodGet, "/assets/index-DDHjEZlF.js", "/"},
	} {
		// Match the pattern rather than serving: the API handlers would scan the
		// whole machine for sessions.
		_, pattern := mux.Handler(httptest.NewRequest(test.method, test.target, nil))
		if pattern != test.want {
			t.Errorf("%s %s matched %q, want %q", test.method, test.target, pattern, test.want)
		}
	}
}

func TestUnroutedAPIPathsAreNotFound(t *testing.T) {
	recorder := httptest.NewRecorder()
	routes(nil).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/unrouted", nil))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", recorder.Code)
	}
}

func TestListenReportsAPortConflict(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer held.Close()
	port := held.Addr().(*net.TCPAddr).Port

	if _, err := listen(port); err == nil {
		t.Fatalf("listen(%d) succeeded on a held port, want an error", port)
	} else if !strings.Contains(err.Error(), "already in use") || !strings.Contains(err.Error(), "--port") {
		t.Errorf("error = %q, want it to report the conflict and suggest --port", err)
	}
}

func TestListenBindsLoopbackOnly(t *testing.T) {
	listener, err := listen(0)
	if err != nil {
		t.Fatalf("listen(0): %v", err)
	}
	defer listener.Close()
	if host := listener.Addr().(*net.TCPAddr).IP.String(); host != "127.0.0.1" {
		t.Errorf("bound to %s, want 127.0.0.1", host)
	}
}
