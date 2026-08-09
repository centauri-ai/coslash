package sessiondetail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/contracts"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/claude"
	"github.com/centauri-ai/coslash/collector/internal/vendors/codex"
)

const (
	defaultMaxTranscriptBytes = 32 << 20
	defaultMaxProjectionBytes = 8 << 20
	defaultMaxRows            = 100_000
)

// Resolver resolves an ID only within one configured vendor. Agent selection
// happens before Resolve is called, preventing a bare ID collision from
// selecting another vendor's transcript.
type Resolver func(context.Context, string) (string, error)

type Options struct {
	ClaudeResolver     Resolver
	CodexResolver      Resolver
	MaxTranscriptBytes int64
	MaxProjectionBytes int64
	MaxRows            int
}

type Projector struct {
	claudeResolver     Resolver
	codexResolver      Resolver
	maxTranscriptBytes int64
	maxProjectionBytes int64
	maxRows            int
}

func New(options Options) *Projector {
	if options.ClaudeResolver == nil {
		options.ClaudeResolver = defaultClaudeResolver
	}
	if options.CodexResolver == nil {
		options.CodexResolver = defaultCodexResolver
	}
	if options.MaxTranscriptBytes <= 0 {
		options.MaxTranscriptBytes = defaultMaxTranscriptBytes
	}
	if options.MaxProjectionBytes <= 0 {
		options.MaxProjectionBytes = defaultMaxProjectionBytes
	}
	if options.MaxRows <= 0 {
		options.MaxRows = defaultMaxRows
	}
	return &Projector{
		claudeResolver:     options.ClaudeResolver,
		codexResolver:      options.CodexResolver,
		maxTranscriptBytes: options.MaxTranscriptBytes,
		maxProjectionBytes: options.MaxProjectionBytes,
		maxRows:            options.MaxRows,
	}
}

func (p *Projector) Project(ctx context.Context, identity contracts.SessionIdentity) (*Detail, error) {
	if identity.Agent == "" || identity.ID == "" {
		return nil, ErrIdentityMismatch
	}
	var resolver Resolver
	switch identity.Agent {
	case vendors.AgentClaude:
		resolver = p.claudeResolver
	case vendors.AgentCodex:
		resolver = p.codexResolver
	default:
		return nil, ErrUnknownAgent
	}
	path, err := resolver(ctx, identity.ID)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if path == "" {
		return nil, ErrNotFound
	}
	return p.ProjectFile(ctx, identity, path)
}

// ProjectFile is useful for isolated tests and imports that already resolved a
// server-known transcript. It still verifies the full composite identity.
func (p *Projector) ProjectFile(ctx context.Context, identity contracts.SessionIdentity, path string) (*Detail, error) {
	return p.projectFile(ctx, identity, path, nil)
}

// ProjectKnown preserves a session already resolved by coSlash's collector
// (including names, status, subagents, synthesis, and probes) and adds only the
// expensive Canvas fields. The transcript identity is independently verified.
func (p *Projector) ProjectKnown(ctx context.Context, known session.Session, path string) (*Detail, error) {
	identity := contracts.SessionIdentity{Agent: known.Agent, ID: known.ID}
	return p.projectFile(ctx, identity, path, &known)
}

func (p *Projector) projectFile(ctx context.Context, identity contracts.SessionIdentity, path string, known *session.Session) (*Detail, error) {
	if identity.Agent == "" || identity.ID == "" {
		return nil, ErrIdentityMismatch
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, ErrNotFound
	}
	if info.Size() > p.maxTranscriptBytes {
		return nil, ErrTranscriptTooLarge
	}

	var parsedSession *session.Session
	var heavy *heavyDetail
	switch identity.Agent {
	case vendors.AgentClaude:
		parsed, parseErr := claude.Parse(path)
		if parseErr != nil {
			return nil, fmt.Errorf("%w: %v", ErrMalformedTranscript, parseErr)
		}
		parsedSession = parsed.Session
		heavy, err = p.projectClaude(ctx, path)
	case vendors.AgentCodex:
		parsed, parseErr := codex.Parse(path)
		if parseErr != nil {
			return nil, fmt.Errorf("%w: %v", ErrMalformedTranscript, parseErr)
		}
		parsedSession = parsed.Session
		heavy, err = p.projectCodex(ctx, path)
	default:
		return nil, ErrUnknownAgent
	}
	if err != nil {
		return nil, err
	}
	if parsedSession == nil || parsedSession.Agent != identity.Agent || parsedSession.ID != identity.ID {
		return nil, ErrIdentityMismatch
	}
	base := *parsedSession
	if known != nil {
		if known.Agent != identity.Agent || known.ID != identity.ID {
			return nil, ErrIdentityMismatch
		}
		base = *known
	}
	detail := heavy.finish(base)
	encoded, marshalErr := json.Marshal(detail)
	if marshalErr != nil {
		return nil, marshalErr
	}
	if int64(len(encoded)) > p.maxProjectionBytes {
		return nil, ErrProjectionTooLarge
	}
	return detail, nil
}

func defaultClaudeResolver(ctx context.Context, id string) (string, error) {
	files, err := claude.Files()
	if err != nil {
		return "", err
	}
	for _, path := range files {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if claude.ParentIDFromPath(path) == "" && claude.IDFromPath(path) == id {
			return path, nil
		}
	}
	return "", ErrNotFound
}

func defaultCodexResolver(ctx context.Context, id string) (string, error) {
	files, err := codex.Files()
	if err != nil {
		return "", err
	}
	for _, path := range files {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if codex.SessionIDFromRollout(path) == id {
			return path, nil
		}
	}
	return "", ErrNotFound
}
