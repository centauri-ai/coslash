// Package web serves the compiled frontend from assets embedded in the binary.
// `make release` stages frontend/dist into the dist directory here before
// compiling, since Go cannot embed a path outside its own module.
package web

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

//go:embed all:dist
var staged embed.FS

// Handler serves the embedded frontend.
func Handler() (http.Handler, error) {
	assets, err := fs.Sub(staged, "dist")
	if err != nil {
		return nil, fmt.Errorf("read embedded frontend: %w", err)
	}
	return handler(assets)
}

// handler serves real files as themselves, client-side routes as index.html so
// they survive a refresh, and missing static assets as a 404.
func handler(assets fs.FS) (http.Handler, error) {
	document, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		return nil, errors.New("no frontend assets are embedded in this binary; build it with `make release`")
	}
	files := http.FileServerFS(assets)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := path.Clean(r.URL.Path)
		switch {
		case isFile(assets, name):
			files.ServeHTTP(w, r)
		case strings.HasPrefix(name, "/assets/"):
			http.NotFound(w, r)
		case path.Ext(name) != "":
			http.NotFound(w, r)
		default:
			http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(document))
		}
	}), nil
}

// isFile reports whether name is a regular file in assets. A directory fails
// the check so it falls through to index.html instead of serving a listing.
func isFile(assets fs.FS, name string) bool {
	info, err := fs.Stat(assets, strings.TrimPrefix(name, "/"))
	return err == nil && info.Mode().IsRegular()
}
