package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/tailscale/hujson"

	"github.com/centauri-ai/coslash/collector/internal/atomicfile"
)

const (
	mcpUsage       = "usage: coslash mcp setup|login|switch <claude|codex|cursor|opencode> [--url https://mcp.hub.coslash.io/mcp]"
	mcpDefaultURL  = "https://mcp.hub.coslash.io/mcp"
	mcpServerName  = "coslash"
	mcpCommandTime = 5 * time.Minute
)

func runMCP(stdout io.Writer, args []string) error {
	if len(args) < 2 || len(args) > 4 {
		return errors.New(mcpUsage)
	}
	action, agent := args[0], args[1]
	if agent != "claude" && agent != "codex" && agent != "cursor" && agent != "opencode" {
		return errors.New(mcpUsage)
	}
	endpoint := mcpDefaultURL
	if len(args) > 2 {
		if action != "setup" || len(args) != 4 || args[2] != "--url" {
			return errors.New(mcpUsage)
		}
		endpoint = args[3]
	}
	if action == "setup" {
		if err := validateMCPEndpoint(endpoint); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), mcpCommandTime)
	defer cancel()
	switch action {
	case "setup":
		if err := setupMCP(ctx, stdout, agent, endpoint); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "coSlash MCP configured for %s. Run `coslash mcp login %s` to authorize in your browser.\n", agent, agent)
	case "login":
		return loginMCP(ctx, stdout, agent)
	case "switch":
		if agent == "cursor" {
			return errors.New("Cursor does not expose MCP credential logout in its CLI; disconnect coSlash in Cursor MCP settings, switch the coSlash browser account, then run `coslash mcp login cursor`")
		}
		if err := logoutMCP(ctx, stdout, agent); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s coSlash MCP credentials cleared. Switch the coSlash account in your browser, then run `coslash mcp login %s`.\n", agent, agent)
	default:
		return errors.New(mcpUsage)
	}
	return nil
}

