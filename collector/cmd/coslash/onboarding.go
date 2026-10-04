package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/settings"
)

const storedHubURLFilename = "hub-url"

type onboardingManager struct {
	ctx     context.Context
	cancel  context.CancelFunc
	version string

	mu     sync.Mutex
	active map[string]context.CancelFunc
	wait   sync.WaitGroup
}

func newOnboardingManager(version string) *onboardingManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &onboardingManager{ctx: ctx, cancel: cancel, version: version, active: make(map[string]context.CancelFunc)}
}

func (m *onboardingManager) StartPairing(rawHubURL, attemptID, launchIntent string) error {
	origin, err := hubOrigin(rawHubURL)
	if err != nil || launchIntent == "" {
		return errors.New("invalid Hub activation")
	}
	hubURL, _ := url.Parse(origin)
	client, err := hubClientForURL(m.version, hubURL.String())
	if err != nil {
		return errors.New("Hub activation is unavailable")
	}
	if err := writeStoredHubURL(hubURL.String()); err != nil {
		return errors.New("Hub address could not be saved")
	}
	key := hubURL.Host + "/" + attemptID
	m.mu.Lock()
	if _, exists := m.active[key]; exists {
		m.mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.active[key] = cancel
	m.wait.Add(1)
	m.mu.Unlock()
	go func() {
		defer m.wait.Done()
		defer m.finish(key)
		m.runPairing(ctx, client, attemptID, launchIntent)
	}()
	return nil
}

func (m *onboardingManager) runPairing(ctx context.Context, client *hubclient.Client, attemptID, launchIntent string) {
	pairing, err := client.BeginOnboardingPairing(ctx, attemptID, launchIntent)
	if err != nil {
		return
	}
	interval := time.Duration(pairing.IntervalSeconds) * time.Second
	if interval < time.Second || interval > 30*time.Second {
		interval = 2 * time.Second
	}
	for time.Now().Before(pairing.ExpiresAt) {
		result, err := client.PollPairing(ctx, pairing.PairingID)
		if err == nil && result.State == "paired" {
			m.runCheckIns(ctx, client)
			return
		}
		if err == nil && result.State == "expired" {
			return
		}
		if !sleepContext(ctx, interval) {
			return
		}
	}
}

func (m *onboardingManager) runCheckIns(ctx context.Context, client *hubclient.Client) {
	delay := 5 * time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		next, err := client.CheckIn(ctx, m.version)
		if err != nil {
			if !sleepContext(ctx, delay) {
				return
			}
			continue
		}
		delay = next
		if delay < 30*time.Second {
			delay = 30 * time.Second
		}
		if delay > 5*time.Minute {
			delay = 5 * time.Minute
		}
		if !sleepContext(ctx, delay) {
			return
		}
	}
}

func (m *onboardingManager) RetryCheckIn(rawHubURL string) error {
	origin, err := hubOrigin(rawHubURL)
	if err != nil {
		return errors.New("invalid Hub address")
	}
	hubURL, _ := url.Parse(origin)
	client, err := hubClientForURL(m.version, hubURL.String())
	if err != nil {
		return errors.New("Hub address is unavailable")
	}
	if err := writeStoredHubURL(hubURL.String()); err != nil {
		return errors.New("Hub address could not be saved")
	}
	if _, err := client.CheckIn(m.ctx, m.version); err != nil {
		return errors.New("Local could not check in with Hub")
	}
	return nil
}

func (m *onboardingManager) finish(key string) {
	m.mu.Lock()
	delete(m.active, key)
	m.mu.Unlock()
}

func (m *onboardingManager) Close() {
	m.cancel()
	m.wait.Wait()
}

func sleepContext(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func readStoredHubURL() (string, error) {
	data, err := os.ReadFile(filepath.Join(settings.Home(), storedHubURLFilename))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", errors.New("Hub address could not be read")
	}
	raw := strings.TrimSpace(string(data))
	if raw == "" {
		return "", nil
	}
	parsed, err := hubclient.ValidateHubURL(raw)
	if err != nil {
		return "", fmt.Errorf("invalid saved Hub address: %w", err)
	}
	return parsed.String(), nil
}

func writeStoredHubURL(raw string) error {
	parsed, err := hubclient.ValidateHubURL(raw)
	if err != nil || parsed.Path != "" || parsed.RawPath != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("invalid Hub address")
	}
	home := settings.Home()
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(home, ".hub-url-*")
	if err != nil {
		return err
	}
	path := temporary.Name()
	defer os.Remove(path)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.WriteString(parsed.String() + "\n"); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(path, filepath.Join(home, storedHubURLFilename)); err != nil {
		return err
	}
	return nil
}

func hubOrigin(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("invalid Hub address")
	}
	validated, err := hubclient.ValidateHubURL(raw)
	if err != nil {
		return "", err
	}
	return validated.String(), nil
}
