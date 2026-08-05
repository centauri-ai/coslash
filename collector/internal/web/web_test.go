package web

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestHandlerRejectsTraversalAndMissingAssets(t *testing.T) {
	assets := fstest.MapFS{
		"index.html":    &fstest.MapFile{Data: []byte("document")},
		"assets/app.js": &fstest.MapFile{Data: []byte("script")},
	}
	handler, err := handler(fs.FS(assets))
	if err != nil {
		t.Fatal(err)
	}

	for _, requestPath := range []string{"/assets/../../etc/passwd", "/assets/missing.js"} {
		t.Run(requestPath, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "http://coslash.test/", nil)
			request.URL.Path = requestPath
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
			}
		})
	}
}
