package search

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

var kindWeights = map[string]float64{"decision": 3, "title": 2.5, "goal": 2, "finding": 2, "prompt": 1.5, "answer": 1.5, "plan": 1.5, "recap": 1, "commit": 1, "pr": 1, "command": .7, "file": .7, "place": .5}
var ErrEmptyQuery = errors.New("search requires content terms")

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
type SessionHit struct {
	Identity
	Score       float64 `json:"score"`
	Coverage    string  `json:"coverage"`
	Turns       []int   `json:"turns"`
	Match       Match   `json:"match"`
	Name        string  `json:"name"`
	Repo        string  `json:"repo"`
	Branch      string  `json:"branch"`
	Mtime       int64   `json:"mtime"`
	SourceLabel string  `json:"sourceLabel"`
	Selector    string  `json:"selector,omitempty"`
}
type decisionRecord struct {
	DecisionID  string `json:"decisionId,omitempty"`
	Why         string `json:"why,omitempty"`
	Alternative string `json:"alternative,omitempty"`
	Kind        string `json:"kind"`
	Text        string `json:"text"`
	Status      string `json:"status"`
	DecidedBy   string `json:"decidedBy"`
	Turns       []int  `json:"turns"`
	Cited       bool   `json:"cited"`
}
type DecisionHit struct {
	DecisionID  string `json:"decisionId,omitempty"`
	Why         string `json:"why,omitempty"`
	Alternative string `json:"alternative,omitempty"`
	Name        string `json:"name"`
	Repo        string `json:"repo"`
	Mtime       int64  `json:"mtime"`
	SourceLabel string `json:"sourceLabel"`
	Selector    string `json:"selector,omitempty"`
	Identity
	Kind      string  `json:"kind"`
	Status    string  `json:"status"`
	DecidedBy string  `json:"decidedBy"`
	Text      string  `json:"text"`
	Turns     []int   `json:"turns"`
	Cited     bool    `json:"cited"`
	Score     float64 `json:"score"`
}
type Response struct {
	Indexed    []Identity    `json:"indexed"`
	IndexState string        `json:"indexState"`
	Truncated  bool          `json:"truncated"`
	Decisions  []DecisionHit `json:"decisions"`
	Sessions   []SessionHit  `json:"sessions"`
}
type candidate struct {
	hit   SessionHit
	kinds map[string]float64
	terms int
}

