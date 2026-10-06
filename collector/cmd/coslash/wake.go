package main

import (
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
)

var (
	errForeignWakeHub  = errors.New("wake Hub does not match the saved Hub")
	errWakeRateLimited = errors.New("wake request rate limited")
)

type wakeIntent struct {
	HubURL *url.URL
}

func parseWakeIntentURL(raw string) (wakeIntent, error) {
	if len(raw) == 0 || len(raw) > 512 {
		return wakeIntent{}, errors.New("invalid coSlash wake URL")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "coslash" || parsed.Host != "wake" || parsed.Opaque != "" ||
		parsed.User != nil || parsed.Path != "" || parsed.RawPath != "" || parsed.Fragment != "" {
		return wakeIntent{}, errors.New("invalid coSlash wake URL")
	}
	values, err := url.ParseQuery(parsed.RawQuery)
	if err != nil || len(values) != 1 || len(values["hub"]) != 1 || strings.TrimSpace(values.Get("hub")) == "" {
		return wakeIntent{}, errors.New("invalid coSlash wake URL")
	}
	hub, err := hubclient.ValidateHubURL(values.Get("hub"))
	if err != nil || hub.Scheme != "https" || hub.Path != "" || hub.RawPath != "" || hub.RawQuery != "" || hub.ForceQuery || hub.Fragment != "" {
		return wakeIntent{}, errors.New("invalid coSlash wake Hub")
	}
	return wakeIntent{HubURL: hub}, nil
}

func (m *onboardingManager) RequestWake(rawHubURL string) error {
	origin, err := hubOrigin(rawHubURL)
	if err != nil {
		return errForeignWakeHub
	}
	stored, err := readStoredHubURL()
	if err != nil || stored == "" || stored != origin {
		return errForeignWakeHub
	}
	now := time.Now()
	m.mu.Lock()
	if !m.lastWake.IsZero() && now.Sub(m.lastWake) < 10*time.Second {
		m.mu.Unlock()
		return errWakeRateLimited
	}
	m.lastWake = now
	hooks := m.syncHooks
	m.mu.Unlock()
	if hooks == nil {
		return errors.New("Local sync is unavailable")
	}
	hooks.RequestPass("wake")
	return nil
}
