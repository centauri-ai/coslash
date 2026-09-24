package synthesis

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

const openCodeConfigContent = `{"permission":"deny","autoupdate":false}`
const openCodeV2ConfigContent = `{"permission":"deny","update":"disable"}`

var cachedOpenCodeV2 = newOpenCodeV2Detector(detectOpenCodeV2)

func newOpenCodeV2Detector(detect func(string) bool) func(string) bool {
	var mutex sync.Mutex
	var cachedPath string
	var cachedInfo os.FileInfo
	var cachedV2 bool
	return func(bin string) bool {
		path, err := exec.LookPath(bin)
		if err != nil {
			return false
		}
		info, err := os.Stat(path)
		if err != nil {
			return false
		}
		mutex.Lock()
		defer mutex.Unlock()
		if cachedInfo != nil && cachedPath == path && os.SameFile(cachedInfo, info) &&
			cachedInfo.Size() == info.Size() && cachedInfo.ModTime().Equal(info.ModTime()) {
			return cachedV2
		}
		cachedV2 = detect(path)
		cachedPath, cachedInfo = path, info
		return cachedV2
	}
}

const (
	openCodeScratchPrefix = ".opencode-"
	cursorScratchPrefix   = ".cursor-"
	// Well past the 90s run timeout, so a sweep cannot take a live run's
	// directory from a second collector started on another port.
	scratchMaxAge = time.Hour
)

// OpenCode has no ephemeral mode, so its runs go to a scratch database rather
// than the one holding the user's own sessions. One per run, not one shared:
// instances that overlap in time deadlock during init on a shared database,
// and the manager runs up to four at once.
func openCodeScratchDir() (string, error) {
	if err := os.MkdirAll(SynthesisCwd(), 0o700); err != nil {
		return "", fmt.Errorf("create synthesis directory: %w", err)
	}
	directory, err := os.MkdirTemp(SynthesisCwd(), openCodeScratchPrefix+"*")
	if err != nil {
		return "", fmt.Errorf("create OpenCode scratch directory: %w", err)
	}
	return directory, nil
}

