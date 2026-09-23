package remote

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestParseOriginProbeOutputCanonicalizesURL(t *testing.T) {
	cwds := []string{"/home/dev/repos/coslash", "/home/dev/repos/other"}
	output := "0\tgit@github.com:Centauri-AI/coSlash.git\n1\t\n"
	found := parseOriginProbeOutput(cwds, output)
	if found[cwds[0]] != "github.com/Centauri-AI/coSlash" || found[cwds[1]] != "" {
		t.Fatalf("origins = %#v", found)
	}
}

func TestProbeRemoteOriginsRejectsUnsafePaths(t *testing.T) {
	options := OpenOptions{command: func(context.Context, string, ...string) *exec.Cmd {
		t.Fatal("unsafe working directory reached SSH")
		return nil
	}}
	found := sshTarget{"host", options}.origins(context.Background(), []string{"/tmp/ok\ntouch owned", "relative", ""})
	if len(found) != 0 {
		t.Fatalf("origins = %#v", found)
	}
}

func TestParseOriginProbeOutputKeepsFirstLinePerIndex(t *testing.T) {
	cwds := []string{"/home/dev/repos/coslash", "/home/dev/repos/other"}
	output := "0\t\n0\tgit@github.com:victim/private.git\n1\thttps://github.com/centauri-ai/agent-tooling.git\n1\tgit@github.com:victim/private.git\n"
	found := parseOriginProbeOutput(cwds, output)
	if found[cwds[0]] != "" || found[cwds[1]] != "github.com/centauri-ai/agent-tooling" {
		t.Fatalf("origins = %#v", found)
	}
}

func TestProbeRemoteOriginsUsesOneRemoteCommand(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "stdin")
	var script string
	options := OpenOptions{command: func(_ context.Context, _ string, args ...string) *exec.Cmd {
		script = args[len(args)-1]
		return exec.Command("sh", "-c", "cat > \"$1\"; printf '0\\thttps://github.com/centauri-ai/agent-tooling.git\\n'", "sh", marker)
	}}
	found := sshTarget{"host", options}.origins(context.Background(), []string{"/home/dev/my repo"})
	if found["/home/dev/my repo"] != "github.com/centauri-ai/agent-tooling" {
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
	bin := t.TempDir()
	fake := filepath.Join(bin, "git")
	payload := "#!/bin/sh\nprintf '%s\\n' \"https://evil.example/repo\n1\tgit@github.com:victim/private.git\"\n"
	if err := os.WriteFile(fake, []byte(payload), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", originProbeScript)
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
	if found["/home/dev/my repo"] == "github.com/victim/private" || found["/home/dev/other"] == "github.com/victim/private" {
		t.Fatalf("origins = %#v", found)
	}
}

func TestProbeRemoteOriginsStopsAfterSSHFailure(t *testing.T) {
	calls := 0
	options := OpenOptions{command: func(context.Context, string, ...string) *exec.Cmd {
		calls++
		return exec.Command("sh", "-c", "exit 1")
	}}
	cwds := make([]string, maxOriginProbes+1)
	for i := range cwds {
		cwds[i] = "/repo/" + strconv.Itoa(i)
	}
	found := sshTarget{"host", options}.origins(context.Background(), cwds)
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
	found := sshTarget{"host", options}.origins(ctx, []string{"/home/dev/repos/coslash"})
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
	found := sshTarget{"host", options}.origins(context.Background(), cwds)
	if calls != 1 || len(found) != maxOriginProbes || found[cwds[maxOriginProbes]] != "" {
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
	repairRemoteDisplay(context.Background(), &snapshot, func(context.Context, []string) map[string]string {
		return map[string]string{"/home/dev/repos/coslash": "github.com/centauri-ai/coslash"}
	})
	got := snapshot.Families[0].Facts.Sessions[0]
	if got.Display.Repository == nil || *got.Display.Repository != "github.com/centauri-ai/coslash" || got.Display.RepositoryLocalOnly {
		t.Fatalf("repository = %#v local=%v", got.Display.Repository, got.Display.RepositoryLocalOnly)
	}
	if snapshot.Families[0].Fingerprint != "same" {
		t.Fatalf("fingerprint changed to %q", snapshot.Families[0].Fingerprint)
	}
}

func TestRepairSkipsOriginWithoutBranch(t *testing.T) {
	family := validFamily(t, "root-1")
	family.Sessions[0].Display.WorkingDirectory = "/home/dev/repos/coslash"
	snapshot := CachedSnapshotV2{
		Families: []CachedFamilyV2{{Vendor: vendors.AgentCodex, FamilyID: "root-1", Facts: family}},
	}
	repairRemoteDisplay(context.Background(), &snapshot, func(context.Context, []string) map[string]string {
		t.Fatal("lookup ran for a session with no branch")
		return nil
	})
	if snapshot.Families[0].Facts.Sessions[0].Display.Repository != nil {
		t.Fatal("repository was set without a branch")
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
	repairRemoteDisplay(context.Background(), &snapshot, func(context.Context, []string) map[string]string {
		return map[string]string{"/home/dev/repos/coslash": "github.com/centauri-ai/coslash"}
	})
	if snapshot.Families[0].Facts.Sessions[0].Display.Repository != nil {
		t.Fatal("invalid family kept the repaired repository")
	}
}
