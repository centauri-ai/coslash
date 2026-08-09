package terminal

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/agentexec"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/contracts"
	"github.com/creack/pty"
)

const (
	DefaultCapacity = 128
	DefaultCols     = 120
	DefaultRows     = 40
	MinCols         = 20
	MaxCols         = 500
	MinRows         = 5
	MaxRows         = 200
	MaxInputBytes   = 32 << 10
	MaxPasteBytes   = 1 << 20
)

var (
	idPattern        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	tmuxNamePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,79}$`)
	namespacePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,23}$`)

	ErrNotFound = errors.New("terminal not found")
	ErrReadOnly = errors.New("terminal is read-only")
	ErrClosed   = errors.New("terminal manager is closed")
)

type Spec struct {
	ID              string
	TmuxName        string
	Command         agentexec.Command
	Writable        bool
	PreserveOnClose bool
	Cols            uint16
	Rows            uint16
}

type Runner interface {
	Run(context.Context, io.Reader, string, []string, string, []string) error
}

type PTY interface {
	io.ReadWriteCloser
	Resize(uint16, uint16) error
}

type PTYFactory interface {
	Start(context.Context, string, []string, string, []string, uint16, uint16) (PTY, error)
}

type Options struct {
	Capacity   int
	Runner     Runner
	PTYFactory PTYFactory
}

type Manager struct {
	mu         sync.Mutex
	sessions   map[string]*entry
	capacity   int
	runner     Runner
	ptyFactory PTYFactory
	closed     bool
}

type entry struct {
	id       string
	tmuxName string
	cwd      string
	writable bool
	preserve bool
	clients  map[*Client]struct{}
}

type Client struct {
	manager   *Manager
	entry     *entry
	pty       PTY
	writable  bool
	writeMu   sync.Mutex
	closeOnce sync.Once
}

func New(options Options) *Manager {
	capacity := options.Capacity
	if capacity <= 0 {
		capacity = DefaultCapacity
	}
	runner := options.Runner
	if runner == nil {
		runner = osRunner{}
	}
	factory := options.PTYFactory
	if factory == nil {
		factory = osPTYFactory{}
	}
	return &Manager{sessions: map[string]*entry{}, capacity: capacity, runner: runner, ptyFactory: factory}
}

func Name(namespace, identity string) (string, error) {
	if !namespacePattern.MatchString(namespace) || identity == "" || len(identity) > 4096 || strings.ContainsRune(identity, 0) {
		return "", errors.New("terminal: invalid tmux name input")
	}
	digest := sha256.Sum256([]byte(identity))
	return fmt.Sprintf("coslash_%s_%x", namespace, digest[:8]), nil
}

