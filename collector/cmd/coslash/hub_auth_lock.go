package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/settings"
)

const (
	hubAgentLockRetry          = 50 * time.Millisecond
	hubAgentTransactionTimeout = 60 * time.Second
	hubAgentRevisionWidth      = 20
)

// hubAgentLockWait bounds how long a command waits for another process's
// credential transaction. Tests shorten it.
var hubAgentLockWait = 10 * time.Second

var (
	errHubCredentialsBusy       = errors.New("Hub credentials are in use by another coslash process; try again")
	errHubConnectionChanged     = errors.New("the Hub connection changed while this command was running; run it again")
	errHubLoginSuperseded       = errors.New("a newer Hub login or logout replaced this login; its credentials were not saved")
	errHubLockMalformed         = errors.New("the Hub credential lock file hub-credentials/agent-access.lock is malformed; remove it when no coslash command is running ($COSLASH_HOME on Linux, ~/.coslash on macOS and Windows)")
	errHubCredentialLockMissing = errors.New("cannot resolve the home directory for the Hub credential lock")
)

// hubCredentialLockError marks a failure to take the Hub credential lock, as
// opposed to a failure of the credential store itself.
type hubCredentialLockError struct{ err error }

func (e *hubCredentialLockError) Error() string { return e.err.Error() }
func (e *hubCredentialLockError) Unwrap() error { return e.err }

// hubAgentLockPath returns the file that serializes Hub agent credential
// transactions. Linux keeps credentials per coSlash home. macOS and Windows
// share one OS keychain item across every coSlash home, so their lock lives
// under the user's home regardless of COSLASH_HOME.
func hubAgentLockPath(goos string) (string, error) {
	root := ""
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		root = filepath.Join(home, ".coslash")
	}
	if goos == "linux" && os.Getenv("COSLASH_HOME") != "" {
		root = settings.Home()
	}
	if root == "" {
		return "", errHubCredentialLockMissing
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", errHubCredentialLockMissing
	}
	return filepath.Join(root, "hub-credentials", "agent-access.lock"), nil
}

// lockHubAgentCredentials opens and exclusively locks path, waiting at most
// hubAgentLockWait or until ctx ends. The caller closes the file to unlock.
// The file is never renamed or removed: other processes may be waiting on it.
func lockHubAgentCredentials(ctx context.Context, path string) (*os.File, error) {
	file, err := openLockedHubAgentCredentials(ctx, path)
	if err != nil {
		return nil, &hubCredentialLockError{err: err}
	}
	return file, nil
}

func openLockedHubAgentCredentials(ctx context.Context, path string) (*os.File, error) {
	if path == "" {
		return nil, errHubCredentialLockMissing
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create Hub credential lock: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create Hub credential lock: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open Hub credential lock: %w", err)
	}
	wait, cancel := context.WithTimeout(ctx, hubAgentLockWait)
	defer cancel()
	for {
		err := lockRuntimeFileExclusive(file, true)
		if err == nil {
			return file, nil
		}
		if !runtimeLockContended(err) {
			file.Close()
			return nil, fmt.Errorf("lock Hub credentials: %w", err)
		}
		if err := sleepHubContext(wait, hubAgentLockRetry); err != nil {
			file.Close()
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			return nil, errHubCredentialsBusy
		}
	}
}

// readHubAgentRevision reads the login revision from a locked lock file. An
// empty file is revision zero.
func readHubAgentRevision(file *os.File) (uint64, error) {
	data, err := io.ReadAll(io.NewSectionReader(file, 0, hubAgentRevisionWidth+2))
	if err != nil {
		return 0, fmt.Errorf("read Hub credential lock: %w", err)
	}
	text := strings.TrimSuffix(string(data), "\n")
	if text == "" {
		return 0, nil
	}
	revision, err := strconv.ParseUint(text, 10, 64)
	if err != nil || len(text) != hubAgentRevisionWidth {
		return 0, errHubLockMalformed
	}
	return revision, nil
}

// advanceHubAgentRevision increments and durably stores the login revision in
// a locked lock file. Login start and logout advance it; refresh never does.
func advanceHubAgentRevision(file *os.File) (uint64, error) {
	revision, err := readHubAgentRevision(file)
	if err != nil {
		return 0, err
	}
	revision++
	if _, err := file.WriteAt([]byte(fmt.Sprintf("%0*d\n", hubAgentRevisionWidth, revision)), 0); err != nil {
		return 0, fmt.Errorf("write Hub credential lock: %w", err)
	}
	if err := file.Sync(); err != nil {
		return 0, fmt.Errorf("write Hub credential lock: %w", err)
	}
	return revision, nil
}

// withCredentialLock runs fn while holding the Hub credential lock. Network
// requests inside fn share one bounded deadline so a stalled Hub cannot hold
// the lock indefinitely. fn must not take the lock again.
func (c *hubAgentCLI) withCredentialLock(ctx context.Context, fn func(context.Context, *os.File) error) error {
	file, err := lockHubAgentCredentials(ctx, c.lockPath)
	if err != nil {
		return err
	}
	defer file.Close()
	defer unlockRuntimeFile(file)
	ctx, cancel := context.WithTimeout(ctx, hubAgentTransactionTimeout)
	defer cancel()
	return fn(ctx, file)
}

// revokeBestEffort revokes tokens from a login that will not be saved.
func (c *hubAgentCLI) revokeBestEffort(ctx context.Context, endpoints hubOAuthEndpoints, credential hubAgentCredential) {
	token := credential.RefreshToken
	if token == "" {
		token = credential.AccessToken
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), hubAgentRequestTimeout)
	defer cancel()
	_, _ = c.postForm(ctx, endpoints.RevocationURL, url.Values{"client_id": {credential.ClientID}, "token": {token}})
}
