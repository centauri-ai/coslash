package remoteviewv1

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

var (
	ErrOversized      = errors.New("remote view exceeds aggregate limit")
	ErrInvalidFrame   = errors.New("invalid remote transport frame")
	ErrTrailingFrame  = errors.New("framed payload has trailing content")
	ErrMissingFrame   = errors.New("remote transport frame missing")
	ErrDuplicateFrame = errors.New("duplicate remote transport frame")
)

// EncodeFrame wraps payload bytes in COSLASH-REMOTE/1 length framing.
func EncodeFrame(payload []byte) ([]byte, error) {
	if len(payload) > MaxPayloadBytes {
		return nil, fmt.Errorf("payload is %d bytes; maximum is %d: %w", len(payload), MaxPayloadBytes, ErrOversized)
	}
	header := fmt.Sprintf("%s %d\n", FrameMagic, len(payload))
	framed := make([]byte, 0, len(header)+len(payload))
	framed = append(framed, header...)
	framed = append(framed, payload...)
	return framed, nil
}

// DecodeExactFrame requires the buffer to begin with one frame and contain
// only optional trailing whitespace after the declared payload.
func DecodeExactFrame(data []byte) ([]byte, error) {
	payload, consumed, err := readFrameAt(data, 0)
	if err != nil {
		return nil, err
	}
	if trailingNonSpace(data[consumed:]) {
		return nil, ErrTrailingFrame
	}
	return payload, nil
}

// ExtractFrame scans bounded stdout for exactly one frame header at a line
// boundary, reads the declared payload, and rejects duplicates or non-space
// trailing bytes after that payload.
func ExtractFrame(data []byte) (payload, prefix []byte, err error) {
	offset := 0
	for offset < len(data) {
		lineStart := offset
		newline := bytes.IndexByte(data[offset:], '\n')
		if newline < 0 {
			break
		}
		lineEnd := offset + newline
		line := data[lineStart:lineEnd]
		if isFrameHeaderLine(line) {
			payload, consumed, err := readFrameAt(data, lineStart)
			if err != nil {
				return nil, nil, err
			}
			if second := findFrameHeader(data[consumed:]); second >= 0 {
				return nil, nil, ErrDuplicateFrame
			}
			if trailingNonSpace(data[consumed:]) {
				return nil, nil, ErrTrailingFrame
			}
			return payload, data[:lineStart], nil
		}
		offset = lineEnd + 1
	}
	if findFrameHeader(data) >= 0 {
		return nil, nil, fmt.Errorf("%w: truncated frame", ErrInvalidFrame)
	}
	return nil, nil, ErrMissingFrame
}

func readFrameAt(data []byte, start int) ([]byte, int, error) {
	if start < 0 || start >= len(data) {
		return nil, 0, ErrInvalidFrame
	}
	newline := bytes.IndexByte(data[start:], '\n')
	if newline < 0 {
		return nil, 0, fmt.Errorf("%w: missing header newline", ErrInvalidFrame)
	}
	header := data[start : start+newline]
	length, err := parseFrameHeader(header)
	if err != nil {
		return nil, 0, err
	}
	if length > MaxPayloadBytes {
		return nil, 0, fmt.Errorf("framed payload is %d bytes; maximum is %d: %w", length, MaxPayloadBytes, ErrOversized)
	}
	payloadStart := start + newline + 1
	payloadEnd := payloadStart + length
	if payloadEnd > len(data) {
		return nil, 0, fmt.Errorf("%w: truncated payload", ErrInvalidFrame)
	}
	return data[payloadStart:payloadEnd], payloadEnd, nil
}

func parseFrameHeader(line []byte) (int, error) {
	if !isFrameHeaderLine(line) {
		return 0, fmt.Errorf("%w: bad header", ErrInvalidFrame)
	}
	fields := bytes.SplitN(line, []byte{' '}, 2)
	if len(fields) != 2 {
		return 0, fmt.Errorf("%w: bad header", ErrInvalidFrame)
	}
	raw := string(fields[1])
	if raw == "" || raw[0] == '+' || raw[0] == '-' || (len(raw) > 1 && raw[0] == '0') {
		return 0, fmt.Errorf("%w: bad payload length", ErrInvalidFrame)
	}
	for _, char := range raw {
		if char < '0' || char > '9' {
			return 0, fmt.Errorf("%w: bad payload length", ErrInvalidFrame)
		}
	}
	length, err := strconv.Atoi(raw)
	if err != nil || length < 0 {
		return 0, fmt.Errorf("%w: bad payload length", ErrInvalidFrame)
	}
	return length, nil
}

func isFrameHeaderLine(line []byte) bool {
	prefix := FrameMagic + " "
	return bytes.HasPrefix(line, []byte(prefix))
}

func findFrameHeader(data []byte) int {
	prefix := []byte(FrameMagic + " ")
	offset := 0
	for offset < len(data) {
		index := bytes.Index(data[offset:], prefix)
		if index < 0 {
			return -1
		}
		absolute := offset + index
		if absolute == 0 || data[absolute-1] == '\n' {
			return absolute
		}
		offset = absolute + 1
	}
	return -1
}

func trailingNonSpace(data []byte) bool {
	return strings.IndexFunc(string(data), func(r rune) bool {
		return !unicode.IsSpace(r)
	}) >= 0
}
