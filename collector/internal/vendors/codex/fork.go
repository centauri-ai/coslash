package codex

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

// Fork recomputes a forked rollout's Tokens against its parent's cumulative
// prefix — a fork replays the parent's token_count sequence, so everything
// before the divergence belongs to the parent. An unresolvable or unreadable
// parent leaves the full cumulative usage in place; over-counting is the
// deliberate failure mode, never under-counting.
func ApplyForkedUsage(parsed []*vendors.ParsedTranscript, discovered map[string]string) {
	forks := []*vendors.ParsedTranscript{}
	index := make(map[string]string, len(discovered)+len(parsed))
	parsedUsages := make(map[string][]codexTokenUsage, len(parsed))
	for id, path := range discovered {
		index[id] = path
	}
	for _, p := range parsed {
		f, ok := p.ForkUsage.(codexFork)
		if !ok {
			continue
		}
		index[p.Session.ID] = p.Session.LogPath
		usages := make([]codexTokenUsage, len(f.samples))
		for i, sample := range f.samples {
			usages[i] = sample.usage
		}
		parsedUsages[p.Session.ID] = usages
		if f.forkedFromID != "" {
			forks = append(forks, p)
		}
	}
	if len(forks) == 0 {
		return
	}
	// A fork's parent can be archived after the fork. Walked only when a fork
	// exists; a batch (live) path wins over an archived copy of the same id.
	home, err := os.UserHomeDir()
	if err != nil {
		log.Printf("archived sessions: %v; continuing without archived parents", err)
	} else {
		archivedDir := filepath.Join(home, ".codex", "archived_sessions")
		archived, err := vendors.JSONLFilesUnder(archivedDir)
		if err != nil {
			log.Printf("archived sessions dir %q: %v; continuing without archived parents", archivedDir, err)
		}
		for _, file := range archived {
			if id := SessionIDFromRollout(file); id != "" && index[id] == "" {
				index[id] = file
			}
		}
	}
	for _, p := range forks {
		fork := p.ForkUsage.(codexFork)
		parentPath := index[fork.forkedFromID]
		if parentPath == "" || parentPath == p.Session.LogPath {
			continue
		}
		forkSeq := make([]codexTokenUsage, len(fork.samples))
		for i, sample := range fork.samples {
			forkSeq[i] = sample.usage
		}
		parentUsages, ok := parsedUsages[fork.forkedFromID]
		if !ok {
			var err error
			parentUsages, err = parentForkUsages(parentPath, forkSeq)
			if err != nil {
				log.Printf(
					"%s: fork parent %q unreadable; counting full usage: %v",
					p.Session.LogPath, parentPath, err,
				)
				continue
			}
		}
		p.Session.Tokens = tokenBuckets(fork.samples, parentUsages, p.Session.LogPath)
	}
}

// parentForkUsages streams a parent rollout's cumulative token_count sequence
// only as far as the fork needs it: it stops at the first entry that diverges
// from forkSeq (or once the whole fork prefix has matched), since nothing the
// parent recorded past the fork point affects token attribution. This avoids
// fully parsing a parent that may be long-running. The returned slice is enough
// for tokenBuckets to find the shared prefix.
func parentForkUsages(path string, forkSeq []codexTokenUsage) ([]codexTokenUsage, error) {
	file, err := os.Open(path)
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
