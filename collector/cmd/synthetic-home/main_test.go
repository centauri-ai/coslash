package main

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/collector"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/sessionbackupproducer"
	sessionbackupv1 "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
)

var testNow = time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

// The generated home must look like real agent data to Local: every session is
// discovered by the collector, every healthy family passes complete backup and
// independent verification, and every deliberately unreadable family fails
// with problems the sync runner parks instead of retrying.
func TestSyntheticHomeIsDiscoveredAndBackedUp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the synthetic home reproduces the macOS and Linux agent layouts")
	}
	home := t.TempDir()
	result, err := generate(options{out: home, seed: 7, sessionsPerAgent: 4, bytesPerSession: 4096, now: testNow})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("COSLASH_HOME", t.TempDir())

	sessions, err := collector.List(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	discovered := map[string]*session.Session{}
	for _, item := range sessions {
		discovered[item.Agent+"/"+item.ID] = item
	}
	lanes := map[string]int{}
	for _, entry := range result.Sessions {
		item := discovered[entry.Agent+"/"+entry.ID]
		if item == nil {
			t.Errorf("%s %s %v was not discovered", entry.Agent, entry.ID, entry.Labels)
			continue
		}
		if entry.Lane != "" {
			if item.Entrypoint == nil || *item.Entrypoint != entry.Lane {
				t.Errorf("%s lane = %v, want %s", entry.ID, item.Entrypoint, entry.Lane)
			}
			lanes[entry.Lane]++
		}
		if len(item.Subagents) != len(entry.Members)-1 {
			t.Errorf("%s %s subagents = %d, want %d", entry.Agent, entry.ID, len(item.Subagents), len(entry.Members)-1)
		}
	}
	if len(sessions) != len(result.Sessions) || lanes[laneIDE] == 0 || lanes[laneCLI] == 0 {
		t.Fatalf("discovered %d root sessions, manifest has %d; Cursor lanes %v", len(sessions), len(result.Sessions), lanes)
	}
	labels := map[string]int{}
	for _, edge := range result.EdgeCases {
		labels[edge.Label]++
		for _, relative := range edge.Paths {
			if _, err := os.Stat(filepath.Join(home, filepath.FromSlash(relative))); err != nil {
				t.Errorf("%s path %s: %v", edge.Label, relative, err)
			}
		}
	}
	if labels["unreadable"] != 4 || labels["guardian-family"] != 1 || labels["file-changes-100"] != 1 || labels["stray-file"] != 1 {
		t.Fatalf("edge cases = %v", labels)
	}

	spool := t.TempDir()
	manager := sessionbackupproducer.New(sessionbackupproducer.Options{Root: spool, LocalHome: func() (string, error) { return home, nil }})
	for _, entry := range result.Sessions {
		prepared, err := manager.Prepare(t.Context(), sessionbackupproducer.Selection{
			SourceKind: sessionbackupv1.SourceLocal, SourceID: "local", Agent: entry.Agent, SessionID: entry.ID,
		})
		var failure *sessionbackupproducer.PreparationError
		if entry.ExpectedProblem != "" {
			if !errors.As(err, &failure) || len(failure.Coverage.Problems) == 0 || failure.Coverage.Problems[0].Code != entry.ExpectedProblem {
				t.Errorf("%s unreadable %s: prepared=%v err=%v", entry.Agent, entry.ID, prepared != nil, err)
				continue
			}
			for _, problem := range failure.Coverage.Problems {
				if problem.Retryable {
					t.Errorf("%s unreadable %s reports a retryable %s", entry.Agent, entry.ID, problem.Code)
				}
			}
			continue
		}
		if err != nil {
			if errors.As(err, &failure) {
				t.Errorf("%s %s %v: %v %#v", entry.Agent, entry.ID, entry.Labels, err, failure.Coverage.Problems)
				continue
			}
			t.Fatal(err)
		}
		verified, err := sessionbackupv1.VerifyDirectory(filepath.Join(spool, prepared.BundleID))
		if err != nil || verified.CompleteBackupSHA256 != prepared.BundleID {
			t.Errorf("%s %s verification: %v", entry.Agent, entry.ID, err)
			continue
		}
		var members []string
		for _, member := range verified.Members {
			members = append(members, member.MemberID)
		}
		changes := 0
		for _, artifact := range verified.Artifacts {
			if artifact.Kind == sessionbackupv1.KindExactChangeBody {
				changes++
			}
			if slices.Contains(entry.Hidden, artifact.MemberID) {
				t.Errorf("hidden %s became a member artifact", artifact.MemberID)
			}
		}
		if !sameSet(members, entry.Members) || changes != entry.FileChanges {
			t.Errorf("%s %s members=%v changes=%d, manifest members=%v changes=%d", entry.Agent, entry.ID, members, changes, entry.Members, entry.FileChanges)
		}
		if slices.Contains(entry.Labels, "file-changes-100") && len(verified.Artifacts) <= 64 {
			t.Errorf("file-change session has %d artifacts, want more than 64", len(verified.Artifacts))
		}
		if slices.Contains(entry.Labels, "guardian-family") {
			guardian, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(entry.Paths[1])))
			if err != nil {
				t.Fatal(err)
			}
			if !bundleHolds(t, filepath.Join(spool, prepared.BundleID), verified, entry.ID, guardian) {
				t.Error("guardian rollout bytes are not retained under the reviewed root member")
			}
		}
	}
}

func TestSyntheticHomeIsDeterministic(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	digests := func(seed uint64) map[string][32]byte {
		t.Helper()
		if err := os.RemoveAll(home); err != nil {
			t.Fatal(err)
		}
		if _, err := generate(options{out: home, seed: seed, sessionsPerAgent: 3, bytesPerSession: 1024, now: testNow}); err != nil {
			t.Fatal(err)
		}
		files := map[string][32]byte{}
		err := filepath.WalkDir(home, func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			data, err := os.ReadFile(path)
			relative, _ := filepath.Rel(home, path)
			files[filepath.ToSlash(relative)] = sha256.Sum256(data)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return files
	}
	first, again, other := digests(3), digests(3), digests(4)
	if len(first) == 0 || !maps.Equal(first, again) {
		t.Fatal("the same seed and time produced different files")
	}
	if first["manifest.json"] == other["manifest.json"] {
		t.Fatal("a different seed produced the same manifest")
	}
	if _, err := generate(options{out: home, seed: 3, sessionsPerAgent: 3, now: testNow}); err == nil {
		t.Fatal("generation into a non-empty directory succeeded")
	}
}

func bundleHolds(t *testing.T, root string, manifest sessionbackupv1.Manifest, member string, want []byte) bool {
	t.Helper()
	for _, artifact := range manifest.Artifacts {
		if artifact.MemberID != member || artifact.Kind != sessionbackupv1.KindRawTranscript {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(artifact.LogicalName)))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Equal(data, want) {
			return true
		}
	}
	return false
}

func sameSet(left, right []string) bool {
	left, right = slices.Clone(left), slices.Clone(right)
	slices.Sort(left)
	slices.Sort(right)
	return slices.Equal(left, right)
}
