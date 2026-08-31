// Package observe records local, privacy-safe product issue lines for debugging.
// Testing-branch only by default — no network export.
package observe

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/settings"
)

// Default on for this testing branch. Flip to false before promoting to main.
const defaultOn = true

var (
	fileMu sync.Mutex
	file   *os.File
	day    string
)

// Enabled reports whether issue / step logging is active.
// COSLASH_DEBUG wins; empty falls back to COSLASH_REMOTE_DEBUG, then defaultOn.
func Enabled() bool {
	if value, ok := envBool("COSLASH_DEBUG"); ok {
		return value
	}
	if value, ok := envBool("COSLASH_REMOTE_DEBUG"); ok {
		return value
	}
	return defaultOn
}

func envBool(name string) (bool, bool) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return false, false
	}
	switch strings.ToLower(raw) {
	case "0", "false", "off", "no":
		return false, true
	case "1", "true", "on", "yes":
		return true, true
	default:
		return true, true
	}
}

// LogDir is the directory for daily issue log files under COSLASH_HOME.
func LogDir() string {
	return filepath.Join(settings.Home(), "logs")
}

// Event emits one structured line (stdout + ~/.coslash/logs/issues-YYYYMMDD.log).
// Use greppable names like "issue.launch.failed" or "remote.refresh".
// Fields must stay low-cardinality: reasons, timings, statuses — never paths,
// session ids, aliases, prompts, tokens, or raw stderr hosts.
func Event(name string, fields ...any) {
	if !Enabled() {
		return
	}
	parts := make([]string, 0, 1+len(fields)/2)
	parts = append(parts, name)
	for i := 0; i+1 < len(fields); i += 2 {
		parts = append(parts, fmt.Sprintf("%v=%v", fields[i], fields[i+1]))
	}
	line := strings.Join(parts, " ")
	log.Print(line)
	appendFile(line)
}

func appendFile(line string) {
	fileMu.Lock()
	defer fileMu.Unlock()
	today := time.Now().Format("20060102")
	if file == nil || day != today {
		if file != nil {
			_ = file.Close()
			file = nil
		}
		if err := os.MkdirAll(LogDir(), 0o700); err != nil {
			return
		}
		path := filepath.Join(LogDir(), "issues-"+today+".log")
		opened, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return
		}
		file = opened
		day = today
	}
	_, _ = fmt.Fprintf(file, "%s %s\n", time.Now().Format(time.RFC3339), line)
}
