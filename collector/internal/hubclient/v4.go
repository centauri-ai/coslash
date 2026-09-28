package hubclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"
)

// V4 types match sync-v4/v2. They deliberately do not reuse v3 share receipts.
type V4Chunk struct {
	Ordinal int    `json:"ordinal"`
	Offset  int64  `json:"offset"`
	Bytes   int64  `json:"bytes"`
	SHA256  string `json:"sha256"`
}

type V4Artifact struct {
	Ordinal int       `json:"ordinal"`
	Kind    string    `json:"kind"`
	Bytes   int64     `json:"bytes"`
	SHA256  string    `json:"sha256"`
	Chunks  []V4Chunk `json:"chunks"`
}

type V4Manifest struct {
	ContentSHA256   string       `json:"contentSha256"`
	ProducerVersion string       `json:"producerVersion"`
	Artifacts       []V4Artifact `json:"artifacts"`
}

type V4Session struct {
	InstallID    string     `json:"installId"`
	LocalKeyHash string     `json:"localKeyHash"`
	Agent        string     `json:"agent"`
	Title        string     `json:"title"`
	Summary      string     `json:"summary,omitempty"`
	Repo         string     `json:"repo,omitempty"`
	Branch       string     `json:"branch,omitempty"`
	CWDLabel     string     `json:"cwdLabel,omitempty"`
	StartedAt    *time.Time `json:"startedAt,omitempty"`
	EndedAt      *time.Time `json:"endedAt,omitempty"`
	Tokens       int64      `json:"tokens,omitempty"`
	CostMicroUSD int64      `json:"costMicroUsd,omitempty"`
}

type V4Create struct {
	IdempotencyKey string     `json:"idempotencyKey"`
	Session        V4Session  `json:"session"`
	Manifest       V4Manifest `json:"manifest"`
}

type V4Missing struct {
	ArtifactOrdinal int    `json:"artifactOrdinal"`
	ChunkOrdinal    int    `json:"chunkOrdinal"`
	Offset          int64  `json:"offset"`
	Bytes           int64  `json:"bytes"`
	SHA256          string `json:"sha256"`
}

type V4Status struct {
	UploadID    string      `json:"uploadId"`
	SessionID   string      `json:"sessionId"`
	State       string      `json:"state"`
	Missing     []V4Missing `json:"missing"`
	RevisionID  string      `json:"revisionId"`
	FailureCode string      `json:"failureCode"`
	Dedup       bool        `json:"dedup"`
}

type V4Queue struct {
	Pending   int `json:"pending"`
	Failing   int `json:"failing"`
	FirstSync struct {
		RecentDone   int    `json:"recentDone"`
		RecentTotal  int    `json:"recentTotal"`
		HistoryState string `json:"historyState"`
	} `json:"firstSync"`
}

type V4Config struct {
	Paused         bool     `json:"paused"`
	DeviceOff      bool     `json:"deviceOff"`
	LeaveOut       []string `json:"leaveOut"`
	AgentKnowledge bool     `json:"agentKnowledge"`
}

type V4Command struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type V4CommandResult struct {
	CommandID string `json:"commandId"`
	Result    string `json:"result"`
	Error     string `json:"error,omitempty"`
}

type V4CheckIn struct {
	ConfigVersion          int64       `json:"configVersion"`
	Config                 V4Config    `json:"config"`
	Commands               []V4Command `json:"commands"`
	MinVersion             string      `json:"minVersion"`
	RecommendedVersion     string      `json:"recommendedVersion"`
	RecommendedDownloadURL string      `json:"recommendedDownloadUrl"`
	NextCheckInSeconds     int         `json:"nextCheckInSeconds"`
	UpdateRequired         bool        `json:"-"`
	RecommendedUpdate      bool        `json:"-"`
}