func validateMCPEndpoint(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "/mcp" {
		return errors.New("MCP URL must be an absolute HTTPS /mcp endpoint")
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
	return errors.New("MCP URL must use HTTPS outside loopback development")
}

func setupMCP(ctx context.Context, output io.Writer, agent, endpoint string) error {
	switch agent {
	case "claude":
		return runMCPAgent(ctx, output, "claude", "mcp", "add", "--transport", "http", "--scope", "user", mcpServerName, endpoint)
	case "codex":
		return runMCPAgent(ctx, output, "codex", "mcp", "add", mcpServerName, "--url", endpoint, "--oauth-client-registration", "dcr", "--oauth-resource", endpoint)
	case "cursor":
		path, err := mcpConfigPath(".cursor", "mcp.json")
		if err != nil {
			return err
		}
		return writeMCPConfig(path, "mcpServers", endpoint, false)
	case "opencode":
		path, err := opencodeMCPConfigPath()
		if err != nil {
			return err
		}
		return writeMCPConfig(path, "mcp", endpoint, true)
	}
	return errors.New(mcpUsage)
}

func loginMCP(ctx context.Context, output io.Writer, agent string) error {
	switch agent {
	case "claude", "codex":
		return runMCPAgent(ctx, output, agent, "mcp", "login", mcpServerName)
	case "opencode":
		return runMCPAgent(ctx, output, "opencode", "mcp", "auth", mcpServerName)
	case "cursor":
		return runMCPAgent(ctx, output, "cursor-agent", "mcp", "login", mcpServerName)
	}
	return errors.New(mcpUsage)
}

func logoutMCP(ctx context.Context, output io.Writer, agent string) error {
	switch agent {
	case "claude", "codex":
		return runMCPAgent(ctx, output, agent, "mcp", "logout", mcpServerName)
	case "opencode":
		return runMCPAgent(ctx, output, "opencode", "mcp", "logout", mcpServerName)
	}
	return errors.New(mcpUsage)
}

func runMCPAgent(ctx context.Context, output io.Writer, executable string, args ...string) error {
	command := exec.CommandContext(ctx, executable, args...)
	command.Stdin = os.Stdin
	command.Stdout = output
	command.Stderr = output
	if err := command.Run(); err != nil {
		return fmt.Errorf("%s MCP command failed: %w", executable, err)
	}
	return nil
}

func mcpConfigPath(parts ...string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(append([]string{home}, parts...)...), nil
}

func opencodeMCPConfigPath() (string, error) {
	root := os.Getenv("XDG_CONFIG_HOME")
	if root == "" {
		var err error
		root, err = mcpConfigPath(".config")
		if err != nil {
			return "", err
		}
	}
	path := filepath.Join(root, "opencode", "opencode.json")
	if _, err := os.Lstat(path + "c"); err == nil {
		if _, jsonErr := os.Lstat(path); jsonErr == nil {
			return "", errors.New("both opencode.json and opencode.jsonc exist; choose one active OpenCode config before setup")
		} else if !errors.Is(jsonErr, os.ErrNotExist) {
			return "", jsonErr
		}
		return path + "c", nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return path, nil
}

// writeMCPConfig adds the coSlash server to a JSON or JSONC (by the .jsonc
// suffix) agent config. It changes only the added member, so the key order,
// comments, formatting, and file mode of an existing config stay as they were.
func writeMCPConfig(path, key, endpoint string, opencode bool) error {
	var server any = map[string]string{"url": endpoint}
	if opencode {
		server = map[string]any{"type": "remote", "url": endpoint, "enabled": true, "oauth": map[string]string{"scope": "mcp:read mcp:ask"}}
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		data, _ := json.MarshalIndent(map[string]any{key: map[string]any{mcpServerName: server}}, "", "  ")
		return atomicfile.Write(path, append(data, '\n'), 0o600, nil)
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return errors.New("MCP config must be a regular file under 1 MiB")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	root, err := hujson.Parse(data)
	if err != nil || !strings.HasSuffix(path, ".jsonc") && !root.IsStandard() {
		return errors.New("MCP config is not a supported JSON object")
	}
	object, ok := root.Value.(*hujson.Object)
	if !ok {
		return errors.New("MCP config is not a supported JSON object")
	}
	parent, name, value := object, key, any(map[string]any{mcpServerName: server})
	if settings := root.Find("/" + key); settings != nil {
		servers, ok := settings.Value.(*hujson.Object)
		if !ok {
			return errors.New("MCP server settings are not a JSON object")
		}
		if existing := root.Find("/" + key + "/" + mcpServerName); existing != nil {
			previous := existing.Clone()
			previous.Standardize()
			encodedServer, _ := json.Marshal(server)
			var previousValue, desiredValue any
			if json.Unmarshal(previous.Pack(), &previousValue) != nil || json.Unmarshal(encodedServer, &desiredValue) != nil || !reflect.DeepEqual(previousValue, desiredValue) {
				return errors.New("coSlash MCP already has different settings; review that entry before setup")
			}
			return nil
		}
		parent, name, value = servers, mcpServerName, server
	}
	if err := appendMember(object, parent, name, value); err != nil {
		return err
	}
	return atomicfile.Write(path, root.Pack(), info.Mode().Perm(), nil)
}

// appendMember adds name as the last member of parent. When the member before
// it starts on its own line, the new member copies that indentation and the
// trailing-comma style. Otherwise it is added in compact form.
func appendMember(root, parent *hujson.Object, name string, value any) error {
	lineIndent := func(member hujson.ObjectMember) (string, bool) {
		i := bytes.LastIndexByte(member.Name.BeforeExtra, '\n')
		return string(member.Name.BeforeExtra[i+1:]), i >= 0
	}
	encodedName, _ := json.Marshal(name)
	separator, multiline, indent := []byte(":"), false, ""
	if n := len(parent.Members); n > 0 {
		indent, multiline = lineIndent(parent.Members[n-1])
	}
	encoded, err := json.Marshal(value)
	if multiline {
		separator = []byte(": ")
		unit, ok := lineIndent(root.Members[0])
		if !ok || unit == "" {
			unit = "  "
		}
		encoded, err = json.MarshalIndent(value, indent, unit)
	}
	if err != nil {
		return err
	}
	member, err := hujson.Parse(slices.Concat([]byte("{"), encodedName, separator, encoded, []byte("}")))
	if err != nil {
		return err
	}
	added := member.Value.(*hujson.Object).Members[0]
	if multiline {
		added.Name.BeforeExtra = hujson.Extra("\n" + indent)
		if parent.Members[len(parent.Members)-1].Value.AfterExtra != nil {
			added.Value.AfterExtra = hujson.Extra{}
		}
	}
	parent.Members = append(parent.Members, added)
	return nil
}
