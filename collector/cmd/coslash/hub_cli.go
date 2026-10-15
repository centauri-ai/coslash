package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/settings"
)

const (
	hubUsage                    = "usage: coslash hub login [--hub URL] [--scope SCOPE ...] | logout | tools [--json] | search <query> | call <tool> --json '{…}'"
	hubDefaultURL               = "https://hub.coslash.io/mcp"
	hubAgentKeychainService     = "ai.coslash.hub-agent"
	hubAgentKeychainAccount     = "default"
	hubAgentDeviceGrant         = "urn:ietf:params:oauth:grant-type:device_code"
	hubAgentDefaultScope        = "hub:read"
	hubAgentRequestTimeout      = 20 * time.Second
	hubAgentRefreshLeeway       = 30 * time.Second
	hubAgentMaxResponseBytes    = 1 << 20
	hubAgentDefaultPollInterval = 5 * time.Second
)

type hubAgentCredential struct {
	HubURL       string    `json:"hub_url"`
	Issuer       string    `json:"issuer"`
	ClientID     string    `json:"client_id"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
}

type hubOAuthEndpoints struct {
	Issuer                 string `json:"issuer"`
	RegistrationEndpoint   string `json:"registration_endpoint"`
	DeviceAuthorizationURL string `json:"device_authorization_endpoint"`
	TokenURL               string `json:"token_endpoint"`
	RevocationURL          string `json:"revocation_endpoint"`
}

type hubAgentCLI struct {
	output      io.Writer
	credentials hubclient.CredentialStore
	http        *http.Client
	now         func() time.Time
	sleep       func(context.Context, time.Duration) error
	pollEvery   time.Duration
}

func newHubAgentCLI(output io.Writer) *hubAgentCLI {
	return &hubAgentCLI{
		output: output, credentials: hubAgentCredentialStore(runtime.GOOS),
		http: &http.Client{Timeout: hubAgentRequestTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		now:  time.Now, sleep: sleepHubContext, pollEvery: hubAgentDefaultPollInterval,
	}
}

func hubAgentCredentialStore(goos string) hubclient.CredentialStore {
	if goos == "linux" {
		return hubclient.FileCredentialStore{Path: filepath.Join(settings.Home(), "hub-credentials", "agent-access")}
	}
	return hubclient.OSKeychain{Service: hubAgentKeychainService, Account: hubAgentKeychainAccount}
}

func runHubCLI(output io.Writer, args []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	return newHubAgentCLI(output).run(ctx, args)
}

func (c *hubAgentCLI) run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New(hubUsage)
	}
	switch args[0] {
	case "login":
		options, err := parseHubLoginArgs(args[1:])
		if err != nil {
			return err
		}
		return c.login(ctx, options.HubURL, strings.Join(options.Scopes, " "))
	case "logout":
		if len(args) != 1 {
			return errors.New(hubUsage)
		}
		return c.logout(ctx)
	case "tools":
		if len(args) > 2 || len(args) == 2 && args[1] != "--json" {
			return errors.New(hubUsage)
		}
		return c.listTools(ctx, len(args) == 2)
	case "search":
		query := strings.TrimSpace(strings.Join(args[1:], " "))
		if query == "" {
			return errors.New(hubUsage)
		}
		arguments, _ := json.Marshal(map[string]string{"query": query})
		return c.callTool(ctx, "coslash_search_sessions", arguments)
	case "call":
		if len(args) != 4 || args[2] != "--json" || !validHubToolName(args[1]) {
			return errors.New(hubUsage)
		}
		arguments := json.RawMessage(args[3])
		var object map[string]json.RawMessage
		if err := json.Unmarshal(arguments, &object); err != nil || object == nil {
			return errors.New("tool arguments must be a JSON object")
		}
		return c.callTool(ctx, args[1], arguments)
	default:
		return errors.New(hubUsage)
	}
}

type hubLoginOptions struct {
	HubURL string
	Scopes []string
}

func parseHubLoginArgs(args []string) (hubLoginOptions, error) {
	options := hubLoginOptions{HubURL: hubDefaultURL}
	var scopes []string
	seenScopes := map[string]bool{}
	seenHub := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--hub":
			if seenHub || i+1 >= len(args) {
				return hubLoginOptions{}, errors.New(hubUsage)
			}
			seenHub = true
			i++
			options.HubURL = args[i]
		case "--scope":
			if i+1 >= len(args) {
				return hubLoginOptions{}, errors.New(hubUsage)
			}
			i++
			scope := args[i]
			if !validHubAgentScope(scope) || seenScopes[scope] {
				return hubLoginOptions{}, errors.New("Hub scope is invalid or repeated")
			}
			seenScopes[scope] = true
			scopes = append(scopes, scope)
		default:
			return hubLoginOptions{}, errors.New(hubUsage)
		}
	}
	if _, err := validateHubURL(options.HubURL); err != nil {
		return hubLoginOptions{}, err
	}
	if len(scopes) == 0 {
		scopes = strings.Fields(hubAgentDefaultScope)
	}
	options.Scopes = scopes
	return options, nil
}

func validHubAgentScope(scope string) bool {
	switch scope {
	case "hub:read", "hub:write", "hub:share", "hub:admin":
		return true
	default:
		return false
	}
}

func validHubToolName(value string) bool {
	if !strings.HasPrefix(value, "coslash_") || len(value) <= len("coslash_") || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if character != '_' && (character < 'a' || character > 'z') && (character < '0' || character > '9') {
			return false
		}
	}
	return true
}

func validateHubURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "/mcp" {
		return nil, errors.New("Hub URL must be an absolute /mcp endpoint")
	}
	if err := validateHubWebURL(parsed); err != nil {
		return nil, errors.New("Hub URL must use HTTPS outside loopback development")
	}
	return parsed, nil
}

func validateHubWebURL(parsed *url.URL) error {
	if parsed == nil || parsed.Host == "" || parsed.User != nil {
		return errors.New("invalid Hub URL")
	}
	if parsed.Scheme == "https" {
		return nil
	}
	if parsed.Scheme == "http" {
		ip := net.ParseIP(parsed.Hostname())
		if strings.EqualFold(parsed.Hostname(), "localhost") || ip != nil && ip.IsLoopback() {
			return nil
		}
	}
	return errors.New("Hub URL must use HTTPS outside loopback development")
}

func (c *hubAgentCLI) discover(ctx context.Context, hubURL string) (hubOAuthEndpoints, error) {
	resource, err := validateHubURL(hubURL)
	if err != nil {
		return hubOAuthEndpoints{}, err
	}
	resourceURL := *resource
	resourceURL.Path = "/.well-known/oauth-protected-resource/mcp"
	resourceURL.RawPath = ""
	var protected struct {
		Resource             string   `json:"resource"`
		AuthorizationServers []string `json:"authorization_servers"`
	}
	if err := c.getJSON(ctx, resourceURL.String(), &protected); err != nil {
		return hubOAuthEndpoints{}, fmt.Errorf("discover Hub authorization server: %w", err)
	}
	if protected.Resource != hubURL || len(protected.AuthorizationServers) == 0 {
		return hubOAuthEndpoints{}, errors.New("Hub authorization metadata does not match the selected /mcp URL")
	}
	issuerURL, err := validateIssuerURL(protected.AuthorizationServers[0], resource)
	if err != nil {
		return hubOAuthEndpoints{}, err
	}
	metadataURL := *issuerURL
	metadataURL.Path = "/.well-known/oauth-authorization-server"
	metadataURL.RawPath = ""
	var endpoints hubOAuthEndpoints
	if err := c.getJSON(ctx, metadataURL.String(), &endpoints); err != nil {
		return hubOAuthEndpoints{}, fmt.Errorf("read Hub OAuth metadata: %w", err)
	}
	if endpoints.Issuer != strings.TrimRight(issuerURL.String(), "/") {
		return hubOAuthEndpoints{}, errors.New("Hub OAuth issuer metadata does not match the authorization server")
	}
	for _, endpoint := range []string{endpoints.RegistrationEndpoint, endpoints.DeviceAuthorizationURL, endpoints.TokenURL, endpoints.RevocationURL} {
		if err := validateOAuthEndpoint(endpoint, issuerURL); err != nil {
			return hubOAuthEndpoints{}, err
		}
	}
	return endpoints, nil
}

func validateIssuerURL(raw string, resource *url.URL) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Path != "" && parsed.Path != "/" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("Hub authorization server URL is invalid")
	}
	if err := validateHubWebURL(parsed); err != nil {
		return nil, errors.New("Hub authorization server must use HTTPS outside loopback development")
	}
	if resource.Scheme == "https" && parsed.Scheme != "https" {
		return nil, errors.New("Hub authorization server must use HTTPS")
	}
	return parsed, nil
}

func validateOAuthEndpoint(raw string, issuer *url.URL) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Scheme != issuer.Scheme || !strings.EqualFold(parsed.Host, issuer.Host) {
		return errors.New("Hub OAuth endpoint does not match its authorization server")
	}
	if err := validateHubWebURL(parsed); err != nil {
		return errors.New("Hub OAuth endpoint must use HTTPS outside loopback development")
	}
	return nil
}

func (c *hubAgentCLI) login(ctx context.Context, hubURL, scope string) error {
	if _, err := validateHubURL(hubURL); err != nil {
		return err
	}
	endpoints, err := c.discover(ctx, hubURL)
	if err != nil {
		return err
	}
	registration, err := c.postJSON(ctx, endpoints.RegistrationEndpoint, map[string]any{
		"client_name": "coSlash Local CLI", "redirect_uris": []string{"http://127.0.0.1"},
	})
	if err != nil {
		return fmt.Errorf("register coSlash Hub CLI: %w", err)
	}
	var client struct {
		ClientID string `json:"client_id"`
	}
	if err := json.Unmarshal(registration, &client); err != nil || client.ClientID == "" {
		return errors.New("Hub did not return a client ID")
	}
	deviceResponse, err := c.postForm(ctx, endpoints.DeviceAuthorizationURL, url.Values{
		"client_id": {client.ClientID}, "resource": {hubURL}, "scope": {scope},
	})
	if err != nil {
		return fmt.Errorf("start Hub device authorization: %w", err)
	}
	var device struct {
		DeviceCode              string `json:"device_code"`
		UserCode                string `json:"user_code"`
		VerificationURI         string `json:"verification_uri"`
		VerificationURIComplete string `json:"verification_uri_complete"`
		ExpiresIn               int    `json:"expires_in"`
		Interval                int    `json:"interval"`
	}
	if err := json.Unmarshal(deviceResponse, &device); err != nil || device.DeviceCode == "" || device.UserCode == "" || device.ExpiresIn < 1 {
		return errors.New("Hub returned an invalid device authorization response")
	}
	if !validHubUserCode(device.UserCode) || device.ExpiresIn > 900 || device.Interval < 0 || device.Interval > 60 {
		return errors.New("Hub returned an invalid device authorization response")
	}
	verificationURI := device.VerificationURIComplete
	if verificationURI == "" {
		verificationURI = device.VerificationURI
	}
	if err := validateHubLinkURL(verificationURI, endpoints.Issuer); err != nil {
		return err
	}
	fmt.Fprintf(c.output, "Open %s\nEnter code: %s\nWaiting for Hub approval...\n", verificationURI, device.UserCode)
	interval := time.Duration(device.Interval) * time.Second
	if interval < time.Second {
		interval = hubAgentDefaultPollInterval
	}
	deadline := c.now().Add(time.Duration(device.ExpiresIn) * time.Second)
	tokens, err := c.pollDeviceToken(ctx, endpoints, hubURL, client.ClientID, device.DeviceCode, interval, deadline)
	if err != nil {
		return err
	}
	credential := hubAgentCredential{
		HubURL: hubURL, Issuer: endpoints.Issuer, ClientID: client.ClientID,
		AccessToken: tokens.AccessToken, RefreshToken: tokens.RefreshToken,
		ExpiresAt: c.now().Add(time.Duration(tokens.ExpiresIn) * time.Second),
	}
	if err := c.saveCredential(ctx, credential); err != nil {
		return fmt.Errorf("save Hub credentials: %w", err)
	}
	fmt.Fprintln(c.output, "Connected to coSlash Hub.")
	if credential.RefreshToken == "" {
		fmt.Fprintf(c.output, "Hub did not issue a refresh token; access expires in %s and will need another device login.\n", time.Duration(tokens.ExpiresIn)*time.Second)
	}
	return nil
}

func validHubUserCode(value string) bool {
	if len(value) < 4 || len(value) > 16 {
		return false
	}
	for _, character := range value {
		if character != '-' && (character < 'A' || character > 'Z') && (character < '0' || character > '9') {
			return false
		}
	}
	return true
}

func validateVerificationURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" || strings.ContainsAny(raw, "\r\n\t") {
		return errors.New("Hub returned an invalid device verification URL")
	}
	if err := validateHubWebURL(parsed); err != nil {
		return errors.New("Hub verification URL must use HTTPS outside loopback development")
	}
	return nil
}

func validateHubLinkURL(raw, issuer string) error {
	if err := validateVerificationURL(raw); err != nil {
		return err
	}
	link, _ := url.Parse(raw)
	issuerURL, _ := url.Parse(issuer)
	if !strings.EqualFold(link.Scheme, issuerURL.Scheme) || !strings.EqualFold(link.Host, issuerURL.Host) {
		return errors.New("Hub link does not match its authorization server")
	}
	return nil
}

type hubOAuthToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

func (c *hubAgentCLI) pollDeviceToken(ctx context.Context, endpoints hubOAuthEndpoints, resource, clientID, deviceCode string, interval time.Duration, deadline time.Time) (hubOAuthToken, error) {
	for c.now().Before(deadline) {
		data, err := c.postForm(ctx, endpoints.TokenURL, url.Values{
			"grant_type": {hubAgentDeviceGrant}, "device_code": {deviceCode},
			"client_id": {clientID}, "resource": {resource},
		})
		if err == nil {
			var tokens hubOAuthToken
			if json.Unmarshal(data, &tokens) != nil || tokens.AccessToken == "" || tokens.ExpiresIn < 1 || tokens.ExpiresIn > 86400 || !strings.EqualFold(tokens.TokenType, "bearer") {
				return hubOAuthToken{}, errors.New("Hub returned an invalid access token")
			}
			return tokens, nil
		}
		var oauthErr *hubOAuthError
		if !errors.As(err, &oauthErr) {
			return hubOAuthToken{}, fmt.Errorf("poll Hub device authorization: %w", err)
		}
		switch oauthErr.Code {
		case "authorization_pending", "temporarily_unavailable":
		case "slow_down":
			interval += 5 * time.Second
		case "access_denied":
			return hubOAuthToken{}, errors.New("Hub device authorization was denied")
		case "expired_token":
			return hubOAuthToken{}, errors.New("Hub device authorization expired; run coslash hub login again")
		default:
			return hubOAuthToken{}, fmt.Errorf("Hub device authorization failed: %s", safeHubErrorCode(oauthErr.Code))
		}
		if err := c.sleep(ctx, interval); err != nil {
			return hubOAuthToken{}, err
		}
	}
	return hubOAuthToken{}, errors.New("Hub device authorization expired; run coslash hub login again")
}

func (c *hubAgentCLI) logout(ctx context.Context) error {
	credential, err := c.loadCredential(ctx)
	if err != nil && !errors.Is(err, hubclient.ErrNotPaired) {
		return err
	}
	var revokeErr error
	if err == nil {
		token := credential.RefreshToken
		if token == "" {
			token = credential.AccessToken
		}
		if token != "" {
			endpoints, discoverErr := c.discover(ctx, credential.HubURL)
			if discoverErr == nil {
				_, revokeErr = c.postForm(ctx, endpoints.RevocationURL, url.Values{"client_id": {credential.ClientID}, "token": {token}})
			} else {
				revokeErr = discoverErr
			}
		}
	}
	if err := c.credentials.Delete(ctx); err != nil {
		return fmt.Errorf("delete Hub credentials: %w", err)
	}
	fmt.Fprintln(c.output, "Hub credentials removed.")
	if revokeErr != nil {
		return errors.New("local credentials were removed, but Hub could not revoke the token; revoke this client in Hub settings")
	}
	return nil
}

func (c *hubAgentCLI) listTools(ctx context.Context, asJSON bool) error {
	result, err := c.mcpRequest(ctx, "tools/list", map[string]any{})
	if err != nil {
		return err
	}
	var response map[string]json.RawMessage
	if err := json.Unmarshal(result, &response); err != nil {
		return errors.New("Hub returned an invalid tools/list result")
	}
	rawTools, ok := response["tools"]
	if !ok {
		return errors.New("Hub returned an invalid tools/list result")
	}
	var tools []struct {
		Name        string `json:"name"`
		Title       string `json:"title"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(rawTools, &tools); err != nil {
		return errors.New("Hub returned an invalid tools/list result")
	}
	if asJSON {
		var value any
		if err := json.Unmarshal(rawTools, &value); err != nil {
			return errors.New("Hub returned an invalid tools/list result")
		}
		return writeHubJSON(c.output, value)
	}
	for _, tool := range tools {
		if tool.Title != "" {
			fmt.Fprintf(c.output, "%s\t%s\n", tool.Name, tool.Title)
		} else {
			fmt.Fprintln(c.output, tool.Name)
		}
	}
	return nil
}

