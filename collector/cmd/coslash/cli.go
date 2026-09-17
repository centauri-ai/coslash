package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"golang.org/x/sys/unix"
)

const runtimeFilename = "runtime.json"

type runtimeDescriptor struct {
	BaseURL string `json:"baseURL"`
}

func acquireRuntimeLock() (*os.File, error) {
	home := settings.Home()
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(home, "runtime.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		file.Close()
		return nil, errors.New("another coSlash app is already running")
	}
	return file, nil
}

func writeRuntime(baseURL string) error {
	home := settings.Home()
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(runtimeDescriptor{BaseURL: baseURL})
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(home, ".runtime-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(append(data, '\n')); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, filepath.Join(home, runtimeFilename))
}

func readRuntime() (string, string, error) {
	home := settings.Home()
	data, err := os.ReadFile(filepath.Join(home, runtimeFilename))
	if err != nil {
		return "", "", fmt.Errorf("coSlash app is not running; start it and retry")
	}
	var runtime runtimeDescriptor
	if err := json.Unmarshal(data, &runtime); err != nil {
		return "", "", fmt.Errorf("invalid coSlash runtime descriptor; restart the app")
	}
	parsed, err := url.Parse(runtime.BaseURL)
	if err != nil || parsed.Scheme != "http" || !loopbackHost(parsed.Hostname()) {
		return "", "", fmt.Errorf("invalid coSlash runtime URL; restart the app")
	}
	token, err := os.ReadFile(filepath.Join(home, "token"))
	if err != nil || strings.TrimSpace(string(token)) == "" {
		return "", "", fmt.Errorf("coSlash authentication is unavailable; restart the app")
	}
	return strings.TrimRight(runtime.BaseURL, "/"), strings.TrimSpace(string(token)), nil
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

type localAPIClient struct {
	baseURL string
	token   string
	client  *http.Client
}

func newLocalAPIClient() (*localAPIClient, error) {
	baseURL, token, err := readRuntime()
	if err != nil {
		return nil, err
	}
	return &localAPIClient{
		baseURL: baseURL,
		token:   token,
		client:  &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (client *localAPIClient) request(method, path string, body io.Reader) ([]byte, error) {
	request, err := http.NewRequest(method, client.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("X-Coslash-Token", client.token)
	response, err := client.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("coSlash app is not reachable; start it and retry")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("read coSlash response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message := strings.TrimSpace(string(data))
		var apiError apiErrorBody
		if json.Unmarshal(data, &apiError) == nil && apiError.Error != "" {
			message = apiError.Error
		}
		if message == "" {
			message = response.Status
		}
		return nil, errors.New(message)
	}
	return data, nil
}

func runCLI(stdout, stderr io.Writer, args []string) int {
	if len(args) == 0 {
		return 2
	}
	var err error
	switch args[0] {
	case "sessions":
		err = runSessions(stdout, args[1:])
	case "handoff":
		err = runHandoff(stdout, args[1:])
	case "send":
		err = runSend(stdout, args[1:])
	case "doctor":
		return runDoctor(stdout, stderr, args[1:])
	default:
		return 2
	}
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	return 0
}

func runSessions(stdout io.Writer, args []string) error {
	jsonOutput := false
	query := ""
	for _, argument := range args {
		switch {
		case argument == "--json":
			jsonOutput = true
		case strings.HasPrefix(argument, "-"):
			return fmt.Errorf("usage: coslash sessions [query] --json")
		case query == "":
			query = argument
		default:
			return fmt.Errorf("usage: coslash sessions [query] --json")
		}
	}
	if !jsonOutput {
		return fmt.Errorf("usage: coslash sessions [query] --json")
	}
	client, err := newLocalAPIClient()
	if err != nil {
		return err
	}
	data, err := client.request(http.MethodGet, "/api/sessions", nil)
	if err != nil {
		return err
	}
	var sessions []session.Session
	if err := json.Unmarshal(data, &sessions); err != nil {
		return fmt.Errorf("decode sessions: %w", err)
	}
	filtered := make([]cliSession, 0, len(sessions))
	for _, value := range sessions {
		if sessionMatches(value, query) {
			filtered = append(filtered, cliSession{
				ID: value.ID, Name: value.Name, Agent: value.Agent, Status: value.Status,
				Repository: value.Repository, Branch: value.Branch, LastActivityTime: value.LastActivityTime,
			})
		}
	}
	return json.NewEncoder(stdout).Encode(filtered)
}

type cliSession struct {
	ID               string  `json:"id"`
	Name             *string `json:"name"`
	Agent            string  `json:"agent"`
	Status           *string `json:"status"`
	Repository       *string `json:"repo"`
	Branch           *string `json:"branch"`
	LastActivityTime int64   `json:"mtime"`
}

func sessionMatches(value session.Session, query string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return true
	}
	fields := []*string{value.Name, value.Repository, value.Branch}
	for _, field := range fields {
		if field != nil && strings.Contains(strings.ToLower(*field), query) {
			return true
		}
	}
	return strings.Contains(strings.ToLower(value.Agent), query)
}

func runHandoff(stdout io.Writer, args []string) error {
	if len(args) != 1 || args[0] == "" {
		return fmt.Errorf("usage: coslash handoff <session>")
	}
	client, err := newLocalAPIClient()
	if err != nil {
		return err
	}
	data, err := client.request(http.MethodGet, "/api/handoff?id="+url.QueryEscape(args[0]), nil)
	if err != nil {
		return err
	}
	if _, err := stdout.Write(data); err != nil {
		return err
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		_, err = fmt.Fprintln(stdout)
	}
	return err
}

func runSend(stdout io.Writer, args []string) error {
	if len(args) < 3 || args[0] == "" || args[1] != "--to" {
		return fmt.Errorf("usage: coslash send <session> --to claude|codex [message]")
	}
	target := args[2]
	if target != "claude" && target != "codex" {
		return fmt.Errorf("--to must be claude or codex")
	}
	message := strings.Join(args[3:], " ")
	client, err := newLocalAPIClient()
	if err != nil {
		return err
	}
	path := "/api/send?id=" + url.QueryEscape(args[0]) + "&to=" + url.QueryEscape(target)
	if _, err := client.request(http.MethodPost, path, bytes.NewBufferString(message)); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Success: started %s for session %s\n", target, args[0])
	return nil
}