func (manager *Manager) Create(ctx context.Context, spec Spec) (contracts.TerminalStatus, error) {
	if !idPattern.MatchString(spec.ID) {
		return contracts.TerminalStatus{}, errors.New("terminal: invalid terminal id")
	}
	if !tmuxNamePattern.MatchString(spec.TmuxName) {
		return contracts.TerminalStatus{}, errors.New("terminal: invalid tmux name")
	}
	if spec.Command.Path != "claude" && spec.Command.Path != "codex" {
		return contracts.TerminalStatus{}, errors.New("terminal: unsupported program")
	}
	cwd, err := canonicalDirectory(spec.Command.Dir)
	if err != nil {
		return contracts.TerminalStatus{}, err
	}
	cols, rows, err := dimensions(spec.Cols, spec.Rows)
	if err != nil {
		return contracts.TerminalStatus{}, err
	}

	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return contracts.TerminalStatus{}, ErrClosed
	}
	if existing := manager.sessions[spec.ID]; existing != nil {
		if existing.tmuxName != spec.TmuxName {
			return contracts.TerminalStatus{}, errors.New("terminal: terminal id is already bound")
		}
		return statusOf(existing, "running"), nil
	}
	if len(manager.sessions) >= manager.capacity {
		return contracts.TerminalStatus{}, errors.New("terminal: registry capacity reached")
	}
	if manager.hasSession(ctx, spec.TmuxName) {
		return contracts.TerminalStatus{}, errors.New("terminal: tmux session already exists")
	}
	args := []string{"new-session", "-d", "-s", spec.TmuxName, "-x", fmt.Sprint(cols), "-y", fmt.Sprint(rows), "-c", cwd}
	for _, value := range spec.Command.Env {
		args = append(args, "-e", value)
	}
	args = append(args, "--", spec.Command.Path)
	args = append(args, spec.Command.Args...)
	if err := manager.runner.Run(ctx, nil, "tmux", args, "", nil); err != nil {
		return contracts.TerminalStatus{}, errors.New("terminal: could not create tmux session")
	}
	if spec.Command.Stdin != "" {
		buffer := spec.TmuxName + "_prompt"
		if err := manager.runner.Run(ctx, strings.NewReader(spec.Command.Stdin), "tmux", []string{"load-buffer", "-b", buffer, "-"}, "", nil); err != nil ||
			manager.runner.Run(ctx, nil, "tmux", []string{"paste-buffer", "-t", "=" + spec.TmuxName, "-b", buffer, "-d"}, "", nil) != nil ||
			manager.runner.Run(ctx, nil, "tmux", []string{"send-keys", "-t", "=" + spec.TmuxName, "C-d"}, "", nil) != nil {
			_ = manager.runner.Run(ctx, nil, "tmux", []string{"kill-session", "-t", "=" + spec.TmuxName}, "", nil)
			return contracts.TerminalStatus{}, errors.New("terminal: could not deliver agent input")
		}
	}
	_ = manager.runner.Run(ctx, nil, "tmux", []string{"set-option", "-t", "=" + spec.TmuxName, "mouse", "on"}, "", nil)
	_ = manager.runner.Run(ctx, nil, "tmux", []string{"set-option", "-t", "=" + spec.TmuxName, "history-limit", "50000"}, "", nil)
	created := &entry{id: spec.ID, tmuxName: spec.TmuxName, cwd: cwd, writable: spec.Writable, preserve: spec.PreserveOnClose, clients: map[*Client]struct{}{}}
	manager.sessions[spec.ID] = created
	return statusOf(created, "running"), nil
}

func (manager *Manager) Adopt(ctx context.Context, id, tmuxName, cwd string, writable, preserve bool) (contracts.TerminalStatus, error) {
	if !idPattern.MatchString(id) || !tmuxNamePattern.MatchString(tmuxName) {
		return contracts.TerminalStatus{}, errors.New("terminal: invalid identity")
	}
	resolved, err := canonicalDirectory(cwd)
	if err != nil {
		return contracts.TerminalStatus{}, err
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return contracts.TerminalStatus{}, ErrClosed
	}
	if existing := manager.sessions[id]; existing != nil {
		if existing.tmuxName != tmuxName {
			return contracts.TerminalStatus{}, errors.New("terminal: terminal id is already bound")
		}
		return statusOf(existing, "running"), nil
	}
	if len(manager.sessions) >= manager.capacity {
		return contracts.TerminalStatus{}, errors.New("terminal: registry capacity reached")
	}
	if !manager.hasSession(ctx, tmuxName) {
		return contracts.TerminalStatus{}, ErrNotFound
	}
	created := &entry{id: id, tmuxName: tmuxName, cwd: resolved, writable: writable, preserve: preserve, clients: map[*Client]struct{}{}}
	manager.sessions[id] = created
	return statusOf(created, "running"), nil
}

func (manager *Manager) Status(ctx context.Context, id string) (contracts.TerminalStatus, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	current := manager.sessions[id]
	if current == nil {
		return contracts.TerminalStatus{}, ErrNotFound
	}
	if !manager.hasSession(ctx, current.tmuxName) {
		return statusOf(current, "exited"), nil
	}
	return statusOf(current, "running"), nil
}