func (c *hubAgentCLI) callTool(ctx context.Context, name string, arguments json.RawMessage) error {
	result, err := c.mcpToolCall(ctx, name, arguments)
	if err != nil {
		return err
	}
	payload, err := hubToolPayload(result)
	if err != nil {
		return err
	}
	if result.IsError {
		code, data := hubToolError(payload)
		if code != "CONFIRMATION_REQUIRED" {
			_ = writeHubJSON(c.output, payload)
			if code == "" {
				return fmt.Errorf("Hub tool %s failed", name)
			}
			return fmt.Errorf("Hub tool %s failed (%s)", name, safeHubErrorCode(code))
		}
		return c.confirmToolCall(ctx, name, arguments, data)
	}
	return writeHubJSON(c.output, payload)
}

type hubMCPToolResult struct {
	IsError           bool            `json:"isError"`
	StructuredContent json.RawMessage `json:"structuredContent"`
	Content           []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

func hubToolPayload(result hubMCPToolResult) (any, error) {
	if len(result.StructuredContent) > 0 && string(result.StructuredContent) != "null" {
		var value any
		if err := json.Unmarshal(result.StructuredContent, &value); err != nil {
			return nil, errors.New("Hub returned invalid structured tool content")
		}
		return value, nil
	}
	for _, item := range result.Content {
		if item.Type == "text" {
			var value any
			if json.Unmarshal([]byte(item.Text), &value) == nil {
				return value, nil
			}
			return item.Text, nil
		}
	}
	return nil, errors.New("Hub returned an empty tool result")
}

func hubToolError(payload any) (string, map[string]any) {
	root, ok := payload.(map[string]any)
	if !ok {
		return "", nil
	}
	data, ok := root["error"].(map[string]any)
	if !ok {
		data = root
	}
	code, _ := data["code"].(string)
	return code, data
}

func (c *hubAgentCLI) confirmToolCall(ctx context.Context, tool string, arguments json.RawMessage, failure map[string]any) error {
	approvalURL, _ := failure["approve_url"].(string)
	actionID, _ := failure["action_id"].(string)
	seconds, _ := failure["expires_in_s"].(float64)
	if approvalURL == "" || actionID == "" || seconds < 1 || seconds > 300 {
		return errors.New("Hub requested approval without a valid action link")
	}
	credential, err := c.loadCredential(ctx)
	if err != nil {
		return err
	}
	if err := validateHubLinkURL(approvalURL, credential.Issuer); err != nil {
		return errors.New("Hub returned an invalid approval URL")
	}
	fmt.Fprintf(c.output, "Human approval required. Open %s\n", approvalURL)
	deadline := c.now().Add(time.Duration(seconds) * time.Second)
	for c.now().Before(deadline) {
		if err := c.sleep(ctx, c.pollEvery); err != nil {
			return err
		}
		result, err := c.mcpToolCall(ctx, "coslash_get_action", json.RawMessage(fmt.Sprintf(`{"action_id":%q}`, actionID)))
		if err != nil {
			return fmt.Errorf("check Hub approval: %w", err)
		}
		payload, err := hubToolPayload(result)
		if err != nil {
			return err
		}
		if result.IsError {
			return errors.New("Hub rejected the action status request")
		}
		status, _ := payload.(map[string]any)
		state, _ := status["state"].(string)
		switch state {
		case "pending":
			continue
		case "denied":
			return errors.New("Hub action was denied")
		case "expired":
			return errors.New("Hub approval expired")
		case "completed":
			if value, ok := status["result"]; ok {
				return writeHubJSON(c.output, value)
			}
			return errors.New("Hub action completed without a result")
		case "approved":
			retry, err := c.mcpToolCall(ctx, tool, arguments)
			if err != nil {
				return err
			}
			retryPayload, err := hubToolPayload(retry)
			if err != nil {
				return err
			}
			if retry.IsError {
				_ = writeHubJSON(c.output, retryPayload)
				code, _ := hubToolError(retryPayload)
				if code == "" {
					return errors.New("Hub action could not be completed")
				}
				return fmt.Errorf("Hub action could not be completed (%s)", safeHubErrorCode(code))
			}
			return writeHubJSON(c.output, retryPayload)
		default:
			return errors.New("Hub returned an unknown action status")
		}
	}
	return errors.New("Hub approval expired")
}

func (c *hubAgentCLI) mcpToolCall(ctx context.Context, name string, arguments json.RawMessage) (hubMCPToolResult, error) {
	var result hubMCPToolResult
	request := map[string]any{"name": name, "arguments": json.RawMessage(arguments)}
	response, err := c.mcpRequest(ctx, "tools/call", request)
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(response, &result); err != nil {
		return result, errors.New("Hub returned an invalid tool result")
	}
	return result, nil
}

type hubRPCError struct {
	Code int `json:"code"`
}

func (c *hubAgentCLI) mcpRequest(ctx context.Context, method string, params any) (json.RawMessage, error) {
	credential, err := c.loadCredential(ctx)
	if err != nil {
		return nil, errors.New("Hub is not logged in; run coslash hub login")
	}
	credential, err = c.refreshIfNeeded(ctx, credential, false)
	if err != nil {
		return nil, err
	}
	result, status, err := c.sendMCP(ctx, credential, method, params)
	if err != nil {
		return nil, err
	}
	if status == http.StatusUnauthorized {
		credential, err = c.refreshIfNeeded(ctx, credential, true)
		if err != nil {
			return nil, err
		}
		result, status, err = c.sendMCP(ctx, credential, method, params)
		if err != nil {
			return nil, err
		}
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("Hub MCP request failed (HTTP %d)", status)
	}
	var response struct {
		Error  *hubRPCError    `json:"error"`
		Result json.RawMessage `json:"result"`
	}
	if json.Unmarshal(result, &response) != nil {
		return nil, errors.New("Hub returned an invalid JSON-RPC response")
	}
	if response.Error != nil {
		return nil, fmt.Errorf("Hub MCP request failed (%d)", response.Error.Code)
	}
	if len(response.Result) == 0 {
		return nil, errors.New("Hub returned an empty JSON-RPC result")
	}
	return response.Result, nil
}

func (c *hubAgentCLI) sendMCP(ctx context.Context, credential hubAgentCredential, method string, params any) ([]byte, int, error) {
	requestBody, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": method, "params": params,
	})
	if err != nil {
		return nil, 0, errors.New("encode Hub MCP request")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, credential.HubURL, bytes.NewReader(requestBody))
	if err != nil {
		return nil, 0, errors.New("create Hub MCP request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+credential.AccessToken)
	response, err := c.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("connect to Hub MCP: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, hubAgentMaxResponseBytes+1))
	if err != nil || len(body) > hubAgentMaxResponseBytes {
		return nil, response.StatusCode, errors.New("read Hub MCP response")
	}
	return body, response.StatusCode, nil
}

func (c *hubAgentCLI) refreshIfNeeded(ctx context.Context, credential hubAgentCredential, force bool) (hubAgentCredential, error) {
	if !force && credential.ExpiresAt.After(c.now().Add(hubAgentRefreshLeeway)) {
		return credential, nil
	}
	if credential.RefreshToken == "" {
		return hubAgentCredential{}, errors.New("Hub access token expired and no refresh token is available; run coslash hub login")
	}
	endpoints, err := c.discover(ctx, credential.HubURL)
	if err != nil {
		return hubAgentCredential{}, err
	}
	data, err := c.postForm(ctx, endpoints.TokenURL, url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {credential.RefreshToken},
		"client_id": {credential.ClientID}, "resource": {credential.HubURL},
	})
	if err != nil {
		return hubAgentCredential{}, fmt.Errorf("refresh Hub access: %w", err)
	}
	var tokens hubOAuthToken
	if json.Unmarshal(data, &tokens) != nil || tokens.AccessToken == "" || tokens.RefreshToken == "" || tokens.ExpiresIn < 1 || tokens.ExpiresIn > 86400 || !strings.EqualFold(tokens.TokenType, "bearer") {
		return hubAgentCredential{}, errors.New("Hub returned an invalid refreshed token")
	}
	credential.AccessToken = tokens.AccessToken
	credential.RefreshToken = tokens.RefreshToken
	credential.ExpiresAt = c.now().Add(time.Duration(tokens.ExpiresIn) * time.Second)
	if err := c.saveCredential(ctx, credential); err != nil {
		return hubAgentCredential{}, fmt.Errorf("save refreshed Hub credentials: %w", err)
	}
	return credential, nil
}

func (c *hubAgentCLI) loadCredential(ctx context.Context) (hubAgentCredential, error) {
	value, err := c.credentials.Load(ctx)
	if err != nil {
		return hubAgentCredential{}, err
	}
	var credential hubAgentCredential
	if json.Unmarshal([]byte(value), &credential) != nil || credential.HubURL == "" || credential.Issuer == "" || credential.ClientID == "" || credential.AccessToken == "" || credential.ExpiresAt.IsZero() {
		return hubAgentCredential{}, errors.New("stored Hub credentials are invalid; run coslash hub login again")
	}
	if _, err := validateHubURL(credential.HubURL); err != nil {
		return hubAgentCredential{}, errors.New("stored Hub credentials are invalid; run coslash hub login again")
	}
	return credential, nil
}

func (c *hubAgentCLI) saveCredential(ctx context.Context, credential hubAgentCredential) error {
	encoded, err := json.Marshal(credential)
	if err != nil {
		return errors.New("encode Hub credentials")
	}
	return c.credentials.Save(ctx, string(encoded))
}

type hubOAuthError struct {
	Code string `json:"error"`
}

func (e *hubOAuthError) Error() string { return safeHubErrorCode(e.Code) }

func safeHubErrorCode(value string) string {
	if value == "" || len(value) > 64 {
		return "UNKNOWN"
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') && (character < '0' || character > '9') && character != '_' {
			return "UNKNOWN"
		}
	}
	return value
}

