package launch

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxSendEventBytes = 1 << 20
const maxSendOutputBytes = 64 << 10

type sendOutput struct {
	target   string
	line     []byte
	result   SendResult
	complete bool
	err      error
}

// Parsing failures still consume every byte so the agent cannot block on stdout.
func (s *sendOutput) Write(data []byte) (int, error) {
	n := len(data)
	for len(data) > 0 && s.err == nil {
		part, rest, found := bytes.Cut(data, []byte{'\n'})
		if len(s.line)+len(part) > maxSendEventBytes {
			s.err = fmt.Errorf("%w: event exceeds size limit", ErrSendFailed)
			s.line = nil
			break
		}
		s.line = append(s.line, part...)
		if !found {
			break
		}
		s.parse(s.line)
		s.line = s.line[:0]
		data = rest
	}
	return n, nil
}

func (s *sendOutput) parse(data []byte) {
	if len(bytes.TrimSpace(data)) == 0 {
		return
	}
	var event struct {
		Type, Subtype, Result string
		IsError               *bool             `json:"is_error"`
		SessionID             string            `json:"session_id"`
		ThreadID              string            `json:"thread_id"`
		PermissionDenials     []json.RawMessage `json:"permission_denials"`
		Item                  struct{ Type, Text string }
	}
	if !utf8.Valid(data) || json.Unmarshal(data, &event) != nil || event.Type == "" {
		s.err = fmt.Errorf("%w: malformed event", ErrSendFailed)
		return
	}
	if event.Type == "error" || event.Type == "turn.failed" {
		s.err = fmt.Errorf("%w: provider reported failure", ErrSendFailed)
		return
	}
	if s.target == "claude" {
		if len(event.PermissionDenials) > 0 {
			s.err = ErrSendPermission
			return
		}
		if event.Type != "result" {
			return
		}
		if event.Subtype != "success" || event.IsError == nil || *event.IsError {
			s.err = fmt.Errorf("%w: provider result failed", ErrSendFailed)
			return
		}
		s.result = SendResult{Output: event.Result, SessionID: event.SessionID}
	} else {
		switch event.Type {
		case "thread.started":
			s.result.SessionID = event.ThreadID
		case "item.completed":
			if event.Item.Type == "agent_message" {
				s.result.Output = event.Item.Text
			}
		}
		if event.Type != "turn.completed" {
			return
		}
	}
	if s.complete {
		s.err = fmt.Errorf("%w: duplicate completion", ErrSendFailed)
	}
	s.complete = true
}

func (s *sendOutput) finish() (SendResult, error) {
	if s.err == nil && len(s.line) > 0 {
		s.parse(s.line)
	}
	if s.err != nil {
		return SendResult{}, s.err
	}
	if !s.complete || s.result.SessionID != "" && !uuidSessionIDPattern.MatchString(s.result.SessionID) {
		return SendResult{}, fmt.Errorf("%w: missing completion or invalid session ID", ErrSendFailed)
	}
	s.result.Output = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' || unicode.In(r, unicode.Cf) {
			return -1
		}
		return r
	}, s.result.Output)
	if len(s.result.Output) > maxSendOutputBytes {
		const marker = "\n[output truncated]"
		end := maxSendOutputBytes - len(marker)
		for !utf8.RuneStart(s.result.Output[end]) {
			end--
		}
		s.result.Output = s.result.Output[:end] + marker
	}
	return s.result, nil
}
