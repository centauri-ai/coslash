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

const (
	logRetentionDays = 7
	maxLogBytes      = 5 << 20
	slowOperation    = time.Second
)

var (
	fileMu   sync.Mutex
	file     *os.File
	day      string
	filePath string
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

// Event emits one structured line to the coslash server CLI and
// ~/.coslash/logs/issues-YYYYMMDD.log. Operators watching the process terminal
// see the same brief issue context without opening the log file.
//
// Use greppable names like "issue.launch.failed" or "remote.refresh".
// Fields must stay low-cardinality: reasons, timings, statuses, short detail —
// never paths, session ids, aliases, prompts, tokens, or raw stderr hosts.
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
	// Stdlib log defaults to stderr, which is the terminal running coslash.
	log.Print(line)
	appendFile(line)
}

// Operation records failed work and successful work that exceeds the slow threshold.
// Failures use the issue.operation name so they show up with other issue.* CLI lines.
func Operation(operation string, started time.Time, outcome string, fields ...any) {
	duration := time.Since(started)
	if outcome == "ok" && duration < slowOperation {
		return
	}
	values := make([]any, 0, 8+len(fields))
	values = append(values,
		"operation", operation,
		"outcome", outcome,
		"duration_ms", duration.Milliseconds(),
	)
	values = append(values, fields...)
	name := "operation"
	if outcome != "ok" {
		name = "issue.operation"
		values = append(values, "detail", operation+" failed")
	}
	Event(name, values...)
}

func appendFile(line string) {
	fileMu.Lock()
	defer fileMu.Unlock()
	today := time.Now().Format("20060102")
	path := filepath.Join(LogDir(), "issues-"+today+".log")
	if file == nil || day != today || filePath != path {
		if file != nil {
			_ = file.Close()
			file = nil
		}
		if err := os.MkdirAll(LogDir(), 0o700); err != nil {
			return
		}
		pruneLogFiles(LogDir(), today)
		opened, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return
		}
		file = opened
		day = today
		filePath = path
	}
	record := fmt.Sprintf("%s %s\n", time.Now().Format(time.RFC3339), line)
	info, err := file.Stat()
	if err != nil || info.Size()+int64(len(record)) > maxLogBytes {
		return
	}
	_, _ = fmt.Fprint(file, record)
}

func pruneLogFiles(dir, today string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -(logRetentionDays - 1)).Format("20060102")
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || len(name) != len("issues-YYYYMMDD.log") ||
			!strings.HasPrefix(name, "issues-") || !strings.HasSuffix(name, ".log") {
			continue
		}
		date := name[len("issues-") : len("issues-")+len("YYYYMMDD")]
		if date < cutoff && date < today {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}
