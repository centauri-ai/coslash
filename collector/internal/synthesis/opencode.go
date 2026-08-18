package synthesis

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

const openCodeConfigContent = `{"permission":"deny","autoupdate":false}`

// OpenCode instances that overlap in time deadlock during initialization when
// they share a database, and the manager runs up to four at once, so each run
// gets its own directory to hold one.
func openCodeScratchDir() (string, error) {
	if err := os.MkdirAll(SynthesisCwd(), 0o700); err != nil {
		return "", fmt.Errorf("create synthesis directory: %w", err)
	}
	directory, err := os.MkdirTemp(SynthesisCwd(), ".opencode-*")
	if err != nil {
		return "", fmt.Errorf("create OpenCode scratch directory: %w", err)
	}
	return directory, nil
}

func openCodeEnv(scratchDir string) []string {
	return []string{
		"OPENCODE_CONFIG_CONTENT=" + openCodeConfigContent,
		// OpenCode has no ephemeral mode, so its runs go to a scratch database
		// rather than the one holding the user's own sessions.
		"OPENCODE_DB=" + filepath.Join(scratchDir, "opencode.db"),
		"OPENCODE_DISABLE_PROJECT_CONFIG=1",
		"OPENCODE_DISABLE_AUTOUPDATE=1",
		"OPENCODE_DISABLE_SHARE=1",
		"OPENCODE_DISABLE_MODELS_FETCH=1",
		// OpenCode reads PWD before cwd, which exec.Cmd does not update.
		"PWD=" + SynthesisCwd(),
		"NO_COLOR=1",
	}
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

// The schema only reaches OpenCode as prompt text, so the fields it marks
// required are checked here rather than by the CLI.
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
	depth := 0
	inString := false
	escaped := false
	for index := start; index < len(value); index++ {
		char := value[index]
		if escaped {
			escaped = false
			continue
		}
		switch {
		case char == '\\' && inString:
			escaped = true
		case char == '"':
			inString = !inString
		case inString:
		case char == '{':
			depth++
		case char == '}':
			depth--
			if depth == 0 {
				return value[start : index+1]
			}
		}
	}
	return ""
}
