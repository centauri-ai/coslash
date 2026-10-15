package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/settings"
)

const protocolHandlerTestHomeOverride = "COSLASH_TEST_ALLOW_TEMP_HOME_PROTOCOL_REGISTRATION"

type protocolHandlerRecord struct {
	Executable string `json:"executable"`
	Path       string `json:"path"`
}

type protocolHandlerRegistration struct {
	Registered bool
	Executable string
	Path       string
}

type protocolHandlerBackend interface {
	Register(executable, home string, previous protocolHandlerRecord) (string, error)
	Status(executable, home string) (protocolHandlerRegistration, error)
	StaleHandlers(executable, home string) ([]string, error)
	Unregister(path string) error
}

var activeProtocolHandlerBackend protocolHandlerBackend = newProtocolHandlerBackend()

func usesDefaultHome() bool {
	home, err := os.UserHomeDir()
	return err == nil && sameProtocolHandlerPath(settings.Home(), filepath.Join(home, ".coslash"))
}

func ensureProtocolHandler() error {
	if !usesDefaultHome() {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if homeIsUnderTempDir(home, os.TempDir()) && os.Getenv(protocolHandlerTestHomeOverride) != "1" {
		return nil
	}
	executable, err := currentExecutablePath()
	if err != nil {
		return err
	}
	registration, err := activeProtocolHandlerBackend.Status(executable, home)
	if err != nil {
		return err
	}
	if registration.Registered {
		return nil
	}
	return registerProtocolHandler()
}

func registerProtocolHandler() error {
	if !usesDefaultHome() {
		return fmt.Errorf("URL handler installation requires the default COSLASH_HOME")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("find user home for protocol handler: %w", err)
	}
	if homeIsUnderTempDir(home, os.TempDir()) && os.Getenv(protocolHandlerTestHomeOverride) != "1" {
		return fmt.Errorf("refusing to register a protocol handler for temporary HOME %q; set %s=1 only in an isolated test", home, protocolHandlerTestHomeOverride)
	}
	executable, err := currentExecutablePath()
	if err != nil {
		return err
	}
	previous, err := readProtocolHandlerRecord()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read protocol handler record: %w", err)
	}
	path, err := activeProtocolHandlerBackend.Register(executable, home, previous)
	if err != nil {
		return err
	}
	return writeProtocolHandlerRecord(protocolHandlerRecord{Executable: executable, Path: path})
}

func protocolHandlerState() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "not registered"
	}
	executable, err := currentExecutablePath()
	if err != nil {
		return "not registered"
	}
	registration, err := activeProtocolHandlerBackend.Status(executable, home)
	if err != nil || !registration.Registered {
		return "not registered"
	}
	if sameExecutablePath(registration.Executable, executable) {
		return "registered (this copy)"
	}
	path := registration.Executable
	if path == "" {
		path = registration.Path
	}
	if path == "" {
		return "not registered"
	}
	return "registered to another copy at " + path
}

func currentExecutablePath() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("find current executable: %w", err)
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return "", fmt.Errorf("resolve current executable: %w", err)
	}
	return filepath.Clean(executable), nil
}

func sameExecutablePath(left, right string) bool {
	if left == "" || right == "" {
		return false
	}
	left, leftErr := filepath.Abs(left)
	right, rightErr := filepath.Abs(right)
	if leftErr == nil && rightErr == nil {
		left, right = filepath.Clean(left), filepath.Clean(right)
	}
	return strings.EqualFold(left, right) && (os.PathSeparator == '\\' || left == right)
}

func sameProtocolHandlerPath(left, right string) bool {
	return canonicalPath(left) == canonicalPath(right)
}

func unregisterPreviousProtocolHandler(previousPath, currentPath string, unregister func(string) error) error {
	if previousPath == "" || sameProtocolHandlerPath(previousPath, currentPath) {
		return nil
	}
	return unregister(previousPath)
}

func homeIsUnderTempDir(home, tempDir string) bool {
	home = canonicalPath(home)
	tempDir = canonicalPath(tempDir)
	relative, err := filepath.Rel(tempDir, home)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)))
}

func canonicalPath(value string) string {
	absolute, err := filepath.Abs(value)
	if err == nil {
		value = absolute
	}
	if resolved, err := filepath.EvalSymlinks(value); err == nil {
		value = resolved
	}
	return filepath.Clean(value)
}

func protocolHandlerRecordPath() string {
	return filepath.Join(settings.Home(), "protocol-handler.json")
}

func readProtocolHandlerRecord() (protocolHandlerRecord, error) {
	contents, err := os.ReadFile(protocolHandlerRecordPath())
	if err != nil {
		return protocolHandlerRecord{}, err
	}
	var record protocolHandlerRecord
	if err := json.Unmarshal(contents, &record); err != nil {
		return protocolHandlerRecord{}, err
	}
	return record, nil
}

func writeProtocolHandlerRecord(record protocolHandlerRecord) error {
	contents, err := json.Marshal(record)
	if err != nil {
		return err
	}
	path := protocolHandlerRecordPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create protocol handler state directory: %w", err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		return fmt.Errorf("write protocol handler record: %w", err)
	}
	return nil
}

func planProtocolHandlerCleanup(stdout, stderr io.Writer, apply bool) int {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(stderr, "inspect protocol handlers: %v\n", err)
		return 2
	}
	executable, err := currentExecutablePath()
	if err != nil {
		fmt.Fprintf(stderr, "inspect protocol handlers: %v\n", err)
		return 2
	}
	paths, err := activeProtocolHandlerBackend.StaleHandlers(executable, home)
	if err != nil {
		fmt.Fprintf(stderr, "inspect protocol handlers: %v\n", err)
		return 2
	}
	if len(paths) == 0 {
		fmt.Fprintln(stdout, "No stale coSlash URL handlers found.")
		return 0
	}
	for _, path := range paths {
		fmt.Fprintf(stdout, "Will unregister: %s\n", path)
	}
	if !apply {
		fmt.Fprintln(stdout, "No changes made. Rerun `coslash doctor --fix-links --yes` to unregister these handlers.")
		return 0
	}
	for _, path := range paths {
		if err := activeProtocolHandlerBackend.Unregister(path); err != nil {
			fmt.Fprintf(stderr, "unregister %q: %v\n", path, err)
			return 1
		}
		fmt.Fprintf(stdout, "Unregistered: %s\n", path)
	}
	return 0
}
