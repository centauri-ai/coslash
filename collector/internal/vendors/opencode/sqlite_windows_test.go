//go:build windows

package opencode

import (
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadOnlyDatabaseDSNAcceptsWindowsDrivePath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "OpenCode 卡尔文")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "opencode.db")
	writable, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writable.Exec("CREATE TABLE session (id TEXT)"); err != nil {
		writable.Close()
		t.Fatal(err)
	}
	if err := writable.Close(); err != nil {
		t.Fatal(err)
	}

	dsn := readOnlyDatabaseDSN(path)
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Scheme != "file" || parsed.Host != "" || !strings.HasPrefix(parsed.Path, "/") {
		t.Fatalf("Windows database DSN = %q; parsed as scheme=%q host=%q path=%q", dsn, parsed.Scheme, parsed.Host, parsed.Path)
	}
	if parsed.Query().Get("mode") != "ro" || parsed.Query().Get("_query_only") != "1" ||
		parsed.Query().Get("_busy_timeout") != "1000" {
		t.Fatalf("Windows database DSN is not read-only: %q", dsn)
	}

	readOnly, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer readOnly.Close()
	if err := readOnly.Ping(); err != nil {
		t.Fatalf("open Windows database DSN %q: %v", dsn, err)
	}
	if _, err := readOnly.Exec("CREATE TABLE forbidden (id TEXT)"); err == nil {
		t.Fatal("read-only database accepted a write")
	}
}

func TestReadOnlyDatabaseDSNWindowsURIShapes(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		wantPath string
	}{
		{
			name:     "drive path with reserved characters",
			path:     `C:\OpenCode #100%\data&more.db`,
			wantPath: `/C:/OpenCode #100%/data&more.db`,
		},
		{
			name:     "UNC path",
			path:     `\\server\share\OpenCode #100%\data&more.db`,
			wantPath: `//server/share/OpenCode #100%/data&more.db`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dsn := readOnlyDatabaseDSN(test.path)
			parsed, err := url.Parse(dsn)
			if err != nil {
				t.Fatal(err)
			}
			if parsed.Scheme != "file" || parsed.Host != "" || parsed.Path != test.wantPath {
				t.Fatalf("Windows database DSN = %q; parsed as scheme=%q host=%q path=%q, want path %q", dsn, parsed.Scheme, parsed.Host, parsed.Path, test.wantPath)
			}
			if strings.Contains(dsn, " ") || strings.Contains(dsn, "#") ||
				!strings.Contains(dsn, "%23") || !strings.Contains(dsn, "%25") {
				t.Fatalf("Windows database DSN contains an unescaped reserved character: %q", dsn)
			}
		})
	}
}
