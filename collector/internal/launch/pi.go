package launch

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/pi"
)

var ErrPiUnsupportedVersion = errors.New("launch: Pi requires a stable release at least " + pi.MinimumRuntimeVersion)
var ErrPiExtension = errors.New("launch: managed Pi extension is unavailable")

func piSupportedExecutable() (string, error) {
	if !vendors.PiSupported() {
		return "", errors.New("launch: Pi support requires macOS")
	}
	cli, err := vendors.PiExecutable()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	version, err := exec.CommandContext(ctx, cli, "--version").Output()
	if err != nil || !pi.RuntimeSupported(strings.TrimSpace(string(version))) {
		return "", ErrPiUnsupportedVersion
	}
	return cli, nil
}
func piExecutable() (string, error) {
	cli, err := piSupportedExecutable()
	if err != nil {
		return "", err
	}
	if err := pi.EnsureExtension(); err != nil {
		return "", fmt.Errorf("%w: %v", ErrPiExtension, err)
	}
	return cli, nil
}
func validatePiTranscript(path, id, cwd string) error {
	if !filepath.IsAbs(path) {
		return errors.New("launch: Pi transcript path must be absolute")
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("launch: Pi transcript: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("launch: Pi transcript is not a readable regular file")
	}
	line, err := bufio.NewReader(io.LimitReader(file, 64*1024)).ReadBytes('\n')
	var header struct {
		Type    string `json:"type"`
		Version int    `json:"version"`
		ID      string `json:"id"`
		CWD     string `json:"cwd"`
	}
	if (err != nil && len(line) == 0) || json.Unmarshal(line, &header) != nil || header.Type != "session" || header.Version != 3 || header.ID != id || filepath.Clean(header.CWD) != filepath.Clean(cwd) {
		return errors.New("launch: Pi transcript identity, schema, or working directory changed")
	}
	return nil
}

// GUI terminals may already be running with a different environment.
func piCommand(cli string, arguments ...string) (string, error) {
	extension, err := pi.ExtensionPath()
	if err != nil {
		return "", err
	}
	stateHome, err := filepath.Abs(settings.Home())
	if err != nil {
		return "", err
	}
	cli, err = filepath.Abs(cli)
	if err != nil {
		return "", err
	}
	environment := []string{"COSLASH_HOME=" + stateHome, "PI_CODING_AGENT_DIR=" + filepath.Dir(filepath.Dir(extension))}
	if root := os.Getenv("PI_CODING_AGENT_SESSION_DIR"); root != "" {
		root, err = pi.ResolveDirectory(root)
		if err != nil {
			return "", err
		}
		environment = append(environment, "PI_CODING_AGENT_SESSION_DIR="+root)
	}
	return localPiCommand(cli, extension, environment, arguments), nil
}

func piNewCommand(cli, handoff, prompt string) (string, string, error) {
	if len(handoff) > MaxHandoffBytes {
		return "", "", errors.New("launch: handoff context exceeds size limit")
	}
	context := ""
	if handoff != "" {
		context = handoffPreamble + handoff
	}
	return localPiNewCommand(cli, context, prompt)
}
func localHandoffScript(directory, command, path string, shells ...string) string {
	shell := os.Getenv("SHELL")
	if len(shells) > 0 {
		shell = shells[0]
	}
	if filepath.Base(shell) == "fish" {
		failed := `coslash_cd_status=$1; rm -f ` + shellQuote(path) + " " + shellQuote(path+".context") + `; exit "$coslash_cd_status"`
		return "if cd " + shellQuote(directory) + "; " + withCleanup(command, path) + "; else; " + shellJoin("/bin/sh", "-c", failed, "sh") + ` "$status"; end`
	}
	return "if cd " + shellQuote(directory) + "; then " + withCleanup(command, path) + "; else ( coslash_cd_status=$?; rm -f " + shellQuote(path) + " " + shellQuote(path+".context") + "; exit \"$coslash_cd_status\" ); fi"
}

func PiAvailable() bool { _, err := piSupportedExecutable(); return err == nil }
