package main

import (
	"context"
	"errors"
	"fmt"
	"log"
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

	mu            sync.Mutex
	active        map[string]context.CancelFunc
	connectJobs   map[string]connectJob
	hubClient     *hubclient.Client
	bindHubClient func(*hubclient.Client)
	syncHooks     syncHooks
	v4SyncActive  bool
	lastWake      time.Time
	wait          sync.WaitGroup
}

func newOnboardingManager(version string) *onboardingManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &onboardingManager{ctx: ctx, cancel: cancel, version: version, active: make(map[string]context.CancelFunc), connectJobs: make(map[string]connectJob)}
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
	m.setHubClient(client)
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
			m.ensureSync(client)
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

func (m *onboardingManager) StartCheckIns(client *hubclient.Client) {
	m.mu.Lock()
	hooks := m.syncHooks
	m.mu.Unlock()
	if hooks != nil {
		if err := hooks.Ensure(client); err != nil {
			log.Printf("start Hub sync: %v", err)
		}
	}
	m.startLegacyCheckIns(client)
}

func (m *onboardingManager) startLegacyCheckIns(client *hubclient.Client) {
	if client == nil || client.BaseURL == nil || client.Credentials == nil {
		return
	}
	credential, err := client.Credentials.Load(m.ctx)
	if err != nil || credential == "" {
		return
	}
	key := "check-in/" + client.BaseURL.Host
	m.mu.Lock()
	if m.v4SyncActive || m.ctx.Err() != nil {
		m.mu.Unlock()
		return
	}
	if _, exists := m.active[key]; exists {
		m.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.active[key] = cancel
	m.wait.Add(1)
	m.mu.Unlock()
	go func() {
		defer m.wait.Done()
		defer m.finish(key)
		m.runCheckIns(ctx, client)
	}()
}

func (m *onboardingManager) SetV4SyncActive(active bool) {
	m.mu.Lock()
	m.v4SyncActive = active
	m.mu.Unlock()
}

func (m *onboardingManager) setSyncHooks(hooks syncHooks) {
	m.mu.Lock()
	m.syncHooks = hooks
	m.mu.Unlock()
}

func (m *onboardingManager) ensureSync(client *hubclient.Client) {
	m.StartCheckIns(client)
}

func (m *onboardingManager) setHubClient(client *hubclient.Client) {
	m.mu.Lock()
	if m.bindHubClient != nil {
		m.bindHubClient(client)
	}
	m.hubClient = client
	m.mu.Unlock()
}

func (m *onboardingManager) setHubClientBinder(bind func(*hubclient.Client)) {
	m.mu.Lock()
	m.bindHubClient = bind
	if m.hubClient != nil {
		bind(m.hubClient)
	}
	m.mu.Unlock()
}

func (m *onboardingManager) currentHubClient() *hubclient.Client {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.hubClient
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
	m.setHubClient(client)
	m.mu.Lock()
	hooks := m.syncHooks
	v4SyncActive := m.v4SyncActive
	m.mu.Unlock()
	if hooks != nil {
		if err := hooks.Ensure(client); err != nil {
			return errors.New("Local could not check in with Hub")
		}
	}
	if v4SyncActive {
		return nil
	}
	if _, err := client.CheckIn(m.ctx, m.version); err != nil {
		return errors.New("Local could not check in with Hub")
	}
	m.startLegacyCheckIns(client)
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
	if err != nil || parsed.Path != "" || parsed.RawPath != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
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
	if err != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return "", errors.New("invalid Hub address")
	}
	validated, err := hubclient.ValidateHubURL(raw)
	if err != nil {
		return "", err
	}
	return validated.String(), nil
}
