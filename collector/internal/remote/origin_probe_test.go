package remote

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

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
	found := probeRemoteOrigins(context.Background(), "host", options, []string{"/tmp/ok\ntouch owned", "relative", ""})
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
	found := probeRemoteOrigins(context.Background(), "host", options, []string{"/home/dev/my repo"})
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
	found := probeRemoteOrigins(context.Background(), "host", options, cwds)
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
	found := probeRemoteOrigins(ctx, "host", options, []string{"/home/dev/repos/coslash"})
	if len(found) != 0 {
		t.Fatalf("origins = %#v", found)
	}
}

func TestRunSSHStdoutKillsOverlongOutput(t *testing.T) {
	options := OpenOptions{command: func(context.Context, string, ...string) *exec.Cmd {
		return exec.Command("sh", "-c", "head -c 100000 /dev/zero; sleep 30")
	}}
	started := time.Now()
	_, err := runSSHStdout(context.Background(), options, []string{"ignored"}, 128, nil)
	if err == nil || time.Since(started) > 5*time.Second {
		t.Fatalf("err=%v elapsed=%s", err, time.Since(started))
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