func (i *Index) Search(ctx context.Context, raw string, options Options) (Response, error) {
	i.mu.RLock()
	state := i.state
	i.mu.RUnlock()
	result := Response{IndexState: state, Sessions: []SessionHit{}, Decisions: []DecisionHit{}}
	result.Indexed = []Identity{}
	// Overlay rewrites may reuse row IDs; eligibility and scoring need one snapshot.
	tx, err := i.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	indexedRows, err := tx.QueryContext(ctx, "SELECT source_id, agent, session_id FROM sessions WHERE source_id != 'local' ORDER BY source_id,agent,session_id")
	if err != nil {
		return result, err
	}
	for indexedRows.Next() {
		var id Identity
		if err = indexedRows.Scan(&id.SourceID, &id.Agent, &id.SessionID); err != nil {
			indexedRows.Close()
			return result, err
		}
		result.Indexed = append(result.Indexed, id)
	}
	err = indexedRows.Err()
	indexedRows.Close()
	if err != nil {
		return result, err
	}
	q := ParseQuery(raw)
	if len(q.Terms) == 0 {
		return result, ErrEmptyQuery
	}
	where := ""
	args := []any{q.Match}
	if options.Repo != "" {
		where += " AND s.repo=?"
		args = append(args, options.Repo)
	}
	if options.Agent != "" {
		where += " AND s.agent=?"
		args = append(args, options.Agent)
	}
	if options.Since != 0 {
		where += " AND s.mtime>=?"
		args = append(args, options.Since)
	}
	terms := make([]string, len(q.Terms))
	for n, term := range q.Terms {
		terms[n] = literal(term)
	}
	decisionUnits, err := matchingUnits(ctx, tx, strings.Join(terms, " AND "), "u.kind IN ('decision','finding')"+where, args[1:])
	if err != nil {
		return result, err
	}
	var titlePhraseUnits map[int64]bool
	phrase := strings.Join(q.Terms, " ")
	if len(strings.Fields(phrase)) > 1 {
		titlePhraseUnits, err = matchingUnits(ctx, tx, literal(phrase), "u.kind='title'"+where, args[1:])
		if err != nil {
			return result, err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT u.id,s.source_id,s.agent,s.session_id,s.coverage,s.mtime,s.name,s.repo,s.branch,u.kind,u.turn,u.text,u.record_json,bm25(units_fts),highlight(units_fts,0,char(1),char(2)) FROM units_fts JOIN units u ON u.id=units_fts.rowid JOIN sessions s USING(source_id,agent,session_id) WHERE units_fts MATCH ?`+where, args...)
	if err != nil {
		return result, err
	}
	candidates := map[Identity]*candidate{}
	var decisions []DecisionHit
	for rows.Next() {
		var unitID int64
		var hit SessionHit
		var kind, text, record, highlight string
		var turn int
		var bm25 float64
		if err = rows.Scan(&unitID, &hit.SourceID, &hit.Agent, &hit.SessionID, &hit.Coverage, &hit.Mtime, &hit.Name, &hit.Repo, &hit.Branch, &kind, &turn, &text, &record, &bm25, &highlight); err != nil {
			rows.Close()
			return result, err
		}
		hit.SourceLabel = "Local"
		if hit.SourceID != "local" {
			hit.SourceLabel = "SSH"
		} else {
			hit.Selector = hit.Agent + ":" + hit.SessionID
		}
		unitTurns := []int{}
		if turn > 0 {
			unitTurns = []int{turn}
		}
		score := -bm25 * kindWeights[kind]
		if titlePhraseUnits[unitID] {
			score *= 1.5
		}
		if kind == "decision" || kind == "finding" {
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
			switch r.Status {
			case "proposed":
				score *= .5
			case "superseded":
				score *= .3
			}
			unitTurns = r.Turns
			if r.Turns == nil {
				r.Turns = []int{}
			}
			if decisionUnits[unitID] {
				decisions = append(decisions, DecisionHit{DecisionID: r.DecisionID, Why: r.Why, Alternative: r.Alternative, Name: hit.Name, Repo: hit.Repo, Mtime: hit.Mtime, SourceLabel: hit.SourceLabel, Selector: hit.Selector, Identity: hit.Identity, Kind: kind, Status: r.Status, DecidedBy: r.DecidedBy, Text: r.Text, Turns: r.Turns, Cited: r.Cited, Score: score})
			}
		}
		c := candidates[hit.Identity]
		if c == nil {
			hit.SourceLabel = "Local"
			if hit.SourceID != "local" {
				hit.SourceLabel = "SSH"
			}
			if hit.SourceID == "local" {
				hit.Selector = hit.Agent + ":" + hit.SessionID
			}
			c = &candidate{hit: hit, kinds: map[string]float64{}}
			candidates[hit.Identity] = c
		}
		if score > c.hit.Score {
			c.hit.Score = score
			c.hit.Match = makeMatch(kind, turn, highlight)
			c.hit.Turns = append([]int{}, unitTurns...)
		}
		if score > c.kinds[kind] {
			c.kinds[kind] = score
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	// Use the same tokenizer and stemmer for coverage as for candidate retrieval.
	for _, term := range q.Terms {
		termArgs := append([]any{literal(term)}, args[1:]...)
		rows, err = tx.QueryContext(ctx, `SELECT DISTINCT s.source_id,s.agent,s.session_id FROM units_fts JOIN units u ON u.id=units_fts.rowid JOIN sessions s USING(source_id,agent,session_id) WHERE units_fts MATCH ?`+where, termArgs...)
		if err != nil {
			return result, err
		}
		for rows.Next() {
			var id Identity
			if err = rows.Scan(&id.SourceID, &id.Agent, &id.SessionID); err != nil {
				rows.Close()
				return result, err
			}
			if c := candidates[id]; c != nil {
				c.terms++
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return result, err
		}
	}
	for _, c := range candidates {
		scores := make([]float64, 0, len(c.kinds))
		for _, s := range c.kinds {
			scores = append(scores, s)
		}
		sort.Sort(sort.Reverse(sort.Float64Slice(scores)))
		total := scores[0]
		for n := 1; n < len(scores) && n < 3; n++ {
			total += .25 * scores[n]
		}
		c.hit.Score = total * (.5 + .5*float64(c.terms)/float64(len(q.Terms)))
		result.Sessions = append(result.Sessions, c.hit)
	}
	sort.Slice(result.Sessions, func(a, b int) bool {
		l, r := result.Sessions[a], result.Sessions[b]
		if l.Score != r.Score {
			return l.Score > r.Score
		}
		if l.Mtime != r.Mtime {
			return l.Mtime > r.Mtime
		}
		return identityLess(l.Identity, r.Identity)
	})
	sort.SliceStable(decisions, func(a, b int) bool {
		l, r := decisions[a], decisions[b]
		if l.Score != r.Score {
			return l.Score > r.Score
		}
		if l.Identity != r.Identity {
			return identityLess(l.Identity, r.Identity)
		}
		return l.Text < r.Text
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

// matchingUnits keeps phrase and all-term checks on the same FTS tokenizer as retrieval.
func matchingUnits(ctx context.Context, tx *sql.Tx, match, where string, filters []any) (map[int64]bool, error) {
	args := append([]any{match}, filters...)
	rows, err := tx.QueryContext(ctx, `SELECT u.id FROM units_fts JOIN units u ON u.id=units_fts.rowid JOIN sessions s USING(source_id,agent,session_id) WHERE units_fts MATCH ? AND `+where, args...)
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

func identityLess(a, b Identity) bool {
	if a.SourceID != b.SourceID {
		return a.SourceID < b.SourceID
	}
	if a.Agent != b.Agent {
		return a.Agent < b.Agent
	}
	return a.SessionID < b.SessionID
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
