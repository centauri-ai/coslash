package hubclient

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFakeSecurityProcess(t *testing.T) {
	root := os.Getenv("COSLASH_FAKE_KEYCHAIN")
	if root == "" {
		return
	}
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	args = args[1:]
	account := ""
	for i := 1; i+1 < len(args); i++ {
		if args[i] == "-a" {
			account = args[i+1]
		}
	}
	path := filepath.Join(root, fmt.Sprintf("%x", sha256.Sum256([]byte(account))))
	switch args[0] {
	case "find-generic-password":
		if os.Getenv("COSLASH_FAKE_KEYCHAIN_DENIED") == account {
			fmt.Fprintln(os.Stderr, "User interaction is not allowed")
			os.Exit(36)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "The specified item could not be found in the keychain")
			os.Exit(44)
		}
		fmt.Print(string(data))
	case "add-generic-password":
		data, _ := io.ReadAll(os.Stdin)
		if err := os.WriteFile(path, []byte(strings.SplitN(string(data), "\n", 2)[0]), 0600); err != nil {
			os.Exit(1)
		}
	case "delete-generic-password":
		if err := os.Remove(path); err != nil {
			fmt.Print("The specified item could not be found in the keychain")
			os.Exit(44)
		}
	default:
		os.Exit(2)
	}
	os.Exit(0)
}

func TestKeychainHomesAndLegacyLifecycle(t *testing.T) {
	t.Setenv("COSLASH_FAKE_KEYCHAIN", t.TempDir())
	previous := keychainCommandContext
	keychainCommandContext = func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, os.Args[0], append([]string{"-test.run=^TestFakeSecurityProcess$", "--"}, args...)...)
	}
	t.Cleanup(func() { keychainCommandContext = previous })
	ctx := context.Background()
	legacy := OSKeychain{Service: "test", Account: "hub"}
	release := OSKeychain{Service: "test", Account: "default:hub", LegacyAccount: "hub"}
	dev := OSKeychain{Service: "test", Account: "dev:hub"}
	if err := legacy.Save(ctx, "release-old"); err != nil {
		t.Fatal(err)
	}
	if got, err := release.Load(ctx); err != nil || got != "release-old" {
		t.Fatalf("legacy load = %q, %v", got, err)
	}
	if _, err := dev.Load(ctx); !errors.Is(err, ErrNotPaired) {
		t.Fatalf("dev read legacy: %v", err)
	}
	t.Setenv("COSLASH_FAKE_KEYCHAIN_DENIED", release.Account)
	if _, err := release.Load(ctx); !errors.Is(err, ErrCredentialStoreUnavailable) {
		t.Fatalf("access failure fell back to legacy: %v", err)
	}
	t.Setenv("COSLASH_FAKE_KEYCHAIN_DENIED", "")
	if err := dev.Save(ctx, "dev"); err != nil {
		t.Fatal(err)
	}
	if got, err := release.Load(ctx); err != nil || got != "release-old" {
		t.Fatalf("dev overwrote release: %q, %v", got, err)
	}
	if err := release.Save(ctx, "release-new"); err != nil {
		t.Fatal(err)
	}
	if got, err := release.Load(ctx); err != nil || got != "release-new" {
		t.Fatalf("scoped credential did not win: %q, %v", got, err)
	}
	if deleted, err := release.DeleteIfMatches(ctx, "release-old"); err != nil || deleted {
		t.Fatalf("stale deletion = %v, %v", deleted, err)
	}
	if deleted, err := release.DeleteIfMatches(ctx, "release-new"); err != nil || !deleted {
		t.Fatalf("deletion = %v, %v", deleted, err)
	}
	if _, err := release.Load(ctx); !errors.Is(err, ErrNotPaired) {
		t.Fatalf("legacy credential resurrected: %v", err)
	}
	if got, err := dev.Load(ctx); err != nil || got != "dev" {
		t.Fatalf("release deletion affected dev: %q, %v", got, err)
	}
}
