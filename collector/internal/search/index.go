package search

import (
	"cmp"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/centauri-ai/coslash/collector/internal/session"
	_ "modernc.org/sqlite"
)

const schemaVersion = 1

type Identity struct {
	SourceID  string `json:"sourceId"`
	Agent     string `json:"agent"`
	SessionID string `json:"sessionId"`
}
type Status struct {
	State    string `json:"state"`
	Indexed  int    `json:"indexed"`
	Total    int    `json:"total"`
	Coverage string `json:"coverage,omitempty"`
}
type Index struct {
	db    *sql.DB
	mu    sync.RWMutex
	state string
	total int
}

func Open(path string) (*Index, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	// The private directory also covers the -wal and -shm files SQLite creates.
	if err := protectDirectory(dir); err != nil {
		return nil, err
	}
	for attempt := 0; ; attempt++ {
		db, err := sql.Open("sqlite", path)
		if err != nil {
			return nil, err
		}
		db.SetMaxOpenConns(1)
		idx := &Index{db: db, state: "building"}
		if err = idx.initialize(); err == nil {
			return idx, nil
		}
		db.Close()
		// Only malformed databases or our own version mismatch are rebuildable.
		if attempt != 0 || (!errors.Is(err, errSchema) && !isCorrupt(err)) {
			return nil, err
		}
		// A write-ahead log left beside a new database would be replayed into it.
		for _, file := range []string{path, path + "-wal", path + "-shm"} {
			if err = os.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
		}
	}
}

var errSchema = errors.New("search schema mismatch")

func isCorrupt(err error) bool {
	var e interface{ Code() int }
	return errors.As(err, &e) && (e.Code()&255 == 11 || e.Code()&255 == 26)
}
func (i *Index) initialize() error {
	var version int
	if err := i.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version != 0 && version != schemaVersion {
		return errSchema
	}
	_, err := i.db.Exec(`PRAGMA foreign_keys=ON;
 PRAGMA busy_timeout=5000;
 PRAGMA journal_mode=WAL;
 PRAGMA synchronous=NORMAL;
 CREATE TABLE IF NOT EXISTS sessions(source_id TEXT,agent TEXT,session_id TEXT,detail_revision TEXT,coverage TEXT,overlay_key TEXT,mtime INTEGER,name TEXT,repo TEXT,branch TEXT,PRIMARY KEY(source_id,agent,session_id));
 CREATE TABLE IF NOT EXISTS units(id INTEGER PRIMARY KEY,source_id TEXT,agent TEXT,session_id TEXT,overlay INTEGER,kind TEXT,turn INTEGER,entry_id TEXT,time_ms INTEGER,text TEXT,record_json TEXT,FOREIGN KEY(source_id,agent,session_id) REFERENCES sessions ON DELETE CASCADE);
 CREATE INDEX IF NOT EXISTS units_session ON units(source_id,agent,session_id);
 CREATE VIRTUAL TABLE IF NOT EXISTS units_fts USING fts5(text,content='units',content_rowid='id',tokenize='porter unicode61 remove_diacritics 2');
 CREATE TRIGGER IF NOT EXISTS units_ai AFTER INSERT ON units BEGIN INSERT INTO units_fts(rowid,text) VALUES(new.id,new.text); END;
 CREATE TRIGGER IF NOT EXISTS units_ad AFTER DELETE ON units BEGIN INSERT INTO units_fts(units_fts,rowid,text) VALUES('delete',old.id,old.text); END;
 PRAGMA user_version=1;`)
	return err
}
func (i *Index) Close() error { return i.db.Close() }
func (i *Index) SetState(state string, total int) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.state = state
	i.total = total
}
func (i *Index) Status(ctx context.Context, id *Identity) (Status, error) {
	i.mu.RLock()
	s := Status{State: i.state, Total: i.total}
	i.mu.RUnlock()
	if err := i.db.QueryRowContext(ctx, "SELECT count(*) FROM sessions").Scan(&s.Indexed); err != nil {
		return s, err
	}
	s.Total = max(s.Total, s.Indexed)
	if id != nil {
		s.Coverage = "absent"
		err := i.db.QueryRowContext(ctx, "SELECT coverage FROM sessions WHERE source_id=? AND agent=? AND session_id=?", id.SourceID, id.Agent, id.SessionID).Scan(&s.Coverage)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return s, err
		}
	}
	return s, nil
}

type unit struct {
	Kind    string
	Turn    int
	EntryID string
	Time    int64
	Text    string
	Record  string
}

