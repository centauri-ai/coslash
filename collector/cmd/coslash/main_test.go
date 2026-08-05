package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSameOrigin(t *testing.T) {
	for _, test := range []struct {
		name           string
		host           string
		secFetchSite   string
		origin         string
		referer        string
		wantStatusCode int
	}{
		{"same origin", "127.0.0.1:8787", "same-origin", "", "", http.StatusNoContent},
		{"fetch metadata is authoritative", "127.0.0.1:8787", "same-origin", "http://localhost:5173", "", http.StatusNoContent},
		{"direct navigation", "localhost:8787", "none", "", "", http.StatusNoContent},
		{"case-insensitive localhost", "LOCALHOST:8787", "same-origin", "", "", http.StatusNoContent},
		{"non-browser caller", "127.0.0.1:8787", "", "", "", http.StatusNoContent},
		{"origin fallback", "127.0.0.1:8787", "", "http://127.0.0.1:8787", "", http.StatusNoContent},
		{"referer fallback", "localhost:8787", "", "", "http://localhost:8787/app?id=1", http.StatusNoContent},
		{"cross-origin origin", "127.0.0.1:8787", "", "https://example.com", "", http.StatusForbidden},
		{"cross-origin referer", "127.0.0.1:8787", "", "", "https://example.com/app", http.StatusForbidden},
		{"origin takes precedence", "127.0.0.1:8787", "", "https://example.com", "http://127.0.0.1:8787/app", http.StatusForbidden},
		{"malformed origin", "127.0.0.1:8787", "", "http://127.0.0.1:8787/path", "", http.StatusForbidden},
		{"cross site", "127.0.0.1:8787", "cross-site", "", "", http.StatusForbidden},
		{"same site", "127.0.0.1:8787", "same-site", "", "", http.StatusForbidden},
		{"rebound host", "example.com:8787", "same-origin", "", "", http.StatusForbidden},
		{"wrong port", "localhost:5173", "same-origin", "", "", http.StatusForbidden},
		{"missing port", "localhost", "same-origin", "", "", http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8787/api/sessions", nil)
			request.Host = test.host
			if test.secFetchSite != "" {
				request.Header.Set("Sec-Fetch-Site", test.secFetchSite)
			}
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			if test.referer != "" {
				request.Header.Set("Referer", test.referer)
			}

			recorder := httptest.NewRecorder()
			sameOrigin(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}), 8787).ServeHTTP(recorder, request)

			if recorder.Code != test.wantStatusCode {
				t.Errorf("status = %d, want %d", recorder.Code, test.wantStatusCode)
			}
		})
	}
}
