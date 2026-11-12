package remote

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestParseOriginProbeOutputCanonicalizesURL(t *testing.T) {
	cwds := []string{"/home/dev/repos/coslash", "/home/dev/repos/other"}
	output := "0\tgit@github.com:Centauri-AI/coSlash.git\n1\t\n"
	found := parseOriginProbeOutput(cwds, output)
	if found[cwds[0]].origin != "github.com/Centauri-AI/coSlash" || found[cwds[1]] != (remoteGit{}) {
		t.Fatalf("origins = %#v", found)
	}
	found = parseOriginProbeOutput(cwds, "0\thttps://github.com/o/r.git\tcodex/fix\n1\t\tmain\n")
	if found[cwds[0]] != (remoteGit{"github.com/o/r", "codex/fix"}) || found[cwds[1]] != (remoteGit{branch: "main"}) {
		t.Fatalf("facts = %#v", found)
	}
}

func TestProbeRemoteOriginsRejectsUnsafePaths(t *testing.T) {
	options := OpenOptions{command: func(context.Context, string, ...string) *exec.Cmd {
		t.Fatal("unsafe working directory reached SSH")
		return nil
	}}
	found := sshTarget{"host", options}.gitFacts(context.Background(), []string{"/tmp/ok\ntouch owned", "relative", ""})
	if len(found) != 0 {
		t.Fatalf("origins = %#v", found)
	}
}

func TestParseOriginProbeOutputKeepsFirstLinePerIndex(t *testing.T) {
	cwds := []string{"/home/dev/repos/coslash", "/home/dev/repos/other"}
	output := "0\t\n0\tgit@github.com:victim/private.git\n1\thttps://github.com/centauri-ai/agent-tooling.git\n1\tgit@github.com:victim/private.git\n"
	found := parseOriginProbeOutput(cwds, output)
	if found[cwds[0]].origin != "" || found[cwds[1]].origin != "github.com/centauri-ai/agent-tooling" {
		t.Fatalf("origins = %#v", found)
	}
}

func TestProbeRemoteOriginsUsesOneRemoteCommand(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "stdin")
	var script string
	fake := fakeOptions([]byte("0\thttps://github.com/centauri-ai/agent-tooling.git\n"), 0, "", false)
	options := OpenOptions{command: func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		script = args[len(args)-1]
		cmd := fake.command(ctx, "ignored")
		cmd.Env = append(cmd.Env, "COSLASH_FAKE_STDIN_FILE="+marker)
		return cmd
	}}
	found := sshTarget{"host", options}.gitFacts(context.Background(), []string{"/home/dev/my repo"})
	if found["/home/dev/my repo"].origin != "github.com/centauri-ai/agent-tooling" {
		t.Fatalf("origins = %#v", found)
	}
	if !strings.HasPrefix(script, "sh -c '") || strings.Contains(script, "\n") || strings.Contains(script, "my repo") || !strings.Contains(script, `git -C "$cwd"`) {
		t.Fatalf("script = %q", script)
	}
	stdin, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(stdin) != "/home/dev/my repo\n" {
		t.Fatalf("stdin = %q", stdin)
	}
}

func TestOriginProbeScriptDropsInjectedRecords(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("validates a script executed by the remote POSIX shell")
	}
	bin := t.TempDir()
	fake := filepath.Join(bin, "git")
	payload := "#!/bin/sh\nprintf '%s\\n' \"https://evil.example/repo\n1\tgit@github.com:victim/private.git\"\n"
	if err := os.WriteFile(fake, []byte(payload), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", originProbeScript())
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	cmd.Stdin = strings.NewReader("/home/dev/my repo\n/home/dev/other\n")
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(output), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("output = %q", output)
	}
	for _, line := range lines {
		_, rawURL, _ := strings.Cut(line, "\t")
		if rawURL == "git@github.com:victim/private.git" {
			t.Fatalf("injected record survived: %q", output)
		}
	}
	found := parseOriginProbeOutput([]string{"/home/dev/my repo", "/home/dev/other"}, string(output))
	if found["/home/dev/my repo"].origin == "github.com/victim/private" || found["/home/dev/other"].origin == "github.com/victim/private" {
		t.Fatalf("origins = %#v", found)
	}
}

func TestOriginProbeScriptBoundsBranchOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("validates a script executed by the remote POSIX shell")
	}
	bin := t.TempDir()
	fake := filepath.Join(bin, "git")
	payload := `#!/bin/sh
case "$*" in
  *"remote get-url"*) printf 'https://github.com/o/r\n' ;;
  *"symbolic-ref"*) head -c 10000 /dev/zero | tr '\000' x; printf '\n' ;;
esac
`
	if err := os.WriteFile(fake, []byte(payload), 0o700); err != nil {
		t.Fatal(err)
	}
	cwds := make([]string, maxOriginProbes)
	for i := range cwds {
		cwds[i] = "/home/dev/repo-" + strconv.Itoa(i)
	}
	cmd := exec.Command("sh", "-c", originProbeScript())
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	cmd.Stdin = strings.NewReader(strings.Join(cwds, "\n") + "\n")
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	found := parseOriginProbeOutput(cwds, string(output))
	if len(output) > maxOriginProbeBytes || len(found) != len(cwds) {
		t.Fatalf("output length=%d facts=%d", len(output), len(found))
	}
	for _, cwd := range cwds {
		if found[cwd].origin != "github.com/o/r" || found[cwd].branch != "" {
			t.Fatalf("facts[%q] = %#v", cwd, found[cwd])
		}
	}
}

func TestOriginProbeScriptBoundsAggregateOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("validates a script executed by the remote POSIX shell")
	}
	url := "https://github.com/o/" + strings.Repeat("r", 240-len("https://github.com/o/"))
	branch := strings.Repeat("x", 280)
	bin := t.TempDir()
	fake := filepath.Join(bin, "git")
	payload := "#!/bin/sh\ncase \"$*\" in\n  *\"remote get-url\"*) printf '%s\\n' '" + url + "' ;;\n  *\"symbolic-ref\"*) printf '%s\\n' '" + branch + "' ;;\nesac\n"
	if err := os.WriteFile(fake, []byte(payload), 0o700); err != nil {
		t.Fatal(err)
	}
	cwds := make([]string, maxOriginProbes)
	for i := range cwds {
		cwds[i] = "/home/dev/repo-" + strconv.Itoa(i)
	}
	cmd := exec.Command("sh", "-c", originProbeScript())
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	cmd.Stdin = strings.NewReader(strings.Join(cwds, "\n") + "\n")
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	wantBytes := 0
	for i := range cwds {
		wantBytes += len(strconv.Itoa(i)) + len(url) + 3
	}
	if len(output) != wantBytes || len(output) > maxOriginProbeBytes {
		t.Fatalf("output length=%d, want %d and at most %d", len(output), wantBytes, maxOriginProbeBytes)
	}
	found := parseOriginProbeOutput(cwds, string(output))
	wantOrigin := session.CanonicalOriginURL(url)
	if len(found) != len(cwds) {
		t.Fatalf("facts=%d, want %d", len(found), len(cwds))
	}
	for _, cwd := range cwds {
		facts := found[cwd]
		if facts.origin != wantOrigin || facts.branch != "" {
			t.Fatalf("facts[%q] = %#v", cwd, facts)
		}
	}
}

func TestOriginProbeScriptIncludesBranchWithinBudget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("validates a script executed by the remote POSIX shell")
	}
	bin := t.TempDir()
	fake := filepath.Join(bin, "git")
	payload := `#!/bin/sh
case "$*" in
  *"remote get-url"*) printf 'https://github.com/o/r\n' ;;
  *"symbolic-ref"*) printf 'codex/test\n' ;;
esac
`
	if err := os.WriteFile(fake, []byte(payload), 0o700); err != nil {
		t.Fatal(err)
	}
	cwd := "/home/dev/repo"
	cmd := exec.Command("sh", "-c", originProbeScript())
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	cmd.Stdin = strings.NewReader(cwd + "\n")
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := parseOriginProbeOutput([]string{cwd}, string(output))[cwd]; got != (remoteGit{"github.com/o/r", "codex/test"}) {
		t.Fatalf("facts = %#v", got)
	}
}

