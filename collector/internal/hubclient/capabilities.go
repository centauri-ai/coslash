package hubclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

type capabilityDocument struct {
	Product                    string   `json:"product"`
	ServerID                   string   `json:"serverId"`
	DisplayName                string   `json:"displayName"`
	ProtocolVersions           []string `json:"protocolVersions"`
	SnapshotVersions           []string `json:"snapshotVersions"`
	MaxSnapshotBytes           int64    `json:"maxSnapshotBytes"`
	FullSessionVersions        []string `json:"fullSessionVersions"`
	MaxFullSessionBytes        int64    `json:"maxFullSessionBytes"`
	MaxRequestBytes            int64    `json:"maxRequestBytes"`
	BackupVersions             []string `json:"backupVersions"`
	BackupUploadVersions       []string `json:"backupUploadVersions"`
	MaxBackupBytes             int64    `json:"maxBackupBytes"`
	MaxBackupChunkBytes        int64    `json:"maxBackupChunkBytes"`
	BackupWorkspaceBytes       int64    `json:"backupWorkspaceBytes"`
	BackupUploadExpiresSeconds int64    `json:"backupUploadExpiresSeconds"`
	PairingURL                 string   `json:"pairingUrl"`
	TeamURL                    string   `json:"teamUrl"`
}

func (c *Client) fetchCapabilities(ctx context.Context) (capabilityDocument, error) {
	if c == nil || c.BaseURL == nil {
		return capabilityDocument{}, capabilityFailure("incompatible_server", false, errors.New("Hub server is not configured"))
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint("/.well-known/coslash-server"), nil)
	if err != nil {
		return capabilityDocument{}, capabilityFailure("temporary_unavailable", true, err)
	}
	response, err := c.httpClient().Do(request)
	if err != nil {
		return capabilityDocument{}, capabilityFailure("network_unavailable", true, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		failure := fmt.Errorf("Hub capability request returned %d", response.StatusCode)
		if response.StatusCode >= http.StatusInternalServerError || response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusTooManyRequests {
			return capabilityDocument{}, capabilityFailure("temporary_unavailable", true, failure)
		}
		return capabilityDocument{}, capabilityFailure("incompatible_server", false, failure)
	}
	var capability capabilityDocument
	if err := decodeBounded(response.Body, &capability); err != nil {
		return capabilityDocument{}, capabilityFailure("temporary_unavailable", true, err)
	}
	return capability, nil
}