func value(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
func coverageLevel(c string) int {
	switch c {
	case "full":
		return 3
	case "partial":
		return 2
	case "record":
		return 1
	}
	return 0
}
func (i *Index) Upsert(ctx context.Context, sourceID string, s session.Session, coverage string) error {
	if sourceID == "" || s.Agent == "" || s.ID == "" || coverageLevel(coverage) == 0 {
		return errors.New("invalid search identity or coverage")
	}
	content, overlay := unitsFor(s)
	encoded, _ := json.Marshal(overlay)
	sum := sha256.Sum256(encoded)
	key := hex.EncodeToString(sum[:])
	tx, err := i.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var oldRevision, oldCoverage, oldKey string
	err = tx.QueryRowContext(ctx, "SELECT detail_revision,coverage,overlay_key FROM sessions WHERE source_id=? AND agent=? AND session_id=?", sourceID, s.Agent, s.ID).Scan(&oldRevision, &oldCoverage, &oldKey)
	fresh := errors.Is(err, sql.ErrNoRows)
	if err != nil && !fresh {
		return err
	}
	rewriteContent := fresh || oldRevision != s.DetailRevision || coverageLevel(coverage) > coverageLevel(oldCoverage)
	if !rewriteContent {
		coverage = oldCoverage
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO sessions VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(source_id,agent,session_id) DO UPDATE SET detail_revision=excluded.detail_revision,coverage=excluded.coverage,overlay_key=excluded.overlay_key,mtime=excluded.mtime,name=excluded.name,repo=excluded.repo,branch=excluded.branch`, sourceID, s.Agent, s.ID, s.DetailRevision, coverage, key, s.LastActivityTime, value(s.Name), value(s.Repository), value(s.Branch))
	if err != nil {
		return err
	}
	for n, list := range [][]unit{content, overlay} {
		if n == 0 && !rewriteContent || n == 1 && !fresh && key == oldKey {
			continue
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM units WHERE source_id=? AND agent=? AND session_id=? AND overlay=?", sourceID, s.Agent, s.ID, n); err != nil {
			return err
		}
		for _, u := range list {
			if _, err = tx.ExecContext(ctx, "INSERT INTO units(source_id,agent,session_id,overlay,kind,turn,entry_id,time_ms,text,record_json) VALUES(?,?,?,?,?,?,?,?,?,?)", sourceID, s.Agent, s.ID, n, u.Kind, u.Turn, u.EntryID, u.Time, u.Text, u.Record); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// Reconcile must only be called after an authoritative, complete pass over the
// source's sessions active at or after sinceMs; older rows are kept.
func (i *Index) Reconcile(ctx context.Context, sourceID string, keep []Identity, sinceMs int64) error {
	encoded, _ := json.Marshal(append([]Identity{}, keep...))
	_, err := i.db.ExecContext(ctx, "DELETE FROM sessions WHERE source_id=? AND mtime>=? AND (source_id,agent,session_id) NOT IN (SELECT value->>'sourceId',value->>'agent',value->>'sessionId' FROM json_each(?))", sourceID, sinceMs, string(encoded))
	return err
}

// ReconcileSources removes rows belonging to sources no longer configured.
func (i *Index) ReconcileSources(ctx context.Context, sources []string) error {
	encoded, _ := json.Marshal(append([]string{}, sources...))
	_, err := i.db.ExecContext(ctx, "DELETE FROM sessions WHERE source_id NOT IN (SELECT value FROM json_each(?))", string(encoded))
	return err
}
func unitsFor(s session.Session) (content, overlay []unit) {
	add := func(dst *[]unit, kind, text string) {
		if text != "" {
			*dst = append(*dst, unit{Kind: kind, Text: text})
		}
	}
	add(&content, "goal", value(s.DeclaredGoal))
	add(&content, "recap", value(s.Summary))
	add(&content, "prompt", value(s.FirstPrompt))
	for _, d := range s.Digest {
		kind := ""
		switch d.Category {
		case session.DigestFirstPrompt, session.DigestUser, session.DigestQuestion:
			kind = "prompt"
		case session.DigestPlan:
			kind = "plan"
		case session.DigestRecap:
			kind = "recap"
		}
		if kind == "" {
			continue
		}
		text := cmp.Or(d.SearchText, d.Description)
		if text != "" {
			content = append(content, unit{Kind: kind, Text: text, Turn: d.Turn, EntryID: d.SourceEntryID, Time: d.Time})
		}
		if d.Answer != "" {
			content = append(content, unit{Kind: "answer", Text: d.Answer, Turn: d.Turn, EntryID: d.SourceEntryID, Time: d.Time})
		}
	}
	for _, v := range s.Commands {
		add(&content, "command", v)
	}
	for _, v := range s.FileEdits {
		add(&content, "file", v.Path)
	}
	for _, v := range s.PullRequestLinks {
		add(&content, "pr", fmt.Sprintf("%s#%d %s", v.Repository, v.Number, v.URL))
	}
	add(&overlay, "title", value(s.Name))
	add(&overlay, "place", value(s.Repository)+" "+value(s.Branch))
	for _, v := range s.Commits {
		add(&overlay, "commit", v)
	}
	if s.Synthesis != nil {
		for _, v := range s.Synthesis.Goals {
			add(&overlay, "goal", v)
		}
		add(&overlay, "goal", s.Synthesis.Outcome)
		add(&overlay, "recap", s.Synthesis.NextStep)
		for _, decision := range decisionsForSearch(s.Synthesis) {
			raw, _ := json.Marshal(decision)
			overlay = append(overlay, unit{Kind: "decision", Text: decision.Text + " " + decision.Why + " " + decision.Alternative, Record: string(raw)})
		}
	}
	return
}
