package launch

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestGrokReviewExecutableUsesNativePayload(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	for _, payload := range []string{"grok", "grok-native", filepath.Join("home", "bin", "grok"), "missing"} {
		t.Run(payload, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("GROK_HOME", filepath.Join(root, "home"))
			launcher := filepath.Join(root, "grok")
			if payload != "grok" {
				if err := os.WriteFile(launcher, []byte("#!/usr/bin/env node\nrequire('./bootstrap.js');\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if payload != "missing" {
				native := filepath.Join(root, payload)
				if err := os.MkdirAll(filepath.Dir(native), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(executable, native); err != nil {
					t.Fatal(err)
				}
			}
			got, err := grokReviewExecutable(launcher)
			if payload == "missing" {
				if err == nil {
					t.Fatal("script without native payload did not fail closed")
				}
			} else if err != nil || got != executable {
				t.Fatalf("executable=%q err=%v", got, err)
			}
		})
	}
}

func TestGrokReviewSandboxBoundaries(t *testing.T) {
	if os.Getenv("COSLASH_TEST_SANDBOX") == "1" {
		check := func(ok bool, message string) {
			if !ok {
				fmt.Fprintln(os.Stderr, message)
				os.Exit(1)
			}
		}
		work, scratch, outside := os.Getenv("TEST_WORK"), os.Getenv("TEST_SCRATCH"), os.Getenv("TEST_OUTSIDE")
		_, err := os.ReadFile(filepath.Join(work, "allowed.txt"))
		check(err == nil, "worktree read denied")
		_, err = os.ReadFile(outside)
		check(err != nil, "outside read allowed")
		if alias := os.Getenv("TEST_DATA_ALIAS"); alias != "" {
			_, err = os.ReadFile(alias)
			check(err != nil, "data volume alias read allowed")
		}
		_, err = os.ReadFile(filepath.Join(work, "escape"))
		check(err != nil, "symlink escape allowed")
		err = os.WriteFile(filepath.Join(work, "write.txt"), []byte("bad"), 0600)
		check(err != nil, "worktree write allowed")
		err = os.WriteFile(filepath.Join(scratch, "write.txt"), []byte("ok"), 0600)
		check(err == nil, "scratch write denied")
		_, err = net.Dial("unix", os.Getenv("TEST_SOCKET_ALIAS"))
		check(err != nil, "socket alias reachable")
		_, err = net.Dial("unix", os.Getenv("TEST_SOCKET"))
		check(err != nil, "socket target reachable")
		fmt.Println("SANDBOX_BOUNDARIES_OK")
		os.Exit(0)
	}
	if runtime.GOOS != "darwin" {
		t.Skip("macOS kernel sandbox")
	}
	root, err := os.MkdirTemp("/tmp", "gr-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	work := filepath.Join(root, "work ' with spaces")
	scratch := filepath.Join(root, "scratch")
	outside := filepath.Join(root, "private.txt")
	for _, dir := range []string{work, scratch} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{filepath.Join(work, "allowed.txt"), outside} {
		if err := os.WriteFile(file, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(work, "escape")); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(scratch, "docker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	alias := filepath.Join(work, "docker.sock")
	if err := os.Symlink(socket, alias); err != nil {
		t.Fatal(err)
	}
	connection, err := net.Dial("unix", alias)
	if err != nil {
		t.Fatal(err)
	}
	connection.Close()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	spec, err := sandboxGrokReview(reviewCommandSpec{bin: executable, args: []string{"-test.run=^TestGrokReviewSandboxBoundaries$"}}, work, scratch)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(spec.bin, spec.args...)
	command.Dir = spec.dir
	canonicalOutside, err := filepath.EvalSymlinks(outside)
	if err != nil {
		t.Fatal(err)
	}
	dataAlias := "/System/Volumes/Data" + canonicalOutside
	if _, err := os.Stat(dataAlias); err != nil {
		dataAlias = ""
	}
	command.Env = append(os.Environ(), "TEST_DATA_ALIAS="+dataAlias, "COSLASH_TEST_SANDBOX=1", "TEST_WORK="+work, "TEST_SCRATCH="+scratch, "TEST_OUTSIDE="+outside, "TEST_SOCKET="+socket, "TEST_SOCKET_ALIAS="+alias)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("sandbox: %v: %s", err, output)
	}
	if string(output) != "SANDBOX_BOUNDARIES_OK\n" {
		t.Fatalf("output=%q", output)
	}
	if err := os.Remove(spec.args[1]); err != nil {
		t.Fatal(err)
	}
	refused := exec.Command(spec.bin, spec.args...)
	if output, err := refused.CombinedOutput(); err == nil {
		t.Fatalf("missing policy did not fail closed: %s", output)
	}
}