func (manager *Manager) Attach(ctx context.Context, id string, cols, rows uint16) (*Client, error) {
	cols, rows, err := dimensions(cols, rows)
	if err != nil {
		return nil, err
	}
	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		return nil, ErrClosed
	}
	current := manager.sessions[id]
	if current == nil {
		manager.mu.Unlock()
		return nil, ErrNotFound
	}
	manager.mu.Unlock()
	process, err := manager.ptyFactory.Start(ctx, "tmux", []string{"attach-session", "-t", "=" + current.tmuxName}, current.cwd, os.Environ(), cols, rows)
	if err != nil {
		return nil, errors.New("terminal: could not attach PTY")
	}
	client := &Client{manager: manager, entry: current, pty: process, writable: current.writable}
	manager.mu.Lock()
	if manager.closed || manager.sessions[id] != current {
		manager.mu.Unlock()
		_ = process.Close()
		return nil, ErrClosed
	}
	current.clients[client] = struct{}{}
	manager.mu.Unlock()
	return client, nil
}

func (manager *Manager) Input(ctx context.Context, id, data string) error {
	if len(data) == 0 || len(data) > MaxInputBytes || strings.ContainsRune(data, 0) {
		return errors.New("terminal: input is invalid or too large")
	}
	manager.mu.Lock()
	current := manager.sessions[id]
	if current == nil {
		manager.mu.Unlock()
		return ErrNotFound
	}
	writable := current.writable
	name := current.tmuxName
	manager.mu.Unlock()
	if !writable {
		return ErrReadOnly
	}
	if err := manager.runner.Run(ctx, nil, "tmux", []string{"send-keys", "-t", "=" + name, "-l", "--", data}, "", nil); err != nil {
		return errors.New("terminal: input delivery failed")
	}
	return nil
}

func (manager *Manager) Paste(ctx context.Context, id, text string) error {
	if len(text) == 0 || len(text) > MaxPasteBytes || strings.ContainsRune(text, 0) {
		return errors.New("terminal: paste is invalid or too large")
	}
	manager.mu.Lock()
	current := manager.sessions[id]
	if current == nil {
		manager.mu.Unlock()
		return ErrNotFound
	}
	if !current.writable {
		manager.mu.Unlock()
		return ErrReadOnly
	}
	name := current.tmuxName
	manager.mu.Unlock()
	buffer := name + "_note"
	if err := manager.runner.Run(ctx, strings.NewReader(text), "tmux", []string{"load-buffer", "-b", buffer, "-"}, "", nil); err != nil {
		return errors.New("terminal: paste delivery failed")
	}
	if err := manager.runner.Run(ctx, nil, "tmux", []string{"paste-buffer", "-t", "=" + name, "-b", buffer, "-p", "-d"}, "", nil); err != nil {
		return errors.New("terminal: paste delivery failed")
	}
	if err := manager.runner.Run(ctx, nil, "tmux", []string{"send-keys", "-t", "=" + name, "Enter"}, "", nil); err != nil {
		return errors.New("terminal: paste delivery failed")
	}
	return nil
}

func (manager *Manager) Stop(ctx context.Context, id string) error {
	manager.mu.Lock()
	current := manager.sessions[id]
	if current == nil {
		manager.mu.Unlock()
		return ErrNotFound
	}
	delete(manager.sessions, id)
	clients := clientsOf(current)
	manager.mu.Unlock()
	for _, client := range clients {
		_ = client.Close()
	}
	_ = manager.runner.Run(ctx, nil, "tmux", []string{"kill-session", "-t", "=" + current.tmuxName}, "", nil)
	return nil
}

func (manager *Manager) Close(ctx context.Context) error {
	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		return nil
	}
	manager.closed = true
	type closingEntry struct {
		entry   *entry
		clients []*Client
	}
	sessions := make([]closingEntry, 0, len(manager.sessions))
	for _, current := range manager.sessions {
		sessions = append(sessions, closingEntry{entry: current, clients: clientsOf(current)})
	}
	manager.sessions = map[string]*entry{}
	manager.mu.Unlock()
	for _, closing := range sessions {
		for _, client := range closing.clients {
			_ = client.Close()
		}
		if !closing.entry.preserve {
			_ = manager.runner.Run(ctx, nil, "tmux", []string{"kill-session", "-t", "=" + closing.entry.tmuxName}, "", nil)
		}
	}
	return nil
}