// A crash or SIGKILL skips deferred cleanup. Scratch directories can hold
// requests and responses, so old ones are removed at startup.
func CleanupScratch() error {
	entries, err := os.ReadDir(SynthesisCwd())
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	cutoff := time.Now().Add(-scratchMaxAge)
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() ||
			(!strings.HasPrefix(name, openCodeScratchPrefix) && !strings.HasPrefix(name, cursorScratchPrefix)) {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(SynthesisCwd(), entry.Name())); err != nil &&
			!errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

func openCodeEnv(scratchDir string, v2 bool) []string {
	config := openCodeConfigContent
	if v2 {
		config = openCodeV2ConfigContent
	}
	env := []string{
		"OPENCODE_CONFIG_CONTENT=" + config,
		"OPENCODE_DB=" + filepath.Join(scratchDir, "opencode.db"),
		"OPENCODE_DISABLE_PROJECT_CONFIG=1",
		"OPENCODE_DISABLE_AUTOUPDATE=1",
		"OPENCODE_DISABLE_SHARE=1",
		// OpenCode reads PWD before cwd, which exec.Cmd does not update.
		"PWD=" + SynthesisCwd(),
		"NO_COLOR=1",
	}
	if v2 {
		// V2's plugin deny-all also removes its built-in build agent.
		// A private config home excludes global plugins and MCP servers instead.
		env = append(env, "XDG_CONFIG_HOME="+filepath.Join(scratchDir, "config"))
	} else {
		// V2 needs the current catalog to resolve its default and free models.
		env = append(env, "OPENCODE_DISABLE_MODELS_FETCH=1")
	}
	return env
}

func detectOpenCodeV2(bin string) bool {
	dataDir, err := os.MkdirTemp("", "coslash-opencode-version-")
	if err != nil {
		return false
	}
	defer os.RemoveAll(dataDir)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--version")
	cmd.Env = append(os.Environ(), "XDG_DATA_HOME="+dataDir)
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	for _, field := range strings.Fields(string(output)) {
		if strings.HasPrefix(strings.TrimPrefix(field, "v"), "2.") {
			return true
		}
	}
	return false
}

type openCodeEvent struct {
	Type string `json:"type"`
	Part struct {
		ID   string `json:"id"`
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"part"`
}

func parseOpenCodeStream(data []byte) (string, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	// Tool events dwarf the default 64K token limit.
	scanner.Buffer(make([]byte, 0, 64<<10), 4<<20)

	events := 0
	failed := false
	order := []string{}
	texts := map[string]string{}
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var event openCodeEvent
		// A stray banner or notice must not discard a good run.
		if err := json.Unmarshal(line, &event); err != nil {
			continue
		}
		events++
		switch event.Type {
		case "error":
			failed = true
		case "text":
			// Parts arrive as snapshots, so the newest text per part wins.
			key := event.Part.ID
			if key == "" {
				key = strconv.Itoa(len(order))
			}
			if _, seen := texts[key]; !seen {
				order = append(order, key)
			}
			texts[key] = event.Part.Text
		}
	}
	if events == 0 {
		return "", errors.New("OpenCode produced no synthesis output")
	}
	if failed {
		return "", errors.New("OpenCode reported an error during synthesis")
	}
	parts := make([]string, 0, len(order))
	for _, key := range order {
		if text := strings.TrimSpace(texts[key]); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n"), nil
}

func openCodeCommandDiagnostic(err error, output []byte) string {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return ""
	}
	if diagnostic := boundedCommandDiagnostic(exitErr.Stderr); diagnostic != "" {
		return diagnostic
	}
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		var event struct {
			Type  string `json:"type"`
			Error struct {
				Name    string `json:"name"`
				Message string `json:"message"`
				Data    struct {
					Message string `json:"message"`
				} `json:"data"`
			} `json:"error"`
		}
		if json.Unmarshal(line, &event) != nil || event.Type != "error" {
			continue
		}
		if diagnostic := boundedCommandDiagnostic([]byte(strings.Join(strings.Fields(event.Error.Name+" "+event.Error.Message+" "+event.Error.Data.Message), " "))); diagnostic != "" {
			return diagnostic
		}
	}
	return ""
}

func boundedCommandDiagnostic(data []byte) string {
	if len(data) > 2048 {
		data = data[:2048]
	}
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, string(data)))
}

// OpenCode cannot enforce an output schema, so prose around the object is tolerated.
func parseOpenCodeSynthesis(text string) (session.SessionSynthesis, error) {
	synthesis, err := parseOpenCodeObject(text)
	if err == nil {
		return synthesis, nil
	}
	object := extractJSONObject(text)
	if object == "" {
		return session.SessionSynthesis{}, err
	}
	return parseOpenCodeObject(object)
}

func parseOpenCodeObject(text string) (session.SessionSynthesis, error) {
	if err := requireSynthesisFields([]byte(stripJSONFence(text))); err != nil {
		return session.SessionSynthesis{}, err
	}
	return parseSynthesis([]byte(text))
}

func requireSynthesisFields(data []byte) error {
	var fields struct {
		Goals        []*string `json:"goals"`
		Outcome      *string   `json:"outcome"`
		KeyDecisions []*string `json:"keyDecisions"`
		NextStep     *string   `json:"nextStep"`
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		return fmt.Errorf("decode synthesis result: %w", err)
	}
	if len(fields.Goals) == 0 {
		return errors.New("synthesis result has no goals")
	}
	for _, goal := range fields.Goals {
		if goal == nil {
			return errors.New("synthesis result has a null goal")
		}
	}
	if fields.Outcome == nil {
		return errors.New("synthesis result has no outcome")
	}
	if fields.KeyDecisions == nil {
		return errors.New("synthesis result has no key decisions")
	}
	for _, decision := range fields.KeyDecisions {
		if decision == nil {
			return errors.New("synthesis result has a null key decision")
		}
	}
	if fields.NextStep == nil {
		return errors.New("synthesis result has no next step")
	}
	return nil
}

func extractJSONObject(value string) string {
	start := strings.IndexByte(value, '{')
	if start < 0 {
		return ""
	}
	var object json.RawMessage
	if err := json.NewDecoder(strings.NewReader(value[start:])).Decode(&object); err != nil {
		return ""
	}
	return string(object)
}
