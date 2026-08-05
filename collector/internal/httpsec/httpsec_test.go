package httpsec

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGuardRequests(t *testing.T) {
	tests := []struct {
		name      string
		guardAddr string
		path      string
		method    string
		host      string
		origin    string
		fetchSite string
		fetchMode string
		fetchDest string
		token     string
		auth      string
		want      int
	}{
		{name: "API by address", path: "/api/sessions", host: "127.0.0.1:8787", token: "secret", want: http.StatusOK},
		{name: "localhost navigation", path: "/api/sessions", host: "localhost:8787", fetchSite: "none", token: "secret", want: http.StatusOK},
		{name: "bearer token", path: "/api/sessions", host: "127.0.0.1:8787", auth: "Bearer secret", want: http.StatusOK},
		{name: "rebound host", path: "/api/sessions", host: "a.evil.com:8787", token: "secret", want: http.StatusForbidden},
		{name: "listener port mismatch", guardAddr: "127.0.0.1:9999", path: "/api/sessions", host: "127.0.0.1:8787", token: "secret", want: http.StatusForbidden},
		{name: "evil origin", path: "/api/sessions", host: "127.0.0.1:8787", origin: "http://evil.com", token: "secret", want: http.StatusForbidden},
		{name: "cross-site read", path: "/api/sessions", host: "127.0.0.1:8787", fetchSite: "cross-site", token: "secret", want: http.StatusForbidden},
		{name: "same-site read", path: "/api/sessions", host: "127.0.0.1:8787", fetchSite: "same-site", token: "secret", want: http.StatusForbidden},
		{name: "cross-site document navigation", path: "/", host: "127.0.0.1:8787", fetchSite: "cross-site", fetchMode: "navigate", fetchDest: "document", want: http.StatusOK},
		{name: "cross-site subresource", path: "/", host: "127.0.0.1:8787", fetchSite: "cross-site", fetchMode: "cors", fetchDest: "empty", want: http.StatusForbidden},
		{name: "wrong token", path: "/api/sessions", host: "127.0.0.1:8787", token: "wrong", want: http.StatusUnauthorized},
		{name: "missing token", path: "/api/sessions", host: "127.0.0.1:8787", want: http.StatusUnauthorized},
		{name: "document needs no token", path: "/", host: "127.0.0.1:8787", want: http.StatusOK},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			called := false
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			})
			guardAddr := test.guardAddr
			if guardAddr == "" {
				guardAddr = "127.0.0.1:8787"
			}
			method := test.method
			if method == "" {
				method = http.MethodGet
			}
			request := httptest.NewRequest(method, "http://127.0.0.1:8787"+test.path, nil)
			request.Host = test.host
			for name, value := range map[string]string{
				"Origin":          test.origin,
				"Sec-Fetch-Site":  test.fetchSite,
				"Sec-Fetch-Mode":  test.fetchMode,
				"Sec-Fetch-Dest":  test.fetchDest,
				"X-Coslash-Token": test.token,
				"Authorization":   test.auth,
			} {
				if value != "" {
					request.Header.Set(name, value)
				}
			}
			response := httptest.NewRecorder()

			Guard{Addr: guardAddr, Token: "secret"}.Wrap(next).ServeHTTP(response, request)

			if response.Code != test.want {
				t.Fatalf("status = %d, want %d", response.Code, test.want)
			}
			if called != (test.want == http.StatusOK) {
				t.Fatalf("downstream called = %t", called)
			}
			if response.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("guard emitted Access-Control-Allow-Origin")
			}
		})
	}
}

func TestGuardEmptyTokenFailsClosed(t *testing.T) {
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("unauthenticated request reached downstream handler")
	})
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8787/api/sessions", nil)
	response := httptest.NewRecorder()

	Guard{Addr: "127.0.0.1:8787"}.Wrap(next).ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func TestGuardSecurityHeaders(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	guard := Guard{Addr: "127.0.0.1:8787", Token: "secret"}.Wrap(next)

	apiRequest := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8787/api/sessions", nil)
	apiRequest.Header.Set("X-Coslash-Token", "secret")
	apiResponse := httptest.NewRecorder()
	guard.ServeHTTP(apiResponse, apiRequest)
	if got := apiResponse.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("API Cache-Control = %q", got)
	}

	documentRequest := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8787/", nil)
	documentResponse := httptest.NewRecorder()
	guard.ServeHTTP(documentResponse, documentRequest)
	if got := documentResponse.Header().Get("Content-Security-Policy"); !strings.Contains(got, "frame-ancestors 'none'") {
		t.Errorf("document Content-Security-Policy = %q", got)
	}
	for _, name := range []string{
		"X-Content-Type-Options",
		"Referrer-Policy",
		"Cross-Origin-Resource-Policy",
		"Cross-Origin-Opener-Policy",
	} {
		if got := documentResponse.Header().Get(name); got == "" {
			t.Errorf("document missing %s", name)
		}
	}
}
