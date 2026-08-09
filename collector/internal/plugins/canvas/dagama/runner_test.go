package dagama

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/publication"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/revision"
)

type noopAttemptDriver struct{}

func (noopAttemptDriver) Execute(context.Context, AttemptRequest) (AttemptResult, error) {
	return AttemptResult{}, nil
}
func (noopAttemptDriver) Cancel(context.Context, *RunState) ([]byte, error) { return nil, nil }
func (noopAttemptDriver) Takeover(context.Context, AttemptRequest, AttemptState) (AttemptResult, error) {
	return AttemptResult{}, nil
}
func (noopAttemptDriver) Handback(context.Context, AttemptRequest, AttemptState) (AttemptResult, error) {
	return AttemptResult{}, nil
}
func (noopAttemptDriver) Probe(context.Context, *RunState, AttemptState) (ProbeResult, error) {
	return ProbeResult{State: ProbeMissing}, nil
}
func (noopAttemptDriver) Rearm(context.Context, *RunState, AttemptState) error { return nil }
func (noopAttemptDriver) Cleanup(context.Context, *RunState) error             { return nil }

type noopGitHub struct{}

func (noopGitHub) Run(context.Context, []string, string) (publication.GitHubResult, error) {
	return publication.GitHubResult{}, nil
}

func TestProductionRuntimeUsesAnIsolatedRealGitClone(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	runGit(t, project, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(project, "README.md"), []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, project, "add", "README.md")
	runGit(t, project, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "initial")
	hooks := filepath.Join(root, "hooks")
	if err := os.MkdirAll(hooks, 0o700); err != nil {
		t.Fatal(err)
	}
	git, err := revision.NewGit(revision.NewExecRunner(), hooks)
	if err != nil {
		t.Fatal(err)
	}
	publisher, err := publication.NewPublisher(git, noopGitHub{}, func() time.Time { return time.Unix(1, 0).UTC() })
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := NewProductionRuntime(ProductionRuntimeOptions{Git: git, Publisher: publisher, Attempts: noopAttemptDriver{}, Now: func() time.Time { return time.Unix(1, 0).UTC() }})
	if err != nil {
		t.Fatal(err)
	}
	runRoot := filepath.Join(root, "roots", "run-19700101t000001-deadbeef")
	prepared, err := runtime.Prepare(ctx, PrepareRequest{ProjectPath: project, RunID: "run-19700101t000001-deadbeef", RunRoot: runRoot, Branch: "dagama/run-19700101t000001-deadbeef"})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Root.Path != runRoot || prepared.Root.InPlace {
		t.Fatalf("unexpected run root: %+v", prepared.Root)
	}
	if _, err := os.Stat(filepath.Join(runRoot, ".git")); err != nil {
		t.Fatalf("isolated .git missing: %v", err)
	}
	if status := runGitOutput(t, project, "status", "--porcelain"); status != "" {
		t.Fatalf("source worktree changed: %q", status)
	}
	record, err := runtime.RecordControllerArtifact(ctx, runRoot, "SOURCE.md", []byte("source\n"), ProducerRef{Component: ComponentIntake, Instance: 1})
	if err != nil {
		t.Fatal(err)
	}
	contents, err := runtime.ReadArtifact(ctx, runRoot, record)
	if err != nil || string(contents) != "source\n" {
		t.Fatalf("artifact round trip failed: %q %v", contents, err)
	}
	state := &RunState{RunRoot: runRoot, Components: map[ComponentID]*ComponentRunState{ComponentBuild: {Instance: 1}}}
	if err := runtime.Cleanup(ctx, state); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(runRoot); !os.IsNotExist(err) {
		t.Fatalf("disposable run root remains: %v", err)
	}
}

func runGit(t *testing.T, directory string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = directory
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}
func runGitOutput(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = directory
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(output)
}