func (result *V4CheckIn) UnmarshalJSON(data []byte) error {
	type alias V4CheckIn
	var decoded alias
	var required struct {
		Config *struct {
			Paused         *bool     `json:"paused"`
			DeviceOff      *bool     `json:"deviceOff"`
			LeaveOut       *[]string `json:"leaveOut"`
			AgentKnowledge *bool     `json:"agentKnowledge"`
		} `json:"config"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	if err := json.Unmarshal(data, &required); err != nil {
		return err
	}
	if required.Config == nil || required.Config.Paused == nil || required.Config.DeviceOff == nil || required.Config.LeaveOut == nil || required.Config.AgentKnowledge == nil {
		return errors.New("v4 check-in omitted current policy fields")
	}
	*result = V4CheckIn(decoded)
	return nil
}

type V4Wait struct {
	ConfigVersion     int64 `json:"configVersion"`
	Changed           bool  `json:"changed"`
	CommandsAvailable bool  `json:"commandsAvailable"`
}

type V4Problem struct {
	Code string `json:"code"`
}

func (c *Client) V4Binding(ctx context.Context) (string, error) {
	if !c.configured() {
		return "", ErrNotPaired
	}
	credential, err := c.Credentials.Load(ctx)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(c.BaseURL.String() + "\x00" + credential))
	return hex.EncodeToString(sum[:]), nil
}

func (p V4Problem) Error() string { return "Hub v4: " + p.Code }

func (c *Client) v4Request(ctx context.Context, method, path string, body any, result any) error {
	if !c.configured() {
		return ErrNotPaired
	}
	credential, err := c.Credentials.Load(ctx)
	if err != nil {
		return err
	}
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint(path), payload)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Device "+credential)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := c.httpClient()
	if method == http.MethodGet && path == "/v4/devices/me/wait" || strings.HasPrefix(path, "/v4/devices/me/wait?") {
		client.Timeout = 60 * time.Second
	}
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNoContent {
		return nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var problem V4Problem
		_ = json.NewDecoder(io.LimitReader(response.Body, 1<<16)).Decode(&problem)
		if problem.Code == "" {
			problem.Code = fmt.Sprintf("http_%d", response.StatusCode)
		}
		return problem
	}
	if result != nil {
		return json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(result)
	}
	return nil
}

func (c *Client) V4CheckIn(ctx context.Context, queue V4Queue, appliedConfigVersion int64, results []V4CommandResult) (V4CheckIn, error) {
	var result V4CheckIn
	version := strings.SplitN(strings.TrimPrefix(c.CollectorVersion, "v"), "-", 2)[0]
	if !validClientVersion(version) {
		return result, errors.New("v4 sync requires a semantic Local version")
	}
	input := struct {
		ClientVersion        string            `json:"clientVersion"`
		Capabilities         []string          `json:"capabilities"`
		OS                   string            `json:"os"`
		AppliedConfigVersion int64             `json:"appliedConfigVersion"`
		AgentsFound          []string          `json:"agentsFound"`
		Queue                V4Queue           `json:"queue"`
		Results              []V4CommandResult `json:"results,omitempty"`
	}{version, []string{"sync-v4", "session-backup/v1", "launch", "ssh-relay"}, runtime.GOOS, appliedConfigVersion, []string{"codex"}, queue, results}
	err := c.v4Request(ctx, http.MethodPost, "/v4/devices/me/check-in", input, &result)
	if err == nil && (result.ConfigVersion < 1 || !validClientVersion(result.MinVersion)) {
		return V4CheckIn{}, errors.New("v4 check-in omitted current policy")
	}
	if err == nil {
		result.UpdateRequired = clientVersionLess(version, result.MinVersion)
		result.RecommendedUpdate = validClientVersion(result.RecommendedVersion) && clientVersionLess(version, result.RecommendedVersion)
	}
	return result, err
}

func (c *Client) V4Wait(ctx context.Context, since int64) (V4Wait, error) {
	var result V4Wait
	err := c.v4Request(ctx, http.MethodGet, "/v4/devices/me/wait?since="+fmt.Sprint(since), nil, &result)
	return result, err
}

func clientVersionLess(left, right string) bool {
	a, b := strings.Split(left, "."), strings.Split(right, ".")
	for i := range 3 {
		var x, y int
		fmt.Sscan(a[i], &x)
		fmt.Sscan(b[i], &y)
		if x != y {
			return x < y
		}
	}
	return false
}

func validClientVersion(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 3 || len(value) > 32 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, ch := range part {
			if ch < '0' || ch > '9' {
				return false
			}
		}
	}
	return true
}

func (c *Client) V4Create(ctx context.Context, input V4Create) (V4Status, error) {
	var result V4Status
	err := c.v4Request(ctx, http.MethodPost, "/v4/uploads", input, &result)
	return result, err
}

func (c *Client) V4Status(ctx context.Context, uploadID string) (V4Status, error) {
	var result V4Status
	err := c.v4Request(ctx, http.MethodGet, "/v4/uploads/"+url.PathEscape(uploadID), nil, &result)
	return result, err
}

func (c *Client) V4Finalize(ctx context.Context, uploadID string) (V4Status, error) {
	var result V4Status
	err := c.v4Request(ctx, http.MethodPost, "/v4/uploads/"+url.PathEscape(uploadID)+"/finalize", struct{}{}, &result)
	return result, err
}

func (c *Client) V4PutChunk(ctx context.Context, uploadID string, missing V4Missing, body io.Reader) error {
	if missing.Bytes < 1 || missing.Bytes > 64<<20 || missing.ArtifactOrdinal < 0 || missing.ChunkOrdinal < 0 {
		return errors.New("invalid v4 chunk")
	}
	data, err := io.ReadAll(io.LimitReader(body, missing.Bytes+1))
	if err != nil || int64(len(data)) != missing.Bytes {
		return errors.New("v4 chunk length changed")
	}
	coords := struct {
		Coords []struct {
			ArtifactOrdinal int `json:"artifactOrdinal"`
			ChunkOrdinal    int `json:"chunkOrdinal"`
		} `json:"coords"`
	}{}
	coords.Coords = append(coords.Coords, struct {
		ArtifactOrdinal int `json:"artifactOrdinal"`
		ChunkOrdinal    int `json:"chunkOrdinal"`
	}{missing.ArtifactOrdinal, missing.ChunkOrdinal})
	var signed struct {
		URLs []struct {
			ArtifactOrdinal int               `json:"artifactOrdinal"`
			ChunkOrdinal    int               `json:"chunkOrdinal"`
			URL             string            `json:"url"`
			Headers         map[string]string `json:"headers"`
		} `json:"urls"`
	}
	signErr := c.v4Request(ctx, http.MethodPost, "/v4/uploads/"+url.PathEscape(uploadID)+"/chunks:sign", coords, &signed)
	if signErr != nil {
		var problem V4Problem
		if !errors.As(signErr, &problem) || (problem.Code != "not_available" && problem.Code != "temporary_unavailable" && problem.Code != "http_501") {
			return signErr
		}
	}
	if signErr == nil && len(signed.URLs) == 1 {
		item := signed.URLs[0]
		if item.ArtifactOrdinal != missing.ArtifactOrdinal || item.ChunkOrdinal != missing.ChunkOrdinal {
			return errors.New("v4 signed chunk identity changed")
		}
		parsed, err := url.Parse(item.URL)
		if err != nil || parsed.User != nil || parsed.Hostname() == "" || parsed.Fragment != "" ||
			(parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLoopback(parsed.Hostname()))) {
			return errors.New("v4 signed URL is invalid")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, item.URL, bytes.NewReader(data))
		if err != nil {
			return err
		}
		req.ContentLength = missing.Bytes
		for name, value := range item.Headers {
			if strings.EqualFold(name, "Authorization") || strings.EqualFold(name, "Cookie") {
				return errors.New("v4 signed URL requested credential header")
			}
			req.Header.Set(name, value)
		}
		client := c.httpClient()
		if c.HTTP == nil {
			client.Timeout = 2 * time.Minute
		}
		response, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return c.v4ProxyChunk(ctx, uploadID, missing, bytes.NewReader(data))
		}
		io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		response.Body.Close()
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			return nil
		}
	}
	return c.v4ProxyChunk(ctx, uploadID, missing, bytes.NewReader(data))
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	parsed := net.ParseIP(host)
	return parsed != nil && parsed.IsLoopback()
}

func (c *Client) v4ProxyChunk(ctx context.Context, uploadID string, missing V4Missing, body io.Reader) error {
	credential, err := c.Credentials.Load(ctx)
	if err != nil {
		return err
	}
	path := fmt.Sprintf("/v4/uploads/%s/chunks/%d/%d", url.PathEscape(uploadID), missing.ArtifactOrdinal, missing.ChunkOrdinal)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.endpoint(path), body)
	if err != nil {
		return err
	}
	req.ContentLength = missing.Bytes
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Authorization", "Device "+credential)
	client := c.httpClient()
	if c.HTTP == nil {
		client.Timeout = 2 * time.Minute
	}
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		var problem V4Problem
		_ = json.NewDecoder(io.LimitReader(response.Body, 1<<16)).Decode(&problem)
		if problem.Code == "" {
			problem.Code = fmt.Sprintf("http_%d", response.StatusCode)
		}
		return problem
	}
	return nil
}

func (c *Client) V4Confirm(ctx context.Context, uploadID string, missing V4Missing) (V4Status, error) {
	var result V4Status
	coords := struct {
		Coords []struct {
			ArtifactOrdinal int `json:"artifactOrdinal"`
			ChunkOrdinal    int `json:"chunkOrdinal"`
		} `json:"coords"`
	}{}
	coords.Coords = append(coords.Coords, struct {
		ArtifactOrdinal int `json:"artifactOrdinal"`
		ChunkOrdinal    int `json:"chunkOrdinal"`
	}{missing.ArtifactOrdinal, missing.ChunkOrdinal})
	err := c.v4Request(ctx, http.MethodPost, "/v4/uploads/"+url.PathEscape(uploadID)+"/chunks:confirm", coords, &result)
	return result, err
}