func TestProbeRemoteOriginsStopsAfterSSHFailure(t *testing.T) {
	calls := 0
	fake := fakeOptions(nil, 1, "", false)
	options := OpenOptions{command: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		calls++
		return fake.command(ctx, "ignored")
	}}
	cwds := make([]string, maxOriginProbes+1)
	for i := range cwds {
		cwds[i] = "/repo/" + strconv.Itoa(i)
	}
	found := sshTarget{"host", options}.gitFacts(context.Background(), cwds)
	if calls != 1 || len(found) != 0 {
		t.Fatalf("calls=%d origins=%#v", calls, found)
	}
}

func TestProbeRemoteOriginsStopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	options := OpenOptions{command: func(context.Context, string, ...string) *exec.Cmd {
		t.Fatal("probe ran after cancellation")
		return nil
	}}
	found := sshTarget{"host", options}.gitFacts(ctx, []string{"/home/dev/repos/coslash"})
	if len(found) != 0 {
		t.Fatalf("origins = %#v", found)
	}
}

func TestProbeRemoteOriginsCapsTotalDirectories(t *testing.T) {
	var output strings.Builder
	cwds := make([]string, maxOriginProbes+1)
	for i := range cwds {
		cwds[i] = "/repo/" + strconv.Itoa(i)
		output.WriteString(strconv.Itoa(i) + "\thttps://github.com/o/r" + strconv.Itoa(i) + "\n")
	}
	fake := fakeOptions([]byte(output.String()), 0, "", false)
	calls := 0
	options := OpenOptions{command: func(ctx context.Context, name string, args ...string) *exec.Cmd {
		calls++
		return fake.command(ctx, name, args...)
	}}
	found := sshTarget{"host", options}.gitFacts(context.Background(), cwds)
	if calls != 1 || len(found) != maxOriginProbes || found[cwds[maxOriginProbes]] != (remoteGit{}) {
		t.Fatalf("calls=%d origins=%d", calls, len(found))
	}
}

// The fake hangs after writing, so this returns only if the overlong read kills it.
func TestSSHTargetRunKillsOverlongOutput(t *testing.T) {
	options := fakeOptions([]byte(strings.Repeat("x", 256)), 0, "", true)
	if _, err := (sshTarget{"host", options}).run(context.Background(), "ignored", 128, ""); err == nil {
		t.Fatal("overlong output was accepted")
	}
}

func TestRepairRemoteDisplayFillsOrigin(t *testing.T) {
	family := validFamily(t, "root-1")
	family.Vendor = vendors.AgentCodex
	family.Sessions[0].Branch = "main"
	family.Sessions[0].Display.WorkingDirectory = "/home/dev/repos/coslash"
	snapshot := CachedSnapshotV2{
		Families: []CachedFamilyV2{{Vendor: vendors.AgentCodex, FamilyID: "root-1", Facts: family, Fingerprint: "same"}},
	}
	repairRemoteDisplay(context.Background(), &snapshot, "source-1", func(context.Context, []string) map[string]remoteGit {
		return map[string]remoteGit{"/home/dev/repos/coslash": {origin: "github.com/centauri-ai/coslash"}}
	})
	got := snapshot.Families[0].Facts.Sessions[0]
	if got.Display.Repository == nil || *got.Display.Repository != "github.com/centauri-ai/coslash" || got.Display.RepositoryLocalOnly {
		t.Fatalf("repository = %#v local=%v", got.Display.Repository, got.Display.RepositoryLocalOnly)
	}
	if snapshot.Families[0].Fingerprint != "same" {
		t.Fatalf("fingerprint changed to %q", snapshot.Families[0].Fingerprint)
	}
}

// Codex starts worktrees on a detached HEAD, so a session often records no
// branch; the probe still reads its remote and the branch checked out now.
func TestRepairFillsRepositoryAndBranchWithoutRecordedBranch(t *testing.T) {
	family := validFamily(t, "root-1")
	family.Sessions[0].Display.WorkingDirectory = "/home/dev/.codex/worktrees/0040/coslash"
	snapshot := CachedSnapshotV2{
		Families: []CachedFamilyV2{{Vendor: vendors.AgentCodex, FamilyID: "root-1", Facts: family}},
	}
	repairRemoteDisplay(context.Background(), &snapshot, "source-1", func(_ context.Context, cwds []string) map[string]remoteGit {
		if len(cwds) != 1 {
			t.Fatalf("cwds = %v", cwds)
		}
		return map[string]remoteGit{cwds[0]: {origin: "github.com/centauri-ai/coslash", branch: "codex/align-session-inspector-layout"}}
	})
	got := snapshot.Families[0].Facts.Sessions[0]
	if got.Display.Repository == nil || *got.Display.Repository != "github.com/centauri-ai/coslash" || got.Branch != "codex/align-session-inspector-layout" {
		t.Fatalf("repository = %v branch = %q", got.Display.Repository, got.Branch)
	}
}

