// Command synthetic-home writes a deterministic, entirely synthetic agent home
// for integration and gate runs. Used as $HOME (with COSLASH_HOME set
// separately), a real Local build discovers and backs up its Codex, Claude
// Code, OpenCode and Cursor sessions as it would real ones. It is a test tool,
// never part of a release build.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

const (
	manifestSchema = "coslash-synthetic-home/v1"
	recentWindow   = 72 * time.Hour
	padChunk       = 16 << 10
	minSessions    = 3
)

type options struct {
	out              string
	seed             uint64
	sessionsPerAgent int
	bytesPerSession  int64
	now              time.Time
}

type manifest struct {
	Schema           string            `json:"schema"`
	Seed             uint64            `json:"seed"`
	Now              string            `json:"now"`
	SessionsPerAgent int               `json:"sessionsPerAgent"`
	BytesPerSession  int64             `json:"bytesPerSession"`
	Environment      map[string]string `json:"environment"`
	Counts           map[string]counts `json:"counts"`
	Sessions         []sessionEntry    `json:"sessions"`
	EdgeCases        []edgeCase        `json:"edgeCases"`
}

type counts struct {
	Sessions   int `json:"sessions"`
	Recent     int `json:"recent"`
	History    int `json:"history"`
	Members    int `json:"members"`
	Hidden     int `json:"hidden"`
	Unreadable int `json:"unreadable"`
}

// sessionEntry names one root family. ExpectedProblem is empty when the
// family is expected to prepare, otherwise the non-retryable problem code.
type sessionEntry struct {
	Agent           string   `json:"agent"`
	ID              string   `json:"id"`
	Lane            string   `json:"lane,omitempty"`
	Recent          bool     `json:"recent"`
	StartedAt       string   `json:"startedAt"`
	LastActivity    string   `json:"lastActivity"`
	Members         []string `json:"members"`
	Hidden          []string `json:"hidden,omitempty"`
	FileChanges     int      `json:"fileChanges"`
	ExpectedProblem string   `json:"expectedProblem,omitempty"`
	Labels          []string `json:"labels,omitempty"`
	Paths           []string `json:"paths"`
}

type edgeCase struct {
	Label     string   `json:"label"`
	Agent     string   `json:"agent"`
	SessionID string   `json:"sessionId,omitempty"`
	Detail    string   `json:"detail"`
	Paths     []string `json:"paths"`
}

func main() {
	opts, err := parseFlags(os.Args[1:])
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "synthetic-home:", err)
		os.Exit(2)
	}
	result, err := generate(opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "synthetic-home:", err)
		os.Exit(1)
	}
	root, _ := filepath.Abs(opts.out)
	fmt.Printf("wrote %d synthetic sessions and %d edge cases to %s\n", len(result.Sessions), len(result.EdgeCases), root)
	fmt.Print("run Local with COSLASH_HOME outside this directory and:")
	for _, name := range slices.Sorted(maps.Keys(result.Environment)) {
		fmt.Printf(" %s=%s", name, filepath.Join(root, filepath.FromSlash(result.Environment[name])))
	}
	fmt.Println()
}

func parseFlags(args []string) (options, error) {
	flags := flag.NewFlagSet("synthetic-home", flag.ContinueOnError)
	out := flags.String("out", "", "empty or missing directory to write the synthetic home into (required)")
	seed := flags.Uint64("seed", 1, "seed for all generated identities and text")
	sessions := flags.Int("sessions-per-agent", 8, fmt.Sprintf("healthy root sessions per agent (at least %d); Cursor alternates IDE and CLI", minSessions))
	size := flags.Int64("bytes-per-session", 0, "pad each session's transcript with synthetic text to at least this many bytes")
	now := flags.String("now", "", "RFC3339 reference time for recent and history sessions (default: current time)")
	if err := flags.Parse(args); err != nil {
		return options{}, err
	}
	if flags.NArg() != 0 {
		return options{}, fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}
	reference := time.Now().UTC().Truncate(time.Second)
	if *now != "" {
		parsed, err := time.Parse(time.RFC3339, *now)
		if err != nil {
			return options{}, fmt.Errorf("--now: %w", err)
		}
		reference = parsed.UTC()
	}
	return options{out: *out, seed: *seed, sessionsPerAgent: *sessions, bytesPerSession: *size, now: reference}, nil
}