func (client *Client) Read(data []byte) (int, error) {
	return client.pty.Read(data)
}

func (client *Client) Write(data []byte) (int, error) {
	if !client.writable {
		return 0, ErrReadOnly
	}
	if len(data) == 0 || len(data) > MaxInputBytes {
		return 0, errors.New("terminal: input is invalid or too large")
	}
	client.writeMu.Lock()
	defer client.writeMu.Unlock()
	return client.pty.Write(data)
}

func (client *Client) Resize(cols, rows uint16) error {
	cols, rows, err := dimensions(cols, rows)
	if err != nil {
		return err
	}
	return client.pty.Resize(cols, rows)
}

func (client *Client) Close() error {
	var err error
	client.closeOnce.Do(func() {
		err = client.pty.Close()
		client.manager.mu.Lock()
		delete(client.entry.clients, client)
		client.manager.mu.Unlock()
	})
	return err
}

func statusOf(current *entry, state string) contracts.TerminalStatus {
	return contracts.TerminalStatus{TerminalID: current.id, State: state, Writable: current.writable}
}

func clientsOf(current *entry) []*Client {
	clients := make([]*Client, 0, len(current.clients))
	for client := range current.clients {
		clients = append(clients, client)
	}
	return clients
}

func dimensions(cols, rows uint16) (uint16, uint16, error) {
	if cols == 0 {
		cols = DefaultCols
	}
	if rows == 0 {
		rows = DefaultRows
	}
	if cols < MinCols || cols > MaxCols || rows < MinRows || rows > MaxRows {
		return 0, 0, errors.New("terminal: dimensions are outside the allowed range")
	}
	return cols, rows, nil
}

func canonicalDirectory(path string) (string, error) {
	if path == "" {
		return "", errors.New("terminal: working directory is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", errors.New("terminal: could not resolve working directory")
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", errors.New("terminal: working directory is unavailable")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", errors.New("terminal: working directory is not a directory")
	}
	return resolved, nil
}

func (manager *Manager) hasSession(ctx context.Context, name string) bool {
	return manager.runner.Run(ctx, nil, "tmux", []string{"has-session", "-t", "=" + name}, "", nil) == nil
}

type osRunner struct{}

func (osRunner) Run(ctx context.Context, stdin io.Reader, name string, args []string, dir string, env []string) error {
	command := exec.CommandContext(ctx, name, args...)
	command.Stdin = stdin
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	command.Dir = dir
	if env != nil {
		command.Env = env
	}
	return command.Run()
}

type osPTYFactory struct{}

func (osPTYFactory) Start(ctx context.Context, name string, args []string, dir string, env []string, cols, rows uint16) (PTY, error) {
	processCtx, cancel := context.WithCancel(ctx)
	command := exec.CommandContext(processCtx, name, args...)
	command.Dir = dir
	command.Env = env
	file, err := pty.StartWithSize(command, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		cancel()
		return nil, err
	}
	return &processPTY{file: file, command: command, cancel: cancel}, nil
}

type processPTY struct {
	file      *os.File
	command   *exec.Cmd
	cancel    context.CancelFunc
	closeOnce sync.Once
}

func (process *processPTY) Read(data []byte) (int, error)  { return process.file.Read(data) }
func (process *processPTY) Write(data []byte) (int, error) { return process.file.Write(data) }
func (process *processPTY) Resize(cols, rows uint16) error {
	return pty.Setsize(process.file, &pty.Winsize{Cols: cols, Rows: rows})
}
func (process *processPTY) Close() error {
	var err error
	process.closeOnce.Do(func() {
		process.cancel()
		err = process.file.Close()
		_ = process.command.Wait()
	})
	return err
}
