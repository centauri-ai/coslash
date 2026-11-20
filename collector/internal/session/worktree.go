package session

import (
	"cmp"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/centauri-ai/coslash/collector/internal/backgroundexec"
	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

// Location joins a session's recorded directory and repository to a verified checkout.
type Location struct {
	ID           string `json:"id"`
	Label        string `json:"label"`
	Root         string `json:"root"`
	CWD          string `json:"cwd"`
	Repo         string `json:"repo,omitempty"`
	CloneID      string `json:"cloneId"`
	Primary      bool   `json:"primary"`
	Main         bool   `json:"main"`
	HeadKind     string `json:"headKind"`
	Head         string `json:"head,omitempty"`
	LastSeen     int64  `json:"lastSeen"`
	Availability string `json:"availability"`
}

type RecordedDirectory struct {
	CWD      string
	Repo     string
	Activity int64
}

// NoteDirectory keeps only the most recent observation of each recorded cwd and origin.
func NoteDirectory(entries map[string]RecordedDirectory, cwd, repo string, activity int64) {
	if !validLocationText(cwd, 4096) {
		return
	}
	key := cwd + "\x00" + repo
	if prior, ok := entries[key]; !ok || activity >= prior.Activity {
		entries[key] = RecordedDirectory{CWD: cwd, Repo: repo, Activity: activity}
	}
}

type CheckoutObservation struct {
	Location
	Admin, Common, CloneKey, CheckoutKey string
}

func validLocationText(s string, limit int) bool {
	return s != "" && len(s) <= limit && utf8.ValidString(s) && !strings.ContainsFunc(s, unicode.IsControl)
}

// ProbeCheckout verifies Git's own paths and the reciprocal linked-worktree registration.
func ProbeCheckout(ctx context.Context, root, repo string) *CheckoutObservation {
	if !filepath.IsAbs(root) || !backgroundFilesystemProbeAllowed(root) {
		return nil
	}
	out, err := backgroundexec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--path-format=absolute", "--show-toplevel", "--git-dir", "--git-common-dir").Output()
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSuffix(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n"), "\n")
	if len(lines) != 3 {
		return nil
	}
	for i, s := range lines {
		if !validLocationText(s, 4096) || !filepath.IsAbs(s) || !backgroundFilesystemProbeAllowed(s) {
			return nil
		}
		path, err := filepath.EvalSymlinks(s)
		if err != nil {
			return nil
		}
		lines[i] = path
	}
	root, admin, common := lines[0], lines[1], lines[2]
	main := admin == common
	if !main {
		if filepath.Dir(filepath.Dir(admin)) != common || filepath.Base(filepath.Dir(admin)) != "worktrees" {
			return nil
		}
		if registeredRoot(admin) != root {
			return nil
		}
	}
	cloneKey, err := fileIdentity(common)
	if err != nil {
		return nil
	}
	treeKey := cloneKey
	if !main {
		// Git keeps commondir unchanged on branch switches and worktree moves.
		marker := filepath.Join(admin, "commondir")
		treeKey, err = fileIdentity(marker)
		if err != nil {
			return nil
		}
		info, err := os.Stat(marker)
		if err != nil {
			return nil
		}
		treeKey += fmt.Sprintf(":%d", info.ModTime().UnixNano())
	}
	headKind, head := "unknown", ""
	branch := CurrentBranchContext(ctx, root)
	commit, err := backgroundexec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--verify", "HEAD").Output()
	if branch != nil {
		headKind, head = "branch", *branch
		if err != nil {
			headKind = "unborn"
		}
	} else if err == nil {
		headKind, head = "detached", strings.TrimSpace(string(commit))
	}
	label := filepath.Base(root)
	if !validLocationText(label, 255) {
		return nil
	}
	return &CheckoutObservation{Location: Location{Root: root, Label: label, Repo: repo, Main: main, HeadKind: headKind, Head: head, LastSeen: time.Now().UnixMilli(), Availability: "available"}, Admin: admin, Common: common, CloneKey: cloneKey, CheckoutKey: treeKey}
}

func registeredRoot(admin string) string {
	if !backgroundFilesystemProbeAllowed(admin) {
		return ""
	}
	f, err := os.Open(filepath.Join(admin, "gitdir"))
	if err != nil {
		return ""
	}
	raw, err := io.ReadAll(io.LimitReader(f, 4098))
	f.Close()
	if err != nil || len(raw) > 4097 {
		return ""
	}
	back := strings.TrimRight(string(raw), "\r\n")
	if !filepath.IsAbs(back) {
		back = filepath.Join(admin, back)
	}
	if !backgroundFilesystemProbeAllowed(back) {
		return ""
	}
	real, err := filepath.EvalSymlinks(back)
	if err != nil || filepath.Base(real) != ".git" {
		return ""
	}
	return filepath.Dir(real)
}

// RefreshLocalLocations probes each referenced root once per scan, independently of transcript revisions.
func RefreshLocalLocations(ctx context.Context, cards []*Session, origins map[string]string) error {
	probes := map[string]*CheckoutObservation{}
	roots := map[string]*CheckoutObservation{}
	for _, s := range cards {
		cwds := []string{s.WorkingDirectory}
		for _, dir := range s.RecordedDirectories {
			cwds = append(cwds, dir.CWD)
		}
		for _, cwd := range cwds {
			if err := ctx.Err(); err != nil {
				return err
			}
			if _, ok := probes[cwd]; ok || !filepath.IsAbs(cwd) {
				continue
			}
			probes[cwd] = nil
			root := repositoryRootForName(ctx, cwd)
			if root == "" {
				if moved := probeMovedCheckout(ctx, cwd); moved != nil {
					probes["\x00"+cwd] = moved
				}
				continue
			}
			if _, ok := roots[root]; !ok {
				remote := origins[cwd]
				if origins == nil {
					if name, localOnly := repositoryName(ctx, cwd, root); !localOnly {
						remote = name
					}
				}
				roots[root] = ProbeCheckout(ctx, root, remote)
			}
			probes[cwd] = roots[root]
		}
	}
	return RecordLocations(ctx, "local", cards, probes)
}

// A moved linked worktree retains its administrative directory and updates its backlink.
func probeMovedCheckout(ctx context.Context, cwd string) *CheckoutObservation {
	locationMemory.Lock()
	if locationMemory.db == nil {
		locationMemory.Unlock()
		return nil
	}
	var admin string
	err := locationMemory.db.QueryRowContext(ctx, `SELECT admin FROM links l JOIN trees t ON tree=t.id
  WHERE source='local' AND cwd=? AND main=0 AND availability!='removed' ORDER BY observed DESC LIMIT 1`, cwd).Scan(&admin)
	locationMemory.Unlock()
	if err != nil {
		return nil
	}
	root := registeredRoot(admin)
	if root == "" {
		return nil
	}
	repo, localOnly := repositoryName(ctx, root, root)
	if localOnly {
		repo = ""
	}
	return ProbeCheckout(ctx, root, repo)
}

var locationMemory struct {
	sync.Mutex
	db   *sql.DB
	salt string
}

func UseLocationMemory(path string) error {
	locationMemory.Lock()
	defer locationMemory.Unlock()
	return openLocationMemory(path)
}

func openLocationMemory(path string) error {
	dsn := ":memory:"
	if path != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return err
		}
		if err = protectLocationFile(path, f); err != nil {
			f.Close()
			return err
		}
		if err = f.Close(); err != nil {
			return err
		}
		u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
		if !strings.HasPrefix(u.Path, "/") {
			u.Path = "/" + u.Path
		}
		dsn = u.String()
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS namespace (salt TEXT PRIMARY KEY);
 CREATE TABLE IF NOT EXISTS trees(id TEXT PRIMARY KEY, clone TEXT, main INTEGER, root TEXT, label TEXT, common TEXT, admin TEXT, head_kind TEXT, head TEXT, observed INTEGER, availability TEXT);
 CREATE TABLE IF NOT EXISTS tree_origins(tree TEXT, repo TEXT, observed INTEGER, PRIMARY KEY(tree,repo));
 CREATE TABLE IF NOT EXISTS links(source TEXT, agent TEXT, session TEXT, tree TEXT, repo TEXT, cwd TEXT, activity INTEGER, PRIMARY KEY(source,agent,session,tree,repo,cwd));
 CREATE TABLE IF NOT EXISTS observed_activity(source TEXT,agent TEXT,session TEXT,activity INTEGER,PRIMARY KEY(source,agent,session));
 CREATE TABLE IF NOT EXISTS projections(source TEXT,agent TEXT,session TEXT,version INTEGER,signature TEXT,data BLOB,PRIMARY KEY(source,agent,session));
 CREATE INDEX IF NOT EXISTS cwd_links ON links(source,cwd);
 CREATE INDEX IF NOT EXISTS latest_links ON links(source,agent,session,activity DESC);
 INSERT INTO tree_origins SELECT DISTINCT l.tree,l.repo,t.observed FROM links l JOIN trees t ON l.tree=t.id
 WHERE NOT EXISTS (SELECT 1 FROM tree_origins);`)
	if err == nil {
		_, err = db.Exec("INSERT INTO namespace SELECT ? WHERE NOT EXISTS (SELECT 1 FROM namespace)", uuid.NewString())
	}
	var salt string
	if err == nil {
		err = db.QueryRow("SELECT salt FROM namespace").Scan(&salt)
	}
	if err != nil {
		db.Close()
		return err
	}
	if locationMemory.db != nil {
		locationMemory.db.Close()
	}
	locationMemory.db, locationMemory.salt = db, salt
	return nil
}

func locationID(parts ...string) string {
	hash := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(hash[:16])
}

// RecordLocations atomically records the completed probes and attaches the retained associations.
func RecordLocations(ctx context.Context, source string, sessions []*Session, probes map[string]*CheckoutObservation) error {
	if len(sessions) == 0 {
		return nil
	}
	locationMemory.Lock()
	defer locationMemory.Unlock()
	if locationMemory.db == nil {
		if err := openLocationMemory(""); err != nil {
			return err
		}
	}
	tx, err := locationMemory.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	verifiedIDs := map[string]bool{}
	for _, probe := range probes {
		if probe == nil {
			continue
		}
		probe.CloneID = locationID(locationMemory.salt, source, probe.CloneKey)
		probe.ID = locationID(probe.CloneID, probe.CheckoutKey)
		if verifiedIDs[probe.ID] {
			continue
		}
		verifiedIDs[probe.ID] = true
		_, err = tx.ExecContext(ctx, `INSERT INTO trees VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET root=excluded.root,label=excluded.label,common=excluded.common,admin=excluded.admin,head_kind=excluded.head_kind,head=excluded.head,observed=excluded.observed,availability=excluded.availability WHERE excluded.observed>=trees.observed`, probe.ID, probe.CloneID, probe.Main, probe.Root, probe.Label, probe.Common, probe.Admin, probe.HeadKind, probe.Head, probe.LastSeen, probe.Availability)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO tree_origins VALUES(?,?,?) ON CONFLICT(tree,repo) DO UPDATE SET observed=max(tree_origins.observed,excluded.observed)`, probe.ID, probe.Repo, probe.LastSeen)
		if err != nil {
			return err
		}
	}
	for _, s := range sessions {
		if s.ID == "" {
			continue
		}
		dirs := make([]RecordedDirectory, 0, len(s.RecordedDirectories)+1)
		for _, dir := range s.RecordedDirectories {
			dirs = append(dirs, dir)
		}
		slices.SortFunc(dirs, func(a, b RecordedDirectory) int {
			return cmp.Or(cmp.Compare(a.Activity, b.Activity), cmp.Compare(a.CWD, b.CWD), cmp.Compare(a.Repo, b.Repo))
		})
		current := RecordedDirectory{CWD: s.WorkingDirectory, Activity: s.LastActivityTime}
		dirs = append(dirs, current)
		for _, dir := range dirs {
			probe := probes[dir.CWD]
			if probe == nil {
				continue
			}
			// A transcript may independently name a different repository. Only Git identifies the checkout's remote.
			// An unchanged transcript at a reused path must keep its original checkout.
			_, err = tx.ExecContext(ctx, `INSERT INTO links SELECT ?,?,?,?,?,?,? WHERE NOT EXISTS
     (SELECT 1 FROM links WHERE source=? AND agent=? AND session=? AND activity>=? AND cwd=? AND tree!=?)
     ON CONFLICT(source,agent,session,tree,repo,cwd) DO UPDATE SET activity=max(links.activity,excluded.activity)`,
				source, s.Agent, s.ID, probe.ID, probe.Repo, dir.CWD, dir.Activity, source, s.Agent, s.ID, dir.Activity, dir.CWD, probe.ID)
			if err != nil {
				return err
			}
		}
		rows, err := tx.QueryContext(ctx, `SELECT t.id,t.clone,t.main,t.root,t.label,t.head_kind,t.head,coalesce(o.observed,t.observed),t.availability,l.cwd,l.repo,t.common,t.admin,l.activity
    FROM links l JOIN trees t ON l.tree=t.id LEFT JOIN tree_origins o ON o.tree=l.tree AND o.repo=l.repo WHERE source=? AND agent=? AND session=? ORDER BY activity DESC,(l.cwd=?) DESC,o.observed DESC,l.rowid DESC`, source, s.Agent, s.ID, s.WorkingDirectory)
		if err != nil {
			return err
		}
		var locations []Location
		for rows.Next() {
			var l Location
			var common, admin string
			var activity int64
			if err = rows.Scan(&l.ID, &l.CloneID, &l.Main, &l.Root, &l.Label, &l.HeadKind, &l.Head, &l.LastSeen, &l.Availability, &l.CWD, &l.Repo, &common, &admin, &activity); err != nil {
				rows.Close()
				return err
			}
			probe := probes[l.CWD]
			if !verifiedIDs[l.ID] && l.Availability != "removed" {
				l.Availability = "stale"
				if source == "local" && !l.Main && backgroundFilesystemProbeAllowed(common) {
					// A missing registration in a reachable clone confirms removal. A missing mount does not.
					if _, err := os.Stat(admin); errors.Is(err, os.ErrNotExist) {
						if key, err := fileIdentity(common); err == nil && locationID(locationMemory.salt, source, key) == l.CloneID {
							l.Availability = "removed"
						}
					}
					if probe != nil && probe.CloneID == l.CloneID && probe.Admin == admin && probe.ID != l.ID {
						l.Availability = "removed"
					}
				}
			}
			l.Primary = len(locations) == 0 && l.CWD == s.WorkingDirectory && (l.Availability != "removed" || activity >= s.LastActivityTime)
			locations = append(locations, l)
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, l := range locations {
			if l.Availability == "removed" {
				if _, err = tx.ExecContext(ctx, "UPDATE trees SET availability='removed' WHERE id=?", l.ID); err != nil {
					return err
				}
			}
		}
		s.Locations = locations
		if len(locations) > 0 {
			result, err := tx.ExecContext(ctx, `INSERT INTO observed_activity VALUES(?,?,?,?) ON CONFLICT(source,agent,session) DO UPDATE SET activity=excluded.activity WHERE excluded.activity>=observed_activity.activity`, source, s.Agent, s.ID, s.LastActivityTime)
			if err != nil {
				return err
			}
			count, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if count == 0 {
				continue
			}
			// The last entry clears the primary checkout when a session resumes in an unverified folder.
			projected := append([]Location(nil), locations...)
			if !locations[0].Primary {
				repo := ""
				if !s.RepositoryLocalOnly && s.Repository != nil {
					repo = *s.Repository
				}
				projected = append([]Location{{CWD: s.WorkingDirectory, Repo: repo}}, projected...)
			}
			data, err := json.Marshal(projected)
			if err != nil {
				return err
			}
			for i := range projected {
				projected[i].LastSeen /= int64(time.Hour / time.Millisecond)
			}
			body, err := json.Marshal(projected)
			if err != nil {
				return err
			}
			digest := sha256.Sum256(body)
			_, err = tx.ExecContext(ctx, `INSERT INTO projections VALUES(?,?,?,?,?,?) ON CONFLICT(source,agent,session) DO UPDATE SET version=max(projections.version+?,excluded.version),signature=excluded.signature,data=excluded.data WHERE excluded.signature!=projections.signature`, source, s.Agent, s.ID, time.Now().UnixMicro(), hex.EncodeToString(digest[:]), data, len(projected))
			if err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

type LocationProjection struct {
	Version   int64
	Locations []Location
}

func StoredLocations(ctx context.Context, source, agent, id string, after int64) (LocationProjection, error) {
	locationMemory.Lock()
	defer locationMemory.Unlock()
	if locationMemory.db == nil {
		return LocationProjection{}, nil
	}
	var p LocationProjection
	var data []byte
	err := locationMemory.db.QueryRowContext(ctx, "SELECT version,data FROM projections WHERE source=? AND agent=? AND session=? AND version>?", source, agent, id, after).Scan(&p.Version, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return LocationProjection{}, nil
	}
	if err != nil {
		return LocationProjection{}, err
	}
	err = json.Unmarshal(data, &p.Locations)
	return p, err
}
