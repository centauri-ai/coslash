package opencode

import (
	"database/sql"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

func Root() (string, error) {
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dataHome = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dataHome, "opencode", "opencode.db"), nil
}

func open() (*sql.DB, error) {
	path, err := Root()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	dsn := (&url.URL{
		Scheme: "file",
		Path:   path,
		// does not use immutable=1, to ignore WAL files
		RawQuery: url.Values{
			"mode":          {"ro"},   // read-only
			"_query_only":   {"1"},    // prevent accidental writes
			"_busy_timeout": {"1000"}, // waits briefly if another process holds a lock
		}.Encode(),
	}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
