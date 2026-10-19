//go:build darwin

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackgroundLoginPlistEscapesTheExecutable(t *testing.T) {
	contents, err := backgroundLoginPlist(`/Applications/coSlash & <Local>`)
	if err != nil {
		t.Fatal(err)
	}
	text := string(contents)
	if !strings.Contains(text, `/Applications/coSlash &amp; &lt;Local&gt;`) || !strings.Contains(text, "--background") {
		t.Fatalf("launch agent plist does not safely encode its program: %s", text)
	}
}

func TestBackgroundLoginDoesNotReplaceAnotherLocal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("COSLASH_HOME", filepath.Join(home, "dev"))
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	// The fake reports an already loaded job, so bootstrap is never attempted.
	if err := os.WriteFile(filepath.Join(bin, "launchctl"), []byte("#!/bin/sh\n[ \"$1\" = print ] || exit 99\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	path := filepath.Join(home, "Library", "LaunchAgents", "io.coslash.local.plist")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	original := []byte("release login item")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := registerBackgroundLogin(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("startup replaced release login item: %s", got)
	}
	var output bytes.Buffer
	if code := runCLI(&output, &output, []string{"service", "install"}); code == 0 {
		t.Fatal("custom home installed a shared service")
	}
	t.Setenv("COSLASH_HOME", filepath.Join(home, ".coslash"))
	if err := registerBackgroundLogin(); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(path)
	if err != nil || string(got) != string(original) {
		t.Fatal("default-home startup replaced release login item")
	}
	if code := runCLI(&output, &output, []string{"service", "install"}); code == 0 {
		t.Fatal("dev build installed a shared service")
	}
	originalRelease := releaseBuildMetadata
	releaseBuildMetadata = "true"
	t.Cleanup(func() { releaseBuildMetadata = originalRelease })
	if err := registerBackgroundLogin(); err == nil {
		t.Fatal("release replaced an unrecognized login item")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	useFakeProtocolHandlerBackend(t, &fakeProtocolHandlerBackend{})
	t.Setenv(protocolHandlerTestHomeOverride, "1")
	if code := runCLI(&output, &output, []string{"service", "install"}); code != 0 {
		t.Fatalf("explicit install failed: %s", &output)
	}
	got, err = os.ReadFile(path)
	if err != nil || !bytes.Contains(got, []byte("--background")) {
		t.Fatalf("explicit install did not write login item: %s, %v", got, err)
	}
}

func TestReleaseStartupInstallsLoginItem(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("COSLASH_HOME", filepath.Join(home, ".coslash"))
	originalRelease := releaseBuildMetadata
	releaseBuildMetadata = "true"
	t.Cleanup(func() { releaseBuildMetadata = originalRelease })
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "launchctl"), []byte("#!/bin/sh\n[ \"$1\" = print ] || exit 99\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	if err := registerBackgroundLogin(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "Library", "LaunchAgents", "io.coslash.local.plist")); err != nil {
		t.Fatalf("paired release did not install login item: %v", err)
	}
}

