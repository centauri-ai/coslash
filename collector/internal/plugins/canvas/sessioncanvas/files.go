package sessioncanvas

import (
	"errors"
	"html/template"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const renderedFileCSP = "default-src 'none'; style-src 'unsafe-inline'; img-src data:; base-uri 'none'; form-action 'none'; sandbox"

var supportedFileExtensions = map[string]struct{}{
	".c": {}, ".cc": {}, ".cpp": {}, ".css": {}, ".diff": {}, ".go": {}, ".h": {},
	".hpp": {}, ".htm": {}, ".html": {}, ".java": {}, ".js": {}, ".json": {},
	".jsonl": {}, ".jsx": {}, ".md": {}, ".markdown": {}, ".patch": {}, ".py": {},
	".rs": {}, ".sh": {}, ".sql": {}, ".svg": {}, ".toml": {}, ".ts": {}, ".tsx": {},
	".txt": {}, ".xml": {}, ".yaml": {}, ".yml": {},
}

func (h *Handler) handleFile(w http.ResponseWriter, r *http.Request) {
	resolved, _, ok := h.resolve(w, r)
	if !ok {
		return
	}
	name := r.URL.Query().Get("path")
	file, path, err := openScopedRegularFile(resolved.Session.WorkingDirectory, name)
	if err != nil {
		switch {
		case errors.Is(err, fs.ErrNotExist):
			writeError(w, http.StatusNotFound, "FILE_NOT_FOUND", "file not found", "")
		case errors.Is(err, errUnsafeFile):
			writeError(w, http.StatusBadRequest, "INVALID_FILE_PATH", "file path is outside the session workspace", "path")
		default:
			writeError(w, http.StatusUnprocessableEntity, "FILE_UNAVAILABLE", "file is unavailable", "")
		}
		return
	}
	defer file.Close()
	extension := strings.ToLower(filepath.Ext(path))
	if _, supported := supportedFileExtensions[extension]; !supported {
		writeError(w, http.StatusUnsupportedMediaType, "FILE_TYPE_UNSUPPORTED", "file type is not supported", "path")
		return
	}
	data, err := io.ReadAll(io.LimitReader(file, h.maxFileBytes+1))
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "FILE_UNAVAILABLE", "file is unavailable", "")
		return
	}
	if int64(len(data)) > h.maxFileBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", "file exceeds the configured limit", "path")
		return
	}
	if !utf8.Valid(data) {
		writeError(w, http.StatusUnsupportedMediaType, "FILE_BINARY", "binary files are not supported", "path")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	switch extension {
	case ".md", ".markdown":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", renderedFileCSP)
		_, _ = io.WriteString(w, markdownPreview(filepath.Base(path), string(data)))
	case ".html", ".htm":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", renderedFileCSP)
		_, _ = w.Write(data)
	default:
		contentType := mime.TypeByExtension(extension)
		if contentType == "" || strings.HasPrefix(contentType, "image/") {
			contentType = "text/plain; charset=utf-8"
		}
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write(data)
	}
}

var errUnsafeFile = errors.New("session canvas: unsafe file path")

func openScopedRegularFile(root, name string) (*os.File, string, error) {
	if root == "" || name == "" || strings.ContainsRune(name, 0) || filepath.IsAbs(name) {
		return nil, "", errUnsafeFile
	}
	clean := filepath.Clean(name)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return nil, "", errUnsafeFile
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, "", err
	}
	rootInfo, err := os.Stat(resolvedRoot)
	if err != nil || !rootInfo.IsDir() {
		return nil, "", fs.ErrNotExist
	}
	current := resolvedRoot
	for _, component := range strings.Split(clean, string(filepath.Separator)) {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return nil, "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, "", errUnsafeFile
		}
	}
	rootHandle, err := os.OpenRoot(resolvedRoot)
	if err != nil {
		return nil, "", err
	}
	defer rootHandle.Close()
	file, err := rootHandle.Open(clean)
	if err != nil {
		return nil, "", err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, "", err
	}
	if !info.Mode().IsRegular() {
		file.Close()
		return nil, "", fs.ErrNotExist
	}
	return file, current, nil
}

func markdownPreview(title, source string) string {
	return "<!doctype html><html><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width\"><title>" +
		template.HTMLEscapeString(title) + "</title><style>:root{color-scheme:light dark}body{max-width:52rem;margin:2rem auto;padding:0 1rem;font:15px/1.55 ui-monospace,monospace}pre{white-space:pre-wrap;overflow-wrap:anywhere}</style></head><body><pre>" +
		template.HTMLEscapeString(source) + "</pre></body></html>"
}
