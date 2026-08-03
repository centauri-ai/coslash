package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

const document = "<!doctype html><div id=\"root\"></div>"

func testAssets() fstest.MapFS {
	return fstest.MapFS{
		"index.html":                {Data: []byte(document)},
		"favicon.svg":               {Data: []byte("<svg/>")},
		"assets/index-Bzjl3Rnc.css": {Data: []byte(".root{}")},
		"assets/index-DDHjEZlF.js":  {Data: []byte("export {}")},
		"brand/geist-latin.woff2":   {Data: []byte("font")},
	}
}

func testHandler(t *testing.T, assets fstest.MapFS) http.Handler {
	t.Helper()
	served, err := handler(assets)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	return served
}

func get(t *testing.T, served http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	served.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
	return recorder
}

func TestServesEmbeddedAssets(t *testing.T) {
	served := testHandler(t, testAssets())
	for _, test := range []struct {
		target      string
		body        string
		contentType string
	}{
		{"/assets/index-DDHjEZlF.js", "export {}", "javascript"},
		{"/assets/index-Bzjl3Rnc.css", ".root{}", "css"},
		{"/favicon.svg", "<svg/>", "svg"},
		{"/brand/geist-latin.woff2", "font", "font"},
	} {
		recorder := get(t, served, test.target)
		if recorder.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", test.target, recorder.Code)
		}
		if recorder.Body.String() != test.body {
			t.Errorf("%s: body = %q, want %q", test.target, recorder.Body, test.body)
		}
		if contentType := recorder.Header().Get("Content-Type"); !strings.Contains(contentType, test.contentType) {
			t.Errorf("%s: Content-Type = %q, want it to contain %q", test.target, contentType, test.contentType)
		}
	}
}

func TestServesIndexForClientRoutes(t *testing.T) {
	served := testHandler(t, testAssets())
	// "/brand" is a real directory: it must render the app rather than a listing.
	for _, target := range []string{"/", "/coslash", "/coslash/deep/link", "/brand"} {
		recorder := get(t, served, target)
		if recorder.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", target, recorder.Code)
		}
		if recorder.Body.String() != document {
			t.Errorf("%s: body = %q, want the index document", target, recorder.Body)
		}
		if contentType := recorder.Header().Get("Content-Type"); !strings.Contains(contentType, "html") {
			t.Errorf("%s: Content-Type = %q, want it to contain %q", target, contentType, "html")
		}
	}
}

func TestMissingAssetsAreNotFound(t *testing.T) {
	served := testHandler(t, testAssets())
	for _, target := range []string{"/assets/index-Stale123.js", "/assets/gone.css", "/missing.svg"} {
		if recorder := get(t, served, target); recorder.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", target, recorder.Code)
		}
	}
}

func TestUnstagedAssetsReportAnActionableError(t *testing.T) {
	_, err := handler(fstest.MapFS{".gitkeep": {}})
	if err == nil {
		t.Fatal("handler succeeded without an index.html, want an error")
	}
	if !strings.Contains(err.Error(), "make release") {
		t.Errorf("error = %q, want it to name the build command", err)
	}
}
