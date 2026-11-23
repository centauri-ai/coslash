package search

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"
)

var kindWeights = map[string]float64{"decision": 3, "title": 2.5, "goal": 2, "prompt": 1.5, "answer": 1.5, "plan": 1.5, "recap": 1, "commit": 1, "pr": 1, "command": .7, "file": .7, "place": .5}

var (
	ErrEmptyQuery  = errors.New("search requires content terms")
	ErrBuilding    = errors.New("search index building")
	ErrUnavailable = errors.New("search index unavailable")
)

// matchFrom selects FTS matches with their unit and session; callers append filters.
const matchFrom = ` FROM units_fts JOIN units u ON u.id=units_fts.rowid JOIN sessions s USING(source_id,agent,session_id) WHERE units_fts MATCH ?`

type Options struct {
	Repo, Agent string
	Since       int64
	Limit       int
}
type Match struct {
	Kind    string   `json:"kind"`
	Turn    int      `json:"turn,omitempty"`
	Snippet string   `json:"snippet"`
	Ranges  [][2]int `json:"ranges"`
}

// sessionRef is the session context shared by session and decision hits.
type sessionRef struct {
	Identity
	Name        string `json:"name"`
	Repo        string `json:"repo"`
	Mtime       int64  `json:"mtime"`
	SourceLabel string `json:"sourceLabel"`
	Selector    string `json:"selector,omitempty"`
}
type SessionHit struct {
	sessionRef
	Score    float64 `json:"score"`
	Coverage string  `json:"coverage"`
	Turns    []int   `json:"turns"`
	Match    Match   `json:"match"`
	Branch   string  `json:"branch"`
}
type decisionRecord struct {
	DecisionID  string `json:"decisionId,omitempty"`
	Why         string `json:"why,omitempty"`
	Alternative string `json:"alternative,omitempty"`
	Text        string `json:"text"`
	DecidedBy   string `json:"decidedBy"`
	Turns       []int  `json:"turns"`
	Cited       bool   `json:"cited"`
}
type DecisionHit struct {
	sessionRef
	decisionRecord
	Score float64 `json:"score"`
}
type Response struct {
	Indexed   []Identity    `json:"indexed"`
	Truncated bool          `json:"truncated"`
	Decisions []DecisionHit `json:"decisions"`
	Sessions  []SessionHit  `json:"sessions"`
}
type candidate struct {
	hit   SessionHit
	kinds map[string]float64
	terms int
}

