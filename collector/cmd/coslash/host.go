package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/collector"
	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/sessionbackupproducer"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/syncv4"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func runHostCommand(ctx context.Context, args []string, output io.Writer) error {
	if len(args) != 1 || (args[0] != "pair" && args[0] != "once" && args[0] != "run") {
		return errors.New("use coslash host pair, once or run")
	}
	if runtime.GOOS != "linux" || os.Geteuid() == 0 {
		return errors.New("host mode requires a non-root Linux service user")
	}
	hub, err := hubClientFromEnvironment(version)
	if err != nil || hub == nil {
		return errors.New("configure a valid COSLASH_HUB_URL")
	}
	hub.Credentials = hubclient.EncryptedHostCredentials{
		Directory: filepath.Join(settings.Home(), "host-credentials"), HubURL: hub.BaseURL.String(),
	}
	lock, err := acquireHostLock()
	if err != nil {
		return err
	}
	defer lock.Close()
	if args[0] == "pair" {
		return pairHost(ctx, hub, output)
	}
	if os.Getenv("COSLASH_V4_SYNC_ENABLED") != "1" {
		return errors.New("v4 host sync is disabled")
	}
	if _, err := hub.Credentials.Load(ctx); err != nil {
		return err
	}
	queue, err := syncv4.Open("")
	if err != nil {
		return err
	}
	hub.Backup = sessionbackupproducer.New(sessionbackupproducer.Options{CollectorVersion: version})
	hub.V4Capabilities = []string{"sync-v4", "session-backup/v1"}
	initialSessions, initialErr := collector.List(ctx, 0)
	initialPending := true
	foundAgents := supportedHostAgents(initialSessions)
	if initialErr != nil {
		foundAgents = nil
	}
	hub.V4AgentsFound = func() []string { return append([]string{}, foundAgents...) }
	runner := &syncv4.Runner{
		Queue: queue, Backup: hub.Backup, Hub: hub,
		Discover: func(ctx context.Context) ([]*session.Session, error) {
			if initialPending {
				initialPending = false
				return initialSessions, initialErr
			}
			sessions, err := collector.List(ctx, 0)
			if err != nil {
				foundAgents = nil
			} else {
				foundAgents = supportedHostAgents(sessions)
			}
			return sessions, err
		},
		Conditions: syncv4.LocalConditions,
		LocalPause: func() bool {
			state := settings.Open().State()
			return !state.Valid || state.Config.SyncPaused
		},
	}
	if args[0] == "once" {
		return runner.SyncOnce(ctx)
	}
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		if err := runner.SyncOnce(ctx); err != nil && ctx.Err() == nil {
			fmt.Fprintln(output, "host sync deferred; retaining last accepted revision")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func supportedHostAgents(sessions []*session.Session) []string {
	for _, item := range sessions {
		if item != nil && item.Agent == vendors.AgentCodex {
			return []string{vendors.AgentCodex}
		}
	}
	return []string{}
}

func acquireHostLock() (*os.File, error) {
	return acquireRuntimeLock()
}

func pairHost(ctx context.Context, hub *hubclient.Client, output io.Writer) error {
	request, err := hub.BeginPairing(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(output, "Open %s and enter code %s\n", request.VerificationURI, request.UserCode)
	interval := time.Duration(request.IntervalSeconds) * time.Second
	if interval < time.Second {
		interval = time.Second
	}
	if interval > 30*time.Second {
		interval = 30 * time.Second
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
		result, err := hub.PollPairing(ctx, request.PairingID)
		if err != nil {
			return err
		}
		switch result.State {
		case "paired":
			fmt.Fprintln(output, "Host paired. Start coslash host run to sync directly to Hub.")
			return nil
		case "pending":
			continue
		case "expired":
			return errors.New("host pairing expired")
		default:
			return errors.New("unexpected host pairing state")
		}
	}
}
