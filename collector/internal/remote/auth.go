package remote

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"syscall"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/settings"
)

const AuthAttemptTTL = 5 * time.Minute

var authAttemptIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

var ErrAuthAttemptActive = errors.New("authentication attempt already active")

type AuthState string

const (
	AuthNotRequired AuthState = "not_required"
	AuthRequired    AuthState = "required"
	AuthLaunching   AuthState = "launching"
	AuthWaiting     AuthState = "waiting"
	AuthReady       AuthState = "ready"
	AuthTimedOut    AuthState = "timed_out"
	AuthCancelled   AuthState = "cancelled"
	AuthFailed      AuthState = "failed"
)

type authAttempt struct {
	ID          string    `json:"id"`
	Destination string    `json:"destination"`
	CreatedAt   time.Time `json:"createdAt"`
	State       AuthState `json:"state"`
}

func authAttemptPath(id string) (string, error) {
	if !authAttemptIDPattern.MatchString(id) {
		return "", errors.New("invalid authentication attempt")
	}
	return filepath.Join(settings.Home(), "ssh", "auth-"+id+".json"), nil
}

func createAuthAttempt(destination string) (string, error) {
	if _, err := parseDestination(destination); err != nil {
		return "", err
	}
	var id string
	err := withDestinationCoordinator(destination, func() error {
		entries, err := filepath.Glob(filepath.Join(settings.Home(), "ssh", "auth-*.json"))
		if err != nil {
			return err
		}
		for _, path := range entries {
			data, readErr := os.ReadFile(path)
			var existing authAttempt
			if readErr != nil || json.Unmarshal(data, &existing) != nil || time.Since(existing.CreatedAt) > AuthAttemptTTL {
				_ = os.Remove(path)
				continue
			}
			if existing.Destination == destination && existing.State == AuthWaiting {
				return ErrAuthAttemptActive
			}
		}
		raw := make([]byte, 16)
		if _, err := rand.Read(raw); err != nil {
			return fmt.Errorf("generate authentication attempt: %w", err)
		}
		id = hex.EncodeToString(raw)
		path, err := authAttemptPath(id)
		if err != nil {
			return err
		}
		data, err := json.Marshal(authAttempt{ID: id, Destination: destination, CreatedAt: time.Now().UTC(), State: AuthWaiting})
		if err != nil {
			return err
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return fmt.Errorf("create authentication attempt: %w", err)
		}
		if _, err := file.Write(data); err != nil {
			_ = file.Close()
			_ = os.Remove(path)
			return fmt.Errorf("write authentication attempt: %w", err)
		}
		if err := file.Close(); err != nil {
			_ = os.Remove(path)
			return fmt.Errorf("close authentication attempt: %w", err)
		}
		return nil
	})
	return id, err
}

// withDestinationCoordinator serializes an interactive attempt with every
// control-master check/start. The single lock is stronger than a per-
// destination lock, and avoids leaving a new lock file for every attempted
// destination. flock is released by the kernel if a process exits.
func withDestinationCoordinator(_ string, callback func() error) error {
	if err := ensureSSHControlDir(); err != nil {
		return err
	}
	lockPath := filepath.Join(settings.Home(), "ssh", ".auth-coordinator.lock")
	lock, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock SSH destination: %w", err)
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()
	return callback()
}