func TestRepairRefreshesCachedBranch(t *testing.T) {
	family := validFamily(t, "root-1")
	family.Sessions[0].Branch = "codex/old-branch"
	family.Sessions[0].Display.WorkingDirectory = "/home/dev/repos/coslash"
	repository := "github.com/centauri-ai/coslash"
	family.Sessions[0].Display.Repository = &repository
	snapshot := CachedSnapshotV2{
		Families: []CachedFamilyV2{{Vendor: vendors.AgentCodex, FamilyID: "root-1", Facts: family}},
	}
	calls := 0
	repairRemoteDisplay(context.Background(), &snapshot, "source-1", func(_ context.Context, cwds []string) map[string]remoteGit {
		calls++
		if len(cwds) != 1 || cwds[0] != "/home/dev/repos/coslash" {
			t.Fatalf("cwds = %v", cwds)
		}
		return map[string]remoteGit{cwds[0]: {origin: repository, branch: "codex/new-branch"}}
	})
	if calls != 1 || snapshot.Families[0].Facts.Sessions[0].Branch != "codex/new-branch" {
		t.Fatalf("calls=%d branch=%q", calls, snapshot.Families[0].Facts.Sessions[0].Branch)
	}
}

func TestRepairUsesRememberedRepositoryForRemovedWorktree(t *testing.T) {
	session.UseRepositoryMemory(filepath.Join(t.TempDir(), "repositories.json"))
	t.Cleanup(func() { session.UseRepositoryMemory("") })
	snapshotFor := func() CachedSnapshotV2 {
		family := validFamily(t, "root-1")
		family.Sessions[0].Branch = "fix-sidebar"
		family.Sessions[0].Display.WorkingDirectory = "/home/dev/.superset/worktrees/coslash/cvu/fix-sidebar"
		return CachedSnapshotV2{Families: []CachedFamilyV2{{Vendor: vendors.AgentCodex, FamilyID: "root-1", Facts: family}}}
	}
	present := snapshotFor()
	repairRemoteDisplay(context.Background(), &present, "source-1", func(_ context.Context, cwds []string) map[string]remoteGit {
		return map[string]remoteGit{cwds[0]: {origin: "github.com/centauri-ai/coslash"}}
	})
	none := func(context.Context, []string) map[string]remoteGit { return nil }
	removed := snapshotFor()
	repairRemoteDisplay(context.Background(), &removed, "source-1", none)
	if got := removed.Families[0].Facts.Sessions[0].Display.Repository; got == nil || *got != "github.com/centauri-ai/coslash" {
		t.Fatalf("removed worktree repository = %v", got)
	}
	otherHost := snapshotFor()
	repairRemoteDisplay(context.Background(), &otherHost, "source-2", none)
	if got := otherHost.Families[0].Facts.Sessions[0].Display.Repository; got != nil {
		t.Fatalf("another source used this source's memory: %v", *got)
	}
}

func TestRepairDropsEditsWhenValidationFails(t *testing.T) {
	family := validFamily(t, "root-1")
	family.State = "not-a-state"
	family.Sessions[0].Branch = "main"
	family.Sessions[0].Display.WorkingDirectory = "/home/dev/repos/coslash"
	snapshot := CachedSnapshotV2{
		Families: []CachedFamilyV2{{Vendor: vendors.AgentCodex, FamilyID: "root-1", Facts: family}},
	}
	repairRemoteDisplay(context.Background(), &snapshot, "source-1", func(context.Context, []string) map[string]remoteGit {
		return map[string]remoteGit{"/home/dev/repos/coslash": {origin: "github.com/centauri-ai/coslash"}}
	})
	if snapshot.Families[0].Facts.Sessions[0].Display.Repository != nil {
		t.Fatal("invalid family kept the repaired repository")
	}
}
