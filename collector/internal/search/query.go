package search

import (
	"strings"
	"unicode"
)

type Query struct {
	Terms          []string
	DecisionIntent bool
	Filters        map[string][]string
}

var prefixes = map[string]bool{"repo": true, "worktree": true, "agent": true, "machine": true, "group": true, "status": true, "since": true}
var framing = wordSet("a an the why what when where how did does do we i you they is are was were be been happens happen to about of for in on at and or not with our it this that decide decided decision chose choose agreed")
var intent = wordSet("why decide decided decision chose choose agreed")

func wordSet(s string) map[string]bool {
	m := map[string]bool{}
	for _, v := range strings.Fields(s) {
		m[v] = true
	}
	return m
}
func literal(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// ParseQuery treats all user syntax as literal text, never as FTS operators.
func ParseQuery(raw string) Query {
	q := Query{Filters: map[string][]string{}}
	var tokens []string
	var b strings.Builder
	quoted := false
	for _, r := range raw {
		if r == '"' {
			quoted = !quoted
			b.WriteRune(r)
			continue
		}
		if unicode.IsSpace(r) && !quoted {
			if b.Len() > 0 {
				tokens = append(tokens, b.String())
				b.Reset()
			}
		} else {
			b.WriteRune(r)
		}
	}
	if b.Len() > 0 {
		tokens = append(tokens, b.String())
	}
	var fallback []string
	for _, token := range tokens {
		if p, v, ok := strings.Cut(token, ":"); ok && prefixes[strings.ToLower(p)] {
			q.Filters[strings.ToLower(p)] = append(q.Filters[strings.ToLower(p)], strings.Trim(v, `"`))
			continue
		}
		parts := strings.FieldsFunc(strings.ToLower(token), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
		if len(parts) == 0 {
			continue
		}
		term := strings.Join(parts, " ")
		for _, p := range parts {
			if intent[p] {
				q.DecisionIntent = true
			}
		}
		fallback = append(fallback, term)
		if len(parts) == 1 && framing[term] && !strings.HasPrefix(token, `"`) {
			continue
		}
		q.Terms = append(q.Terms, term)
	}
	if len(q.Terms) == 0 {
		q.Terms = fallback
	}
	if len(q.Terms) > 8 {
		q.Terms = q.Terms[:8]
	}
	return q
}

func (q Query) match(op string) string {
	terms := make([]string, len(q.Terms))
	for i, t := range q.Terms {
		terms[i] = literal(t)
	}
	return strings.Join(terms, op)
}