type generator struct {
	opts     options
	root     string
	manifest *manifest
	projects []string
}

func generate(opts options) (*manifest, error) {
	if opts.out == "" {
		return nil, errors.New("--out is required")
	}
	if opts.sessionsPerAgent < minSessions {
		return nil, fmt.Errorf("--sessions-per-agent must be at least %d", minSessions)
	}
	if opts.bytesPerSession < 0 {
		return nil, errors.New("--bytes-per-session must not be negative")
	}
	root, err := filepath.Abs(opts.out)
	if err != nil {
		return nil, err
	}
	if err := requireEmpty(root); err != nil {
		return nil, err
	}
	g := &generator{opts: opts, root: root, manifest: &manifest{
		Schema: manifestSchema, Seed: opts.seed, Now: opts.now.Format(time.RFC3339),
		SessionsPerAgent: opts.sessionsPerAgent, BytesPerSession: opts.bytesPerSession,
		Environment: map[string]string{"HOME": ".", "XDG_DATA_HOME": ".local/share", "XDG_CONFIG_HOME": ".config"},
		Counts:      map[string]counts{}, Sessions: []sessionEntry{}, EdgeCases: []edgeCase{},
	}}
	for _, name := range []string{"synthetic-alpha", "synthetic-beta", "synthetic-gamma"} {
		workspace := filepath.Join(root, "workspaces", name)
		if err := os.MkdirAll(workspace, 0o700); err != nil {
			return nil, err
		}
		g.projects = append(g.projects, workspace)
	}
	for stream, write := range []func(source) error{g.writeCodex, g.writeClaude, g.writeOpenCode, g.writeCursor} {
		if err := write(newSource(opts.seed, uint64(stream)+1)); err != nil {
			return nil, err
		}
	}
	for _, entry := range g.manifest.Sessions {
		total := g.manifest.Counts[entry.Agent]
		total.Sessions++
		if entry.Recent {
			total.Recent++
		} else {
			total.History++
		}
		total.Members += len(entry.Members)
		total.Hidden += len(entry.Hidden)
		if entry.ExpectedProblem != "" {
			total.Unreadable++
		}
		g.manifest.Counts[entry.Agent] = total
	}
	data, err := json.MarshalIndent(g.manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), append(data, '\n'), 0o600); err != nil {
		return nil, err
	}
	return g.manifest, nil
}

// requireEmpty refuses to write into an existing home so the tool can never
// add to or overwrite real agent data.
func requireEmpty(root string) error {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return os.MkdirAll(root, 0o700)
	}
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("%s is not empty; choose a new directory", root)
	}
	return nil
}

func (g *generator) add(entry sessionEntry) {
	g.manifest.Sessions = append(g.manifest.Sessions, entry)
}

func (g *generator) edge(label, agent, id, detail string, paths ...string) {
	g.manifest.EdgeCases = append(g.manifest.EdgeCases, edgeCase{Label: label, Agent: agent, SessionID: id, Detail: detail, Paths: paths})
}

// write creates a private file below the home and dates it to the session's
// last activity, which is what file-backed readers see as activity time.
func (g *generator) write(relative string, data []byte, modified time.Time) error {
	path := g.path(relative)
	if err := makeParent(path); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	return os.Chtimes(path, modified, modified)
}

func (g *generator) path(relative string) string {
	return filepath.Join(g.root, filepath.FromSlash(relative))
}

func makeParent(path string) error { return os.MkdirAll(filepath.Dir(path), 0o700) }

func joinClose(err error, closer io.Closer) error { return errors.Join(err, closer.Close()) }

// timing places a session relative to --now: even indexes inside the recent
// window with a two-hour margin, odd ones in history.
type timing struct {
	start, end time.Time
	recent     bool
}

func (g *generator) timing(r source, index int) timing {
	var ago time.Duration
	recent := index%2 == 0
	if recent {
		ago = time.Duration(30+r.IntN(int((recentWindow-2*time.Hour)/time.Minute)-30)) * time.Minute
	} else {
		ago = recentWindow + 24*time.Hour + time.Duration(r.IntN(56*24))*time.Hour
	}
	end := g.opts.now.Add(-ago - time.Duration(r.IntN(60))*time.Second).Truncate(time.Second)
	return timing{start: end.Add(-time.Duration(5+r.IntN(40)) * time.Minute), end: end, recent: recent}
}

