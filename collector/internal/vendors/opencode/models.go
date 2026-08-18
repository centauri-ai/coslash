package opencode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	modelsTTL    = 10 * time.Minute
	modelsLimit  = 50
	zenPrefix    = "opencode/"
	modelTimeout = 10 * time.Second
)

var modelsCache struct {
	mu     sync.Mutex
	models []string
	loaded time.Time
}

// SynthesisModels lists the live OpenCode Zen models that cost nothing to run,
// so the picker cannot bill the user for a model they did not name. Results are
// cached because the CLI call costs about a second and settings are polled.
func SynthesisModels() []string {
	modelsCache.mu.Lock()
	defer modelsCache.mu.Unlock()
	// Empty results are cached too, so a broken CLI is only paid for once.
	if !modelsCache.loaded.IsZero() && time.Since(modelsCache.loaded) < modelsTTL {
		return modelsCache.models
	}
	modelsCache.models = loadModels()
	modelsCache.loaded = time.Now()
	return modelsCache.models
}

type verboseModel struct {
	Cost struct {
		Input  float64 `json:"input"`
		Output float64 `json:"output"`
	} `json:"cost"`
	Capabilities struct {
		Output struct {
			Text bool `json:"text"`
		} `json:"output"`
	} `json:"capabilities"`
	Status string `json:"status"`
}

func loadModels() []string {
	ctx, cancel := context.WithTimeout(context.Background(), modelTimeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, "opencode", "models", "opencode", "--verbose").Output()
	if err != nil {
		return []string{}
	}
	return parseVerboseModels(output)
}

// parseVerboseModels reads the CLI's "provider/id" header lines followed by a
// pretty-printed object whose closing brace sits in the first column.
func parseVerboseModels(output []byte) []string {
	models := []string{}
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	name := ""
	var body []string
	for scanner.Scan() && len(models) < modelsLimit {
		line := scanner.Text()
		switch {
		case body != nil:
			body = append(body, line)
			if line != "}" {
				continue
			}
			if freeZenModel(name, strings.Join(body, "\n")) {
				models = append(models, name)
			}
			name, body = "", nil
		case line == "{" && name != "":
			body = []string{line}
		default:
			name = strings.TrimSpace(line)
		}
	}
	return models
}

func freeZenModel(name, body string) bool {
	if !strings.HasPrefix(name, zenPrefix) {
		return false
	}
	var model verboseModel
	if err := json.Unmarshal([]byte(body), &model); err != nil {
		return false
	}
	return model.Status != "deprecated" && model.Cost.Input == 0 && model.Cost.Output == 0 &&
		model.Capabilities.Output.Text
}