func (i *Index) Search(ctx context.Context, raw string, options Options) (Response, error) {
	result := Response{Indexed: []Identity{}, Sessions: []SessionHit{}, Decisions: []DecisionHit{}}
	q := ParseQuery(raw)
	if len(q.Terms) == 0 {
		return result, ErrEmptyQuery
	}
	i.mu.RLock()
	state := i.state
	i.mu.RUnlock()
	if state == "building" {
		return result, ErrBuilding
	}
	if state != "ready" {
		return result, ErrUnavailable
	}
	// Overlay rewrites may reuse row IDs; eligibility and scoring need one snapshot.
	tx, err := i.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if result.Indexed, err = identities(ctx, tx, "SELECT source_id,agent,session_id FROM sessions WHERE source_id!='local' ORDER BY source_id,agent,session_id"); err != nil {
		return result, err
	}
	where, filters := "", []any{}
	if options.Repo != "" {
		where += " AND s.repo=?"
		filters = append(filters, options.Repo)
	}
	if options.Agent != "" {
		where += " AND s.agent=?"
		filters = append(filters, options.Agent)
	}
	if options.Since != 0 {
		where += " AND s.mtime>=?"
		filters = append(filters, options.Since)
	}
	decisionUnits, err := matchingUnits(ctx, tx, q.match(" AND "), "u.kind='decision'"+where, filters)
	if err != nil {
		return result, err
	}
	var titlePhraseUnits map[int64]bool
	if phrase := strings.Join(q.Terms, " "); strings.Contains(phrase, " ") {
		if titlePhraseUnits, err = matchingUnits(ctx, tx, literal(phrase), "u.kind='title'"+where, filters); err != nil {
			return result, err
		}
	}
	rows, err := tx.QueryContext(ctx, "SELECT u.id,s.source_id,s.agent,s.session_id,s.coverage,s.mtime,s.name,s.repo,s.branch,u.kind,u.turn,u.record_json,bm25(units_fts),highlight(units_fts,0,char(1),char(2))"+matchFrom+where, append([]any{q.match(" OR ")}, filters...)...)
	if err != nil {
		return result, err
	}
	candidates := map[Identity]*candidate{}
	var decisions []DecisionHit
	for rows.Next() {
		var unitID int64
		var hit SessionHit
		var kind, record, highlight string
		var turn int
		var bm25 float64
		if err = rows.Scan(&unitID, &hit.SourceID, &hit.Agent, &hit.SessionID, &hit.Coverage, &hit.Mtime, &hit.Name, &hit.Repo, &hit.Branch, &kind, &turn, &record, &bm25, &highlight); err != nil {
			rows.Close()
			return result, err
		}
		hit.SourceLabel = "SSH"
		if hit.SourceID == "local" {
			hit.SourceLabel, hit.Selector = "Local", hit.Agent+":"+hit.SessionID
		}
		unitTurns := []int{}
		if turn > 0 {
			unitTurns = []int{turn}
		}
		score := -bm25 * kindWeights[kind]
		if titlePhraseUnits[unitID] {
			score *= 1.5
		}
		if kind == "decision" {
			var r decisionRecord
			if err = json.Unmarshal([]byte(record), &r); err != nil {
				rows.Close()
				return result, err
			}
			if q.DecisionIntent {
				score *= 1.5
			}
			if !r.Cited {
				score *= .6
			}
			unitTurns = r.Turns
			if decisionUnits[unitID] {
				decisions = append(decisions, DecisionHit{hit.sessionRef, r, score})
			}
		}
		c := candidates[hit.Identity]
		if c == nil {
			c = &candidate{hit: hit, kinds: map[string]float64{}}
			candidates[hit.Identity] = c
		}
		if score > c.hit.Score {
			c.hit.Score = score
			c.hit.Match = makeMatch(kind, turn, highlight)
			c.hit.Turns = append([]int{}, unitTurns...)
		}
		c.kinds[kind] = max(c.kinds[kind], score)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	// Use the same tokenizer and stemmer for coverage as for candidate retrieval.
	for _, term := range q.Terms {
		ids, err := identities(ctx, tx, "SELECT DISTINCT s.source_id,s.agent,s.session_id"+matchFrom+where, append([]any{literal(term)}, filters...)...)
		if err != nil {
			return result, err
		}
		for _, id := range ids {
			if c := candidates[id]; c != nil {
				c.terms++
			}
		}
	}
	for _, c := range candidates {
		scores := slices.SortedFunc(maps.Values(c.kinds), func(a, b float64) int { return cmp.Compare(b, a) })
		total := scores[0]
		for _, s := range scores[1:min(len(scores), 3)] {
			total += .25 * s
		}
		c.hit.Score = total * (.5 + .5*float64(c.terms)/float64(len(q.Terms)))
		result.Sessions = append(result.Sessions, c.hit)
	}
	slices.SortFunc(result.Sessions, func(l, r SessionHit) int {
		return cmp.Or(cmp.Compare(r.Score, l.Score), cmp.Compare(r.Mtime, l.Mtime), compareIdentity(l.Identity, r.Identity))
	})
	slices.SortStableFunc(decisions, func(l, r DecisionHit) int {
		return cmp.Or(cmp.Compare(r.Score, l.Score), compareIdentity(l.Identity, r.Identity), cmp.Compare(l.Text, r.Text))
	})
	limit := 2000
	if options.Limit > 0 && options.Limit < limit {
		limit = options.Limit
	}
	if len(result.Sessions) > limit {
		result.Truncated = true
		result.Sessions = result.Sessions[:limit]
	}
	kept := map[Identity]bool{}
	for _, s := range result.Sessions {
		kept[s.Identity] = true
	}
	counts := map[Identity]int{}
	for _, d := range decisions {
		if !kept[d.Identity] || counts[d.Identity] >= 2 {
			continue
		}
		counts[d.Identity]++
		if len(result.Decisions) >= 500 {
			result.Truncated = true
			continue
		}
		result.Decisions = append(result.Decisions, d)
	}
	return result, nil
}

func identities(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]Identity, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []Identity{}
	for rows.Next() {
		var id Identity
		if err := rows.Scan(&id.SourceID, &id.Agent, &id.SessionID); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// matchingUnits keeps phrase and all-term checks on the same FTS tokenizer as retrieval.
func matchingUnits(ctx context.Context, tx *sql.Tx, match, where string, filters []any) (map[int64]bool, error) {
	rows, err := tx.QueryContext(ctx, "SELECT u.id"+matchFrom+" AND "+where, append([]any{match}, filters...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids[id] = true
	}
	return ids, rows.Err()
}

func compareIdentity(a, b Identity) int {
	return cmp.Or(cmp.Compare(a.SourceID, b.SourceID), cmp.Compare(a.Agent, b.Agent), cmp.Compare(a.SessionID, b.SessionID))
}
func makeMatch(kind string, turn int, highlight string) Match {
	// Ranges count Unicode code points, matching Array.from in the browser.
	runes := []rune(highlight)
	first := 0
	for n, r := range runes {
		if r == 1 {
			first = n
			break
		}
	}
	start := max(0, first-70)
	end := min(len(runes), start+280)
	m := Match{Kind: kind, Turn: turn, Ranges: [][2]int{}}
	var b strings.Builder
	offset := 0
	opened := -1
	if start > 0 {
		b.WriteRune('…')
		offset++
	}
	for _, r := range runes[start:end] {
		if r == 1 {
			opened = offset
			continue
		}
		if r == 2 {
			if opened >= 0 {
				m.Ranges = append(m.Ranges, [2]int{opened, offset})
				opened = -1
			}
			continue
		}
		b.WriteRune(r)
		offset++
	}
	if opened >= 0 {
		m.Ranges = append(m.Ranges, [2]int{opened, offset})
	}
	if end < len(runes) {
		b.WriteRune('…')
	}
	m.Snippet = b.String()
	return m
}
