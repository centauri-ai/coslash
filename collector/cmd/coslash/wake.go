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
	if handled, err := m.routeHubSignal(origin, true); handled {
		return err
	}
	stored, err := readStoredHubURL()
	if err != nil {
		return err
	}
	if stored == "" || stored != origin {
		from, bound, err := m.boundElsewhere(origin)
		if err != nil {
			return err
		}
		if bound {
			canSwitch, err := m.hasCredential(origin)
			if err != nil {
				return err
			}
			m.askSwitch(hubSwitch{From: from, To: origin, CanSwitch: canSwitch, action: "wake"})
		}
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

func (m *onboardingManager) routeHubSignal(raw string, rateLimit bool) (bool, error) {
	m.mu.Lock()
	hooks := m.syncHooks
	m.mu.Unlock()
	router, ok := hooks.(interface {
		hubSignalTarget(string, bool) (bool, bool, string)
		signalHub(string) bool
	})
	if !ok {
		return false, nil
	}
	handled, enabled, origin := router.hubSignalTarget(raw, rateLimit)
	if !handled {
		return false, nil
	}
	if !enabled {
		return true, errForeignWakeHub
	}
	if rateLimit {
		now := time.Now()
		m.mu.Lock()
		if m.lastHubWake == nil {
			m.lastHubWake = make(map[string]time.Time)
		}
		if previous := m.lastHubWake[origin]; !previous.IsZero() && now.Sub(previous) < 10*time.Second {
			m.mu.Unlock()
			return true, errWakeRateLimited
		}
		m.lastHubWake[origin] = now
		m.mu.Unlock()
	}
	if !router.signalHub(origin) {
		return true, errors.New("secondary Hub sync is unavailable")
	}
	return true, nil
}
