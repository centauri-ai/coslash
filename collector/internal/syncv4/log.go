package syncv4

import (
	"errors"
	"slices"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/sessionbackupproducer"
	sessionbackupv1 "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
)

// The sync log reports failures to the Hub device page and the failed
// session's card. Lines carry only a closed code, the Hub session ID and a
// fixed message; the Hub replaces the message with its own text anyway.
const (
	// maxLog bounds the unsent lines, so a first sync of thousands of failing
	// sessions cannot grow the queue file without limit. The oldest go first.
	maxLog = 1000
	// maxLogBatch is the Hub's per-check-in limit.
	maxLogBatch = 200
	// logRetention stays inside the Hub's 30-day window, which rejects older
	// lines.
	logRetention = 29 * 24 * time.Hour
)

// LogLine is one unsent sync log line in the queue file.
type LogLine struct {
	Seq       int64     `json:"seq"`
	At        time.Time `json:"at"`
	SessionID string    `json:"sessionId,omitempty"`
	Level     string    `json:"level"`
	Code      string    `json:"code"`
}

// logMessages is the fixed, content-free text for each code Local sends. It
// matches the Hub's own text, which replaces it on arrival.
var logMessages = map[string]string{
	"transcript_changed_during_read": "The session changed while coSlash Local read it.",
	"upload_expired":                 "The upload expired.",
	"server_error":                   "coSlash Hub could not store this sync.",
	"client_update_required":         "coSlash Local needs an update.",
	"too_large":                      "The session exceeds the size limit.",
	"auth_revoked":                   "The device was removed.",
	"unreadable_source":              "coSlash Local cannot read the session folder.",
	"hash_mismatch":                  "Uploaded data did not match its checksum.",
	"space_full":                     "The space is full.",
	"malformed_artifact":             "The session data could not be read.",
}

// hubLogCode maps a failure the queue recorded (code, and the error when the
// failure came from a call) to the Hub's closed log codes. It returns "" for
// outcomes that are not failures for the owner: the Hub's active-upload
// back-pressure, a leave-out, a Hub session the owner deleted, a superseded
// or aborted upload, and a spool Local rebuilds on the next pass.
func hubLogCode(code string, failure error) string {
	if errors.Is(failure, sessionbackupproducer.ErrNotPrepared) {
		return ""
	}
	var preparation *sessionbackupproducer.PreparationError
	if errors.As(failure, &preparation) {
		if len(preparation.Coverage.Problems) == 0 {
			return "unreadable_source"
		}
		switch preparation.Coverage.Problems[0].Code {
		case sessionbackupv1.ProblemUnstable:
			return "transcript_changed_during_read"
		case sessionbackupv1.ProblemUnsupported, sessionbackupv1.ProblemInvalid, sessionbackupv1.ProblemUnattributable:
			return "malformed_artifact"
		default:
			return "unreadable_source"
		}
	}
	switch code {
	case "", "rate_limited", "left_out", "session_deleted", "superseded", "client_aborted":
		return ""
	case "upload_expired", "not_found":
		// Local restarts an upload the Hub no longer knows, as for expiry.
		return "upload_expired"
	case "too_large", "request_too_large", "http_413":
		return "too_large"
	case "device_revoked", "unauthorized":
		return "auth_revoked"
	}
	if _, ok := logMessages[code]; ok {
		return code
	}
	return "server_error"
}

// UpdateWithLog stores entry and appends line to the unsent log in the same
// write.
func (q *Queue) UpdateWithLog(entry Entry, line LogLine) error {
	return q.update(entry, &line)
}

// PendingLog returns the oldest unsent lines the Hub still accepts, at most
// one check-in's worth, and the sequence number through which a successful
// check-in acknowledges them. Lines past the Hub's retention are skipped and
// dropped by that acknowledgement.
func (q *Queue) PendingLog(now time.Time) ([]hubclient.V4LogEntry, int64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	var lines []hubclient.V4LogEntry
	var through int64
	for _, line := range q.state.Log {
		if len(lines) == maxLogBatch {
			break
		}
		through = line.Seq
		if line.At.Before(now.Add(-logRetention)) {
			continue
		}
		at := line.At
		// A clock set back since the failure must not date it in the future.
		if at.After(now) {
			at = now
		}
		lines = append(lines, hubclient.V4LogEntry{At: at.UTC(), SessionID: line.SessionID, Level: line.Level,
			Message: logMessages[line.Code], Code: line.Code})
	}
	return lines, through
}

// AcknowledgeLog drops the lines through seq after the Hub accepted them.
func (q *Queue) AcknowledgeLog(seq int64) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.state.Log) == 0 || q.state.Log[0].Seq > seq {
		return nil
	}
	prior := q.state.Log
	q.state.Log = slices.DeleteFunc(slices.Clone(q.state.Log), func(line LogLine) bool { return line.Seq <= seq })
	if err := q.save(); err != nil {
		q.state.Log = prior
		return err
	}
	return nil
}

// pendingDeviceLine reports an unsent line without a session and with the same
// code. Such lines are identical apart from their time, so a first sync of many
// sessions Local cannot read sends one line per code rather than one per
// session; the check-in's failing count still counts them. The caller holds
// q.mu.
func (q *Queue) pendingDeviceLine(line LogLine) bool {
	return line.SessionID == "" && slices.ContainsFunc(q.state.Log, func(pending LogLine) bool {
		return pending.SessionID == "" && pending.Code == line.Code
	})
}

// appendLog adds a line, dropping the oldest beyond maxLog. The caller holds
// q.mu.
func (q *Queue) appendLog(line LogLine) {
	q.state.LogSeq++
	line.Seq = q.state.LogSeq
	q.state.Log = append(q.state.Log, line)
	if extra := len(q.state.Log) - maxLog; extra > 0 {
		q.state.Log = slices.Delete(q.state.Log, 0, extra)
	}
}
