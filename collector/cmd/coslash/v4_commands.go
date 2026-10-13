package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/centauri-ai/coslash/collector/internal/collector"
	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/launch"
	"github.com/centauri-ai/coslash/collector/internal/remote"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/syncv4"
	sessionbackupv1 "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
)

func v4CommandRunner(queue *syncv4.Queue, local *settings.Store, relay *remote.Manager) func(context.Context, hubclient.V4Command) error {
	return func(ctx context.Context, command hubclient.V4Command) (commandErr error) {
		defer func() {
			if commandErr != nil && (command.Type == "ssh.test" || command.Type == "ssh.install") {
				var sshCommandErr *syncv4.SSHCommandError
				if errors.As(commandErr, &sshCommandErr) {
					return
				}
				remote.LogSSHFailure(command.Type, commandErr)
			}
		}()
		var payload struct {
			SessionID string `json:"sessionId"`
			Mode      string `json:"mode"`
			Alias     string `json:"alias"`
			HostID    string `json:"hostId"`
		}
		if json.Unmarshal(command.Payload, &payload) != nil {
			return errors.New("invalid command payload")
		}
		switch command.Type {
		case "launch":
			if payload.Mode != launch.ResumeSession {
				return errors.New("invalid launch mode")
			}
			for _, entry := range queue.Entries() {
				if entry.SessionID != payload.SessionID || entry.Excluded {
					continue
				}
				state := local.State()
				if !state.Valid {
					return errors.New("invalid local settings")
				}
				if entry.Selection.SourceKind == sessionbackupv1.SourceSSH {
					found, alias, err := relay.LaunchSession(entry.Selection.SourceID, entry.Selection.Agent, entry.Selection.SessionID, launch.ResumeSession)
					if err != nil {
						return err
					}
					if found == nil {
						return errors.New("remote session unavailable")
					}
					return launch.RemoteTerminal(ctx, state.Config.Launch.Terminal, alias, found.Agent, found.WorkingDirectory, found.ID, launch.ResumeSession, "")
				}
				if entry.Selection.SourceKind != sessionbackupv1.SourceLocal {
					return errors.New("unsupported session source")
				}
				found, err := collector.GetSessionFactsByAgent(entry.Selection.Agent, entry.Selection.SessionID)
				if err != nil {
					return err
				}
				return launch.Terminal(ctx, state.Config.Launch.Terminal, found.Agent, found.WorkingDirectory, found.ID, launch.ResumeSession, "")
			}
			return errors.New("session unavailable")
		case "ssh.test":
			if payload.HostID == "" || payload.Alias == "" {
				return errors.New("invalid SSH host")
			}
			health, err := relay.TestAlias(ctx, payload.Alias)
			if err != nil {
				return err
			}
			if health.State != remote.StateOK {
				return &syncv4.SSHCommandError{Reason: health.Reason}
			}
			return configureCommandSSHHost(ctx, local, relay, payload.HostID, payload.Alias)
		case "ssh.install":
			if payload.HostID == "" || payload.Alias == "" {
				return errors.New("invalid SSH host")
			}
			if err := configureSSHHost(ctx, local, relay, payload.HostID, payload.Alias, false); err != nil {
				return err
			}
			health, err := relay.SetupHelperForAlias(ctx, payload.Alias, remote.Consent{Install: true, Upgrade: true})
			if err != nil {
				return err
			}
			if health.Helper == nil || !health.Helper.Compatible {
				reason := health.Reason
				if health.Helper != nil {
					reason = health.Helper.Reason
				}
				return &syncv4.SSHCommandError{Reason: reason, Install: true}
			}
			return configureCommandSSHHost(ctx, local, relay, payload.HostID, payload.Alias)
		default:
			return errors.New("unsupported command")
		}
	}
}

type commandSSHConfigurator interface {
	SaveHubSettings(*settings.Store, settings.Config) error
}

func configureCommandSSHHost(ctx context.Context, local *settings.Store, relay commandSSHConfigurator, hostID, alias string) error {
	return configureSSHHost(ctx, local, relay, hostID, alias, true)
}

func configureSSHHost(ctx context.Context, local *settings.Store, relay commandSSHConfigurator, hostID, alias string, confirmed bool) error {
	if !settings.ValidSSHAlias(alias) {
		return errors.New("invalid SSH alias")
	}
	state := local.State()
	if !state.Valid {
		return errors.New("Local settings must be configured")
	}
	current := state.Config.Remote
	if current != nil && current.Enabled && current.SSHAlias == alias && (!confirmed || current.HostID == hostID) {
		return nil
	}
	config := state.Config
	if current == nil || current.SSHAlias != alias {
		sum := sha256.Sum256([]byte(hostID))
		config.Remote = &settings.RemoteSettings{ID: fmt.Sprintf("r_%x", sum[:8]), SSHAlias: alias, Enabled: true}
	} else {
		copy := *current
		copy.Enabled = true
		config.Remote = &copy
	}
	if confirmed {
		config.Remote.HostID = hostID
	}

	return relay.SaveHubSettings(local, config)
}
