package synthesis

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

const openCodeConfigContent = `{"permission":"deny","autoupdate":false}`

func openCodeEnv() []string {
	return []string{
		"OPENCODE_CONFIG_CONTENT=" + openCodeConfigContent,
		// OpenCode has no ephemeral mode, so its runs go to a scratch database
		// rather than the one holding the user's own sessions.
		"OPENCODE_DB=" + filepath.Join(SynthesisCwd(), "opencode.db"),
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
	synthesis, err := parseSynthesis([]byte(text))
	if err == nil {
		return synthesis, nil
	}
	object := extractJSONObject(text)
	if object == "" {
		return session.SessionSynthesis{}, err
	}
	return parseSynthesis([]byte(object))
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
