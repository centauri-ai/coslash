package main

import (
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
	"strings"
	"time"

	"github.com/tailscale/hujson"
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
		if strings.HasSuffix(path, ".jsonc") {
			return writeMCPJSONCConfig(path, endpoint)
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

func writeMCPConfig(path, key, endpoint string, opencode bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	root := map[string]json.RawMessage{}
	info, err := os.Lstat(path)
	if err == nil {
		if !info.Mode().IsRegular() || info.Size() > 1<<20 {
			return errors.New("MCP config must be a regular file under 1 MiB")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if json.Unmarshal(data, &root) != nil || root == nil {
			return errors.New("MCP config is not a supported JSON object")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	servers := map[string]json.RawMessage{}
	if existing, ok := root[key]; ok {
		if json.Unmarshal(existing, &servers) != nil || servers == nil {
			return errors.New("MCP server settings are not a JSON object")
		}
	}
	var server any = map[string]string{"url": endpoint}
	if opencode {
		server = map[string]any{"type": "remote", "url": endpoint, "enabled": true, "oauth": map[string]string{"scope": "mcp:read mcp:ask"}}
	}
	encodedServer, _ := json.Marshal(server)
	if existing, ok := servers[mcpServerName]; ok {
		var previous, desired any
		if json.Unmarshal(existing, &previous) != nil || json.Unmarshal(encodedServer, &desired) != nil || !reflect.DeepEqual(previous, desired) {
			return errors.New("coSlash MCP already has different settings; review that entry before setup")
		}
		return nil
	}
	servers[mcpServerName] = encodedServer
	root[key], _ = json.Marshal(servers)
	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	return replaceMCPConfig(path, append(data, '\n'))
}

func writeMCPJSONCConfig(path, endpoint string) error {
	info, err := os.Lstat(path)
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
	if err != nil {
		return fmt.Errorf("OpenCode config is not valid JSONC: %w", err)
	}
	object, ok := root.Value.(*hujson.Object)
	if !ok {
		return errors.New("OpenCode config is not a JSONC object")
	}
	server := map[string]any{"type": "remote", "url": endpoint, "enabled": true, "oauth": map[string]string{"scope": "mcp:read mcp:ask"}}
	encodedServer, _ := json.Marshal(server)
	member, err := hujson.Parse([]byte(`{"coslash":` + string(encodedServer) + `}`))
	if err != nil {
		return err
	}
	serverMember := member.Value.(*hujson.Object).Members[0]
	settings := root.Find("/mcp")
	if settings == nil {
		member, err = hujson.Parse([]byte(`{"mcp":{"coslash":` + string(encodedServer) + `}}`))
		if err != nil {
			return err
		}
		object.Members = append(object.Members, member.Value.(*hujson.Object).Members[0])
	} else {
		servers, ok := settings.Value.(*hujson.Object)
		if !ok {
			return errors.New("MCP server settings are not a JSON object")
		}
		if existing := root.Find("/mcp/coslash"); existing != nil {
			previous := existing.Clone()
			previous.Standardize()
			var previousValue, desiredValue any
			if json.Unmarshal(previous.Pack(), &previousValue) != nil || json.Unmarshal(encodedServer, &desiredValue) != nil || !reflect.DeepEqual(previousValue, desiredValue) {
				return errors.New("coSlash MCP already has different settings; review that entry before setup")
			}
			return nil
		}
		servers.Members = append(servers.Members, serverMember)
	}
	root.Format()
	return replaceMCPConfig(path, root.Pack())
}

func replaceMCPConfig(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".mcp-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
