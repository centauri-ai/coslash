package hubclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

// postPairingJSON sends the pairing body. An older Hub rejects unknown JSON
// fields, so one invalid_request retry omits the computer ids.
func (c *Client) postPairingJSON(ctx context.Context, path string, fields map[string]string, action string) (*http.Response, error) {
	response, err := c.postJSON(ctx, path, fields)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", action, err)
	}
	if response.StatusCode != http.StatusBadRequest || (fields["installId"] == "" && fields["machineId"] == "") {
		return response, nil
	}
	problem := readProblem(response)
	_ = response.Body.Close()
	if problem.Code != "invalid_request" {
		return nil, problem
	}
	legacy := map[string]string{}
	for key, value := range fields {
		if key == "installId" || key == "machineId" {
			continue
		}
		legacy[key] = value
	}
	response, err = c.postJSON(ctx, path, legacy)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", action, err)
	}
	return response, nil
}