func (c *hubAgentCLI) getJSON(ctx context.Context, endpoint string, target any) error {
	body, status, err := c.request(ctx, http.MethodGet, endpoint, "", nil)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return hubHTTPError(status, body)
	}
	if err := json.Unmarshal(body, target); err != nil {
		return errors.New("Hub returned invalid JSON metadata")
	}
	return nil
}

func (c *hubAgentCLI) postJSON(ctx context.Context, endpoint string, value any) ([]byte, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, errors.New("encode Hub OAuth request")
	}
	response, status, err := c.request(ctx, http.MethodPost, endpoint, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, hubHTTPError(status, response)
	}
	return response, nil
}

func (c *hubAgentCLI) postForm(ctx context.Context, endpoint string, values url.Values) ([]byte, error) {
	response, status, err := c.request(ctx, http.MethodPost, endpoint, "application/x-www-form-urlencoded", strings.NewReader(values.Encode()))
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, hubHTTPError(status, response)
	}
	return response, nil
}

func (c *hubAgentCLI) request(ctx context.Context, method, endpoint, contentType string, body io.Reader) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, 0, errors.New("create Hub request")
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json")
	response, err := c.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("connect to Hub: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, hubAgentMaxResponseBytes+1))
	if err != nil || len(data) > hubAgentMaxResponseBytes {
		return nil, response.StatusCode, errors.New("read Hub response")
	}
	return data, response.StatusCode, nil
}

func hubHTTPError(status int, body []byte) error {
	var oauthError hubOAuthError
	if json.Unmarshal(body, &oauthError) == nil && oauthError.Code != "" {
		return &oauthError
	}
	return fmt.Errorf("Hub request failed (HTTP %d)", status)
}

func writeHubJSON(output io.Writer, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return errors.New("encode JSON output")
	}
	_, err = fmt.Fprintln(output, string(encoded))
	return err
}

func sleepHubContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
