package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMCPConfigPreservesOtherServersAndHasNoCredential(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(path, []byte(`{"mcpServers":{"other":{"url":"https://other.example/mcp"}},"unrelated":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeMCPConfig(path, "mcpServers", mcpDefaultURL, false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value struct {
		Servers   map[string]map[string]string `json:"mcpServers"`
		Unrelated bool                         `json:"unrelated"`
	}
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	if !value.Unrelated || value.Servers["other"]["url"] != "https://other.example/mcp" || value.Servers[mcpServerName]["url"] != mcpDefaultURL {
		t.Fatalf("unexpected MCP config: %s", data)
	}
	if strings.Contains(string(data), "Bearer") || strings.Contains(string(data), "token") {
		t.Fatalf("credential text in MCP config: %s", data)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("MCP config mode: %v %v", info, err)
		}
	}
	if err := writeMCPConfig(path, "mcpServers", "https://other.example/mcp", false); err == nil {
		t.Fatal("setup replaced an existing coSlash endpoint")
	}
}

func TestMCPSetupRejectsUnsafeURLsAndSymlinkConfig(t *testing.T) {
	for _, endpoint := range []string{
		"http://example.com/mcp", "https://user:pass@example.com/mcp", "https://example.com/mcp?token=x", "https://example.com/other",
	} {
		if err := validateMCPEndpoint(endpoint); err == nil {
			t.Errorf("accepted %q", endpoint)
		}
	}
	if err := validateMCPEndpoint("http://127.0.0.1:8080/mcp"); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "mcp.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := writeMCPConfig(link, "mcpServers", mcpDefaultURL, false); err == nil {
		t.Fatal("setup followed an MCP config symlink")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "keep" {
		t.Fatalf("target changed: %q %v", data, err)
	}
}

func TestMCPSwitchClearsNativeClientBeforePrompting(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX test executable")
	}
	root := t.TempDir()
	commandLog := filepath.Join(root, "argv")
	for _, executable := range []string{"claude", "codex", "opencode"} {
		path := filepath.Join(root, executable)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s ' \"$@\" >> \"$MCP_TEST_LOG\"\nprintf '\\n' >> \"$MCP_TEST_LOG\"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", root)
	t.Setenv("MCP_TEST_LOG", commandLog)
	for _, agent := range []string{"claude", "codex", "opencode"} {
		var stdout bytes.Buffer
		if code := runCLI(&stdout, &stdout, []string{"mcp", "switch", agent}); code != 0 {
			t.Fatalf("switch %s: %s", agent, stdout.String())
		}
		if !strings.Contains(stdout.String(), "Switch the coSlash account in your browser") {
			t.Fatalf("missing account-switch instruction: %s", stdout.String())
		}
	}
	data, err := os.ReadFile(commandLog)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "mcp logout coslash \nmcp logout coslash \nmcp logout coslash \n" {
		t.Fatalf("unexpected native logout commands: %q", data)
	}
	var stdout bytes.Buffer
	if code := runCLI(&stdout, &stdout, []string{"mcp", "switch", "cursor"}); code == 0 || !strings.Contains(stdout.String(), "Cursor does not expose MCP credential logout") {
		t.Fatalf("Cursor switch should require explicit credential cleanup: %s", stdout.String())
	}
}

func TestMCPSetupAndLoginInvokeAgentsWithoutCredentialArguments(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX test executable")
	}
	root := t.TempDir()
	commandLog := filepath.Join(root, "argv")
	for _, executable := range []string{"claude", "codex", "opencode", "cursor-agent"} {
		path := filepath.Join(root, executable)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s ' \"$@\" >> \"$MCP_TEST_LOG\"\nprintf '\\n' >> \"$MCP_TEST_LOG\"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", root)
	t.Setenv("MCP_TEST_LOG", commandLog)
	for _, agent := range []string{"claude", "codex"} {
		var stdout bytes.Buffer
		if code := runCLI(&stdout, &stdout, []string{"mcp", "setup", agent}); code != 0 {
			t.Fatalf("setup %s: %s", agent, stdout.String())
		}
	}
	for _, agent := range []string{"claude", "codex", "opencode", "cursor"} {
		var stdout bytes.Buffer
		stdout.Reset()
		if code := runCLI(&stdout, &stdout, []string{"mcp", "login", agent}); code != 0 {
			t.Fatalf("login %s: %s", agent, stdout.String())
		}
	}
	data, err := os.ReadFile(commandLog)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"mcp add --transport http --scope user coslash " + mcpDefaultURL,
		"mcp add coslash --url " + mcpDefaultURL + " --oauth-client-registration dcr --oauth-resource " + mcpDefaultURL,
		"mcp login coslash", "mcp auth coslash",
	} {
		if !strings.Contains(string(data), expected) {
			t.Errorf("missing %q in %q", expected, data)
		}
	}
	if strings.Contains(string(data), "Bearer") || strings.Contains(string(data), "access_token") {
		t.Fatalf("credential in agent argv: %q", data)
	}
}
