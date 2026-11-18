package hubclient

import (
	"bytes"
	"context"
	"encoding/json"

	"net/http"
	"regexp"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/machineid"
)

var installIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func (c *Client) reportedInstallID() string {
	if c == nil {
		return ""
	}
	id := strings.ToLower(strings.TrimSpace(c.InstallID))
	if !installIDPattern.MatchString(id) {
		return ""
	}
	return id
}

func (c *Client) reportedMachineID() string {
	if c == nil {
		return ""
	}
	return machineid.Normalize(c.MachineID)
}

func reportedDeviceName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 200 || strings.ContainsAny(name, "\x00\r\n") {
		return ""
	}
	return name
}

func (c *Client) addPairIdentity(fields map[string]string) {
	if id := c.reportedInstallID(); id != "" {
		fields["installId"] = id
	}
	if id := c.reportedMachineID(); id != "" {
		fields["machineId"] = id
	}
}

func (c *Client) postJSON(ctx context.Context, path string, fields map[string]string) (*http.Response, error) {
	body, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(path), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	return c.httpClient().Do(request)
}
