package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/syncv4"
)

func TestAutomaticUpdateSkipsTheRunningRelease(t *testing.T) {
	prompt := syncv4.UpdatePrompt{Available: true, Version: "1.2.3"}
	if automaticUpdateNeeded(true, prompt, "1.2.3") {
		t.Fatal("already installed release should not be prepared again")
	}
	if !automaticUpdateNeeded(true, prompt, "1.2.2") {
		t.Fatal("newer release should be prepared")
	}
	if automaticUpdateNeeded(false, prompt, "1.2.2") || automaticUpdateNeeded(true, syncv4.UpdatePrompt{Version: prompt.Version}, "1.2.2") {
		t.Fatal("disabled or unavailable update should not be prepared")
	}
}

func TestAutomaticUpdatesSupported(t *testing.T) {
	for _, test := range []struct {
		name        string
		goos        string
		channel     string
		branchBuild bool
		want        bool
	}{
		{name: "release macOS script", goos: "darwin", channel: "script", want: true},
		{name: "release Windows script", goos: "windows", channel: "windows-script", want: true},
		{name: "branch build reported as script", goos: "darwin", channel: "script", branchBuild: true},
		{name: "Homebrew", goos: "darwin", channel: "brew"},
		{name: "unsupported operating system", goos: "linux", channel: "script"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := automaticUpdatesSupported(test.goos, test.channel, test.branchBuild); got != test.want {
				t.Fatalf("automatic updates supported=%t, want %t", got, test.want)
			}
		})
	}
}

func TestUpdateReplacementKeepsRunnableBackup(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "coslash")
	staged := filepath.Join(directory, ".coslash-update-stage")
	backup := target + ".previous"
	if err := os.WriteFile(target, []byte("old release"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staged, []byte("new release"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := replaceUpdateTarget(target, staged, backup); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "new release" {
		t.Fatalf("installed target = %q, %v", got, err)
	}
	if got, err := os.ReadFile(backup); err != nil || string(got) != "old release" {
		t.Fatalf("backup target = %q, %v", got, err)
	}
	if err := restoreUpdateTarget(target, backup); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "old release" {
		t.Fatalf("restored target = %q, %v", got, err)
	}
}

func TestWaitForRuntimeLockRespectsUpdateHandoff(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	owner, err := acquireRuntimeLock()
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	update, err := acquireUpdateLock()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	result := make(chan *os.File, 1)
	errResult := make(chan error, 1)
	go func() {
		file, err := waitForRuntimeLock(ctx)
		if err != nil {
			errResult <- err
			return
		}
		result <- file
	}()

	assertWaiting := func() {
		t.Helper()
		select {
		case file := <-result:
			_ = file.Close()
			t.Fatal("waiter acquired the runtime lock during handoff")
		case err := <-errResult:
			t.Fatalf("waiter returned early: %v", err)
		case <-time.After(350 * time.Millisecond):
		}
	}
	assertWaiting()
	if err := update.Close(); err != nil {
		t.Fatal(err)
	}
	assertWaiting()
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case file := <-result:
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	case err := <-errResult:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal("waiter did not acquire the runtime lock after the handoff")
	}
}

func TestUpdateHelperCleanup(t *testing.T) {
	helper := filepath.Join(t.TempDir(), "coslash-update-helper-test")
	if err := os.WriteFile(helper, []byte("temporary executable"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := scheduleUpdateHelperCleanup(helper); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(helper); errors.Is(err, os.ErrNotExist) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("temporary update helper was not removed")
}

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

func TestGracefulShutdownWaitsForInFlightHandlers(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		_, _ = w.Write([]byte("done"))
	}))
	server.Start()
	defer server.Close()
	requestDone := make(chan error, 1)
	go func() {
		response, err := http.Get(server.URL)
		if err == nil {
			_ = response.Body.Close()
		}
		requestDone <- err
	}()
	<-started
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- gracefulShutdown(server.Config, 10*time.Millisecond) }()
	select {
	case err := <-shutdownDone:
		t.Fatalf("shutdown returned before the active handler finished: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not finish after the handler drained")
	}
	if err := <-requestDone; err != nil {
		t.Fatal(err)
	}
}