func (t timing) entry(agent, id string) sessionEntry {
	return sessionEntry{Agent: agent, ID: id, Recent: t.recent, StartedAt: stamp(t.start), LastActivity: stamp(t.end), Members: []string{id}}
}

// clock hands out non-decreasing event times inside a session; last is the
// session's exact end.
type clock struct {
	r        source
	now, end time.Time
}

func (t timing) clock(r source) *clock { return &clock{r: r, now: t.start, end: t.end} }

func (c *clock) next() time.Time {
	c.now = c.now.Add(time.Duration(2+c.r.IntN(14)) * time.Second)
	if c.now.After(c.end) {
		c.now = c.end
	}
	return c.now
}

func (c *clock) last() time.Time {
	c.now = c.end
	return c.end
}

func stamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

type object = map[string]any

type jsonl struct{ bytes.Buffer }

func (l *jsonl) add(value any) {
	l.WriteString(compact(value))
	l.WriteByte('\n')
}

// compact encodes like the agents do, without HTML escaping.
func compact(value any) string {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		panic(err)
	}
	return strings.TrimSuffix(buffer.String(), "\n")
}

// padded joins head and tail with synthetic filler rows until the transcript
// reaches the --bytes-per-session target.
func (g *generator) padded(head, tail *jsonl, row func(text string) any) []byte {
	target := g.opts.bytesPerSession
	for salt := 0; int64(head.Len()+tail.Len()) < target; salt++ {
		size := min(padChunk, max(256, int(target-int64(head.Len()+tail.Len()))))
		head.add(row(filler(size, salt)))
	}
	return append(head.Bytes(), tail.Bytes()...)
}

var fillerWords = strings.Fields("synthetic filler text generated for coslash scale testing only it carries no real data")

func filler(size, salt int) string {
	var text strings.Builder
	for index := salt; text.Len() < size; index++ {
		text.WriteString(fillerWords[index%len(fillerWords)])
		text.WriteByte(' ')
	}
	return strings.TrimSpace(text.String()[:size])
}

var (
	actions  = []string{"Refactor", "Add tests for", "Fix the edge case in", "Document", "Speed up", "Simplify", "Add logging to", "Rename fields in"}
	subjects = []string{"parser", "scheduler", "cache layer", "config loader", "retry queue", "metrics exporter", "flag handling", "request handler", "migration step", "test harness"}
)

type topic struct{ action, subject, project string }

func (g *generator) topic(r source, index int) topic {
	return topic{action: pick(r, actions), subject: pick(r, subjects), project: filepath.Base(g.projects[index%len(g.projects)])}
}

func (t topic) prompt() string {
	return fmt.Sprintf("%s the %s in the %s project. This is a synthetic request.", t.action, t.subject, t.project)
}

func (t topic) title() string { return fmt.Sprintf("Synthetic: %s the %s", t.action, t.subject) }

func (t topic) reply() string {
	return fmt.Sprintf("Updated the %s and ran the synthetic checks; nothing else changed.", t.subject)
}

func (t topic) file(index int) string {
	return fmt.Sprintf("src/%s/part-%03d.txt", strings.ReplaceAll(t.subject, " ", "-"), index)
}

func (t topic) content(index int) string {
	return fmt.Sprintf("synthetic %s content %03d\n", t.subject, index)
}

type source struct{ *rand.Rand }

func newSource(seed, stream uint64) source { return source{rand.New(rand.NewPCG(seed, stream))} }

func (r source) uuid() string {
	var value [16]byte
	for index := range value {
		value[index] = byte(r.Uint32())
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16])
}

const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

func (r source) token(size int, letters string) string {
	value := make([]byte, size)
	for index := range value {
		value[index] = letters[r.IntN(len(letters))]
	}
	return string(value)
}

func (r source) hex(size int) string { return r.token(size, "0123456789abcdef") }

func pick(r source, items []string) string { return items[r.IntN(len(items))] }

// cursorGlobalStorage mirrors where Cursor's reader looks for state.vscdb on
// the platform the home is generated for.
func cursorGlobalStorage() string {
	if runtime.GOOS == "windows" {
		return "AppData/Roaming/Cursor/User/globalStorage"
	}
	return "Library/Application Support/Cursor/User/globalStorage"
}

// slug is the folder name Claude Code and Cursor derive from a workspace path.
func slug(path string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, filepath.ToSlash(path))
}
