package main

import (
	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/remote"
	"github.com/centauri-ai/coslash/collector/internal/settings"
)

func reconcileSSHHosts(local *settings.Store, relay *remote.Manager) func([]hubclient.V4SSHHost) error {
	return func(hosts []hubclient.V4SSHHost) error {
		if hosts == nil {
			return nil
		}
		state := local.State()
		if !state.Valid {
			return nil
		}
		current := state.Config.Remote
		if current == nil || current.HostID == "" {
			return nil
		}
		for _, host := range hosts {
			if host.ID == current.HostID || host.Alias == current.SSHAlias {
				return nil
			}
		}
		return relay.RemoveHubHost(local, *current)
	}
}