func TestLoginRegistrationReleaseAndBranchPaths(t *testing.T) {
	originalRelease, originalBranch, originalExecutable := releaseBuildMetadata, branchBuildMetadata, backgroundLoginExecutable
	t.Cleanup(func() {
		releaseBuildMetadata, branchBuildMetadata, backgroundLoginExecutable = originalRelease, originalBranch, originalExecutable
	})
	for _, test := range []struct {
		name, release, branch string
		brew, custom, want    bool
	}{
		{name: "script release", release: "true", want: true},
		{name: "Homebrew release", release: "true", brew: true, want: true},
		{name: "dev in script install path"},
		{name: "dev in Homebrew path", brew: true},
		{name: "branch installer", branch: "true"},
		{name: "branch with release flag", release: "true", branch: "true"},
		{name: "release with custom home", release: "true", custom: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := setProtocolHandlerTestHome(t)
			if test.custom {
				t.Setenv("COSLASH_HOME", filepath.Join(home, "dev"))
			}
			releaseBuildMetadata, branchBuildMetadata = test.release, test.branch
			executable := filepath.Join(home, ".local", "bin", "coslash")
			if test.brew {
				executable = filepath.Join(home, "brew", "Cellar", "coslash", "1.2.3", "bin", "coslash")
			}
			if err := os.MkdirAll(filepath.Dir(executable), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(executable, nil, 0700); err != nil {
				t.Fatal(err)
			}
			if test.brew {
				opt := filepath.Join(home, "brew", "opt")
				if err := os.MkdirAll(opt, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Dir(filepath.Dir(executable)), filepath.Join(opt, "coslash")); err != nil {
					t.Fatal(err)
				}
			}
			backgroundLoginExecutable = func() (string, error) { return executable, nil }
			fake := filepath.Join(home, "launchctl")
			if err := os.WriteFile(fake, []byte("#!/bin/sh\n[ \"$1\" = print ] || exit 99\nexit 0\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", home)
			agent := filepath.Join(home, "Library", "LaunchAgents", "io.coslash.local.plist")
			if err := registerBackgroundLogin(); err != nil {
				t.Fatal(err)
			}
			contents, err := os.ReadFile(agent)
			if !test.want {
				if !os.IsNotExist(err) {
					t.Fatalf("non-release/default-home startup wrote agent: %v", err)
				}
				if err := os.MkdirAll(filepath.Dir(agent), 0700); err != nil {
					t.Fatal(err)
				}
				original := []byte("another installation")
				if err := os.WriteFile(agent, original, 0600); err != nil {
					t.Fatal(err)
				}
				if err := registerBackgroundLogin(); err != nil {
					t.Fatal(err)
				}
				after, err := os.ReadFile(agent)
				if err != nil || !bytes.Equal(after, original) {
					t.Fatal("unsafe startup replaced the shared agent")
				}
				// Even an explicit install cannot let a dev or branch build claim the shared agent.
				if err := installService(); err == nil {
					t.Fatal("unsafe explicit install accepted")
				}
				return
			}
			if err != nil || !bytes.Contains(contents, []byte(backgroundLoginInstallPath(executable))) {
				t.Fatalf("release agent = %s, %v", contents, err)
			}
			original := append([]byte(nil), contents...)
			if err := registerBackgroundLogin(); err != nil {
				t.Fatal(err)
			}
			contents, _ = os.ReadFile(agent)
			if !bytes.Equal(original, contents) {
				t.Fatal("repeat startup changed the agent")
			}
			for _, conflict := range []string{"executable", "HOME", "COSLASH_HOME"} {
				contents, err = backgroundLoginPlist(executable)
				if err != nil {
					t.Fatal(err)
				}
				switch conflict {
				case "executable":
					contents, err = backgroundLoginPlist(filepath.Join(home, "other", "coslash"))
				default:
					contents = bytes.Replace(contents, []byte("</dict>"), []byte("<key>EnvironmentVariables</key><dict><key>"+conflict+"</key><string>/other/home</string></dict></dict>"), 1)
				}
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(agent, contents, 0600); err != nil {
					t.Fatal(err)
				}
				if err := registerBackgroundLogin(); err == nil {
					t.Fatalf("accepted another %s", conflict)
				}
				after, err := os.ReadFile(agent)
				if err != nil || !bytes.Equal(after, contents) {
					t.Fatalf("replaced another %s", conflict)
				}
			}
			if test.brew {
				oldExecutable := filepath.Join(home, "brew", "Cellar", "coslash", "1.2.2", "bin", "coslash")
				old, err := backgroundLoginPlist(oldExecutable)
				if err != nil {
					t.Fatal(err)
				}
				old = bytes.Replace(old, []byte("<key>RunAtLoad"), []byte("<key>StandardOutPath</key><string>/custom/log</string><key>RunAtLoad"), 1)
				if err := os.WriteFile(agent, old, 0600); err != nil {
					t.Fatal(err)
				}
				if err := registerBackgroundLogin(); err != nil {
					t.Fatal(err)
				}
				contents, err := os.ReadFile(agent)
				if err != nil || !bytes.Contains(contents, []byte(backgroundLoginInstallPath(executable))) || !bytes.Contains(contents, []byte("/custom/log")) {
					t.Fatalf("upgrade did not preserve configuration with stable executable: %s, %v", contents, err)
				}
			}
		})
	}
}