var runInteractiveSSH = func(ctx context.Context, args []string) error {
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

var exitAuthControlMaster = exitControlMasterBestEffort

func loadAuthAttempt(id string) (authAttempt, error) {
	path, err := authAttemptPath(id)
	if err != nil {
		return authAttempt{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return authAttempt{}, err
	}
	var attempt authAttempt
	if err := json.Unmarshal(data, &attempt); err != nil || attempt.ID != id {
		return authAttempt{}, errors.New("invalid authentication attempt")
	}
	if _, err := parseDestination(attempt.Destination); err != nil {
		return authAttempt{}, errors.New("invalid authentication destination")
	}
	return attempt, nil
}

// CreateAuthAttempt records only the already-validated destination in a
// mode-0600, short-lived local file. The terminal receives only its opaque ID.
func CreateAuthAttempt(destination string) (string, error) { return createAuthAttempt(destination) }

// AuthAttemptActive is used by background work to avoid racing an explicit
// terminal prompt. Corrupt and expired records are not considered active.
func AuthAttemptActive(destination string) bool {
	entries, err := filepath.Glob(filepath.Join(settings.Home(), "ssh", "auth-*.json"))
	if err != nil {
		return false
	}
	for _, path := range entries {
		data, err := os.ReadFile(path)
		var attempt authAttempt
		if err == nil && json.Unmarshal(data, &attempt) == nil && attempt.Destination == destination && attempt.State == AuthWaiting && time.Since(attempt.CreatedAt) <= AuthAttemptTTL {
			return true
		}
	}
	return false
}

func CancelAuthAttempt(id string) error {
	attempt, err := loadAuthAttempt(id)
	if err != nil {
		return err
	}
	_, err = updateAuthAttemptIfWaiting(id, AuthCancelled)
	if err == nil {
		exitAuthControlMaster(attempt.Destination)
	}
	return err
}

// CancelAuthAttemptsForDestination prevents a removed host from gaining a
// late interactive master. The terminal command re-checks its record under
// this same destination coordinator before treating SSH success as ready.
func CancelAuthAttemptsForDestination(destination string) {
	if destination == "" {
		return
	}
	removed := false
	_ = withDestinationCoordinator(destination, func() error {
		entries, err := filepath.Glob(filepath.Join(settings.Home(), "ssh", "auth-*.json"))
		if err != nil {
			return err
		}
		for _, path := range entries {
			data, err := os.ReadFile(path)
			var attempt authAttempt
			if err != nil || json.Unmarshal(data, &attempt) != nil || attempt.Destination != destination {
				continue
			}
			if _, err := authAttemptPath(attempt.ID); err != nil {
				continue
			}
			attempt.State = AuthCancelled
			if err := writeAuthAttempt(attempt); err != nil {
				return err
			}
			removed = true
		}
		return nil
	})
	if removed {
		exitAuthControlMaster(destination)
	}
}

// updateAuthAttemptIfWaiting preserves a terminal result chosen by another
// process (for example, Cancel in the app racing a terminal SSH failure).
func updateAuthAttemptIfWaiting(id string, state AuthState) (AuthState, error) {
	initial, err := loadAuthAttempt(id)
	if err != nil {
		return "", err
	}
	var result AuthState
	err = withDestinationCoordinator(initial.Destination, func() error {
		attempt, err := loadAuthAttempt(id)
		if err != nil {
			return err
		}
		if attempt.State != AuthWaiting {
			result = attempt.State
			return nil
		}
		attempt.State = state
		if err := writeAuthAttempt(attempt); err != nil {
			return err
		}
		result = state
		return nil
	})
	return result, err
}

func writeAuthAttempt(attempt authAttempt) error {
	path, err := authAttemptPath(attempt.ID)
	if err != nil {
		return err
	}
	data, err := json.Marshal(attempt)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".auth-*")
	if err != nil {
		return err
	}
	if err := temporary.Chmod(0o600); err == nil {
		_, err = temporary.Write(data)
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(temporary.Name())
		return err
	}
	return os.Rename(temporary.Name(), path)
}

func interactiveMasterArgs(destination string) ([]string, error) {
	parsed, err := parseDestination(destination)
	if err != nil {
		return nil, err
	}
	args := []string{
		"-f", "-N",
		"-o", "BatchMode=no",
		"-o", "ConnectTimeout=" + fmt.Sprint(int(DefaultConnectTimeout.Seconds())),
		"-o", "ControlMaster=yes",
		"-o", "ControlPath=" + controlSocketPath(),
		"-o", "ControlPersist=" + defaultControlPersist,
	}
	return append(args, parsed.Args()...), nil
}

// RunAuthAttempt is the ssh-auth subcommand entry point. Its stdin and output
// belong to the terminal, never to the collector HTTP process.
func RunAuthAttempt(ctx context.Context, id string) error {
	attempt, err := loadAuthAttempt(id)
	if err != nil {
		return err
	}
	if time.Since(attempt.CreatedAt) > AuthAttemptTTL {
		_, _ = updateAuthAttemptIfWaiting(id, AuthTimedOut)
		return errors.New("authentication attempt expired")
	}
	deadline := attempt.CreatedAt.Add(AuthAttemptTTL)
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	args, err := interactiveMasterArgs(attempt.Destination)
	if err != nil {
		return err
	}
	if err := runInteractiveSSH(ctx, args); err != nil {
		state := AuthFailed
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			state = AuthTimedOut
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			state = AuthCancelled
		}
		_, _ = updateAuthAttemptIfWaiting(id, state)
		return fmt.Errorf("authenticate SSH: %w", err)
	}
	closeMaster := false
	err = withDestinationCoordinator(attempt.Destination, func() error {
		latest, err := loadAuthAttempt(id)
		if errors.Is(err, os.ErrNotExist) {
			closeMaster = true
			return nil
		}
		if err != nil {
			return err
		}
		if latest.State == AuthReady {
			// A status poll may see the new socket before this terminal process
			// reacquires the coordinator. That is a successful attempt, not a
			// cancellation, so the shared master must remain available.
			return nil
		}
		if latest.State != AuthWaiting {
			closeMaster = true
			return nil
		}
		latest.State = AuthReady
		return writeAuthAttempt(latest)
	})
	if err != nil {
		return fmt.Errorf("record SSH authentication: %w", err)
	}
	if closeMaster {
		exitAuthControlMaster(attempt.Destination)
		return errors.New("authentication attempt was cancelled")
	}
	return nil
}

func AuthAttemptState(ctx context.Context, id string) (AuthState, error) {
	attempt, err := loadAuthAttempt(id)
	if errors.Is(err, os.ErrNotExist) {
		return AuthCancelled, nil
	}
	if err != nil {
		return "", err
	}
	if attempt.State != AuthWaiting {
		return attempt.State, nil
	}
	if time.Since(attempt.CreatedAt) > AuthAttemptTTL {
		state, updateErr := updateAuthAttemptIfWaiting(id, AuthTimedOut)
		if updateErr != nil {
			return "", updateErr
		}
		return state, nil
	}
	args, err := controlCheckArgs(attempt.Destination)
	if err != nil {
		return "", err
	}
	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := runSSHCommand(checkCtx, OpenOptions{}, args); err == nil {
		state, updateErr := updateAuthAttemptIfWaiting(id, AuthReady)
		if updateErr != nil {
			return "", updateErr
		}
		return state, nil
	}
	return AuthWaiting, nil
}
