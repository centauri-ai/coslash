package codex

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

// Fork recomputes a forked rollout's Tokens against its parent's cumulative
// prefix — a fork replays the parent's token_count sequence, so everything
// before the divergence belongs to the parent. An unresolvable or unreadable
// parent leaves the full cumulative usage in place; over-counting is the
// deliberate failure mode, never under-counting.
func applyForkedUsageSource(
	source vendors.ReadSource,
	archivedDir string,
	parsed []*parsedSession,
) {
	_ = applyForkedUsageSourceContext(context.Background(), source, archivedDir, parsed)
}

func applyForkedUsageSourceContext(
	ctx context.Context,
	source vendors.ReadSource,
	archivedDir string,
	parsed []*parsedSession,
) error {
	forks := []*parsedSession{}
	index := map[string]string{}
	for _, p := range parsed {
		if err := ctx.Err(); err != nil {
			return err
		}
		f := p.fork
		index[p.transcript.Session.ID] = p.transcript.LogPath
		if f.forkedFromID != "" {
			forks = append(forks, p)
		}
	}
	if len(forks) == 0 {
		return nil
	}
	// A fork's parent can be archived after the fork. Walked only when a fork
	// exists; a batch (live) path wins over an archived copy of the same id.
	if archivedDir != "" {
		archived, err := vendors.JSONLFilesUnderSourceContext(ctx, source, archivedDir)
		if err != nil {
			log.Printf("archived sessions dir %q: %v; continuing without archived parents", archivedDir, err)
		}
		for _, file := range archived {
			if err := ctx.Err(); err != nil {
				return err
			}
			if id := SessionIDFromRollout(file); id != "" && index[id] == "" {
				index[id] = file
			}
		}
	}
	for _, p := range forks {
		if err := ctx.Err(); err != nil {
			return err
		}
		fork := p.fork
		parentPath := index[fork.forkedFromID]
		if parentPath == "" || parentPath == p.transcript.LogPath {
			continue
		}
		forkSeq := make([]codexTokenUsage, len(fork.samples))
		for i, sample := range fork.samples {
			forkSeq[i] = sample.usage
		}
		parentUsages, err := parentForkUsagesSourceContext(ctx, source, parentPath, forkSeq)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			log.Printf(
				"%s: fork parent %q unreadable; counting full usage: %v",
				p.transcript.LogPath, parentPath, err,
			)
			continue
		}
		p.transcript.Session.Tokens = tokenBuckets(
			fork.samples,
			parentUsages,
			p.transcript.LogPath,
		)
	}
	return nil
}

// parentForkUsages streams a parent rollout's cumulative token_count sequence
// only as far as the fork needs it: it stops at the first entry that diverges
// from forkSeq (or once the whole fork prefix has matched), since nothing the
// parent recorded past the fork point affects token attribution. This avoids
// fully parsing a parent that may be long-running. The returned slice is enough
// for tokenBuckets to find the shared prefix.
func parentForkUsagesSource(
	source vendors.ReadSource,
	path string,
	forkSeq []codexTokenUsage,
) ([]codexTokenUsage, error) {
	return parentForkUsagesSourceContext(context.Background(), source, path, forkSeq)
}

func parentForkUsagesSourceContext(
	ctx context.Context,
	source vendors.ReadSource,
	path string,
	forkSeq []codexTokenUsage,
) ([]codexTokenUsage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := source.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	// json.Decoder (like ParseJSONL) imposes no per-line size limit — rollout
	// lines can inline large tool output, and a bufio.Scanner cap would error
	// out and silently fall back to double-counting the parent.
	decoder := json.NewDecoder(file)
	var seq []codexTokenUsage
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var row codexRow
		if err := decoder.Decode(&row); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				// EOF, or a torn final line from a live-appended rollout.
				break
			}
			return nil, err
		}
		if row.Type != "event_msg" || row.Payload.Type != "token_count" ||
			row.Payload.Info == nil {
			continue
		}
		index := len(seq)
		usage := row.Payload.Info.TotalTokenUsage
		seq = append(seq, usage)
		if index >= len(forkSeq) || forkSeq[index] != usage {
			break
		}
	}
	return seq, nil
}
