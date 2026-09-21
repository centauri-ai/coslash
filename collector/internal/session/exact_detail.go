package session

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// WithLocalChangeIDs binds each parsed change body to an opaque, stable ID.
func WithLocalChangeIDs(value Session) Session {
	result, _ := withLocalChangeIDsContext(context.Background(), value)
	return result
}

func withLocalChangeIDsContext(ctx context.Context, value Session) (Session, error) {
	value.FileEdits = append([]FileEdit(nil), value.FileEdits...)
	occurrences := map[string]int{}
	for editIndex := range value.FileEdits {
		if err := ctx.Err(); err != nil {
			return Session{}, err
		}
		changes := value.FileEdits[editIndex].Changes()
		value.FileEdits[editIndex].ChangeIDs = make([]string, len(changes))
		for changeIndex, change := range changes {
			if err := ctx.Err(); err != nil {
				return Session{}, err
			}
			identity, _ := json.Marshal(struct {
				Path      string `json:"path"`
				Kind      string `json:"kind"`
				Text      string `json:"text"`
				Operation string `json:"operation"`
				Additions int    `json:"additions"`
				Deletions int    `json:"deletions"`
			}{
				Path: value.FileEdits[editIndex].Path, Kind: change.Kind, Text: change.Text,
				Operation: change.Operation, Additions: change.Additions, Deletions: change.Deletions,
			})
			digest := sha256.Sum256(identity)
			base := "change-" + base64.RawURLEncoding.EncodeToString(digest[:])
			occurrence := occurrences[base]
			occurrences[base]++
			if occurrence > 0 {
				base = fmt.Sprintf("%s-%06d", base, occurrence)
			}
			value.FileEdits[editIndex].ChangeIDs[changeIndex] = base
		}
	}
	return value, nil
}

// LocalDetailRevision fingerprints parsed content while excluding live facts
// that are overlaid independently by the session list.
func LocalDetailRevision(value Session) (string, error) {
	return LocalDetailRevisionContext(context.Background(), value)
}

func LocalDetailRevisionContext(ctx context.Context, value Session) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	stable := Clone(&value)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	stable.Status = nil
	stable.Branch = nil
	stable.Repository = nil
	stable.RepositoryLocalOnly = false
	stable.Commits = nil
	stable.CommitSHAs = nil
	stable.Git = nil
	stable.GitProbed = false
	stable.LastEditAt = nil
	stable.ReviewPending = false
	stable.ReviewError = ""
	stable.Synthesis = nil
	stable.SynthesisPending = false
	stable.DetailRevision = ""
	if stable.ActivityFallback {
		stable.LastActivityTime = 0
	}
	for index := range stable.Subagents {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		stable.Subagents[index].Status = ""
	}
	normalizeDetailCollections(stable)
	identified, err := withLocalChangeIDsContext(ctx, *stable)
	if err != nil {
		return "", err
	}

	payload, err := json.Marshal(struct {
		Session   Session             `json:"session"`
		CommitLog []CommitObservation `json:"commitLog"`
	}{
		Session:   identified,
		CommitLog: stable.CommitLog,
	})
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func normalizeDetailCollections(value *Session) {
	if value.Tokens == nil {
		value.Tokens = map[string]ModelTokens{}
	}
	if value.UnpricedModels == nil {
		value.UnpricedModels = []string{}
	}
	if value.Subagents == nil {
		value.Subagents = []Subagent{}
	}
	for index := range value.Subagents {
		if value.Subagents[index].Commands == nil {
			value.Subagents[index].Commands = []SubagentCommand{}
		}
		if value.Subagents[index].Tokens == nil {
			value.Subagents[index].Tokens = map[string]ModelTokens{}
		}
	}
	if value.Commands == nil {
		value.Commands = []string{}
	}
	if value.Commits == nil {
		value.Commits = []string{}
	}
	if value.Todos == nil {
		value.Todos = []Todo{}
	}
	if value.Digest == nil {
		value.Digest = []DigestEntry{}
	}
	if value.FileEdits == nil {
		value.FileEdits = []FileEdit{}
	}
	if value.Synthesis != nil {
		if value.Synthesis.Goals == nil {
			value.Synthesis.Goals = []string{}
		}
		if value.Synthesis.KeyDecisions == nil {
			value.Synthesis.KeyDecisions = []string{}
		}
	}
}
