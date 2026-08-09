package terminal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/centauri-ai/coslash/collector/internal/httpsec"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/contracts"
	"github.com/coder/websocket"
)

const maxHTTPBody = MaxPasteBytes + 1024

type Handler struct {
	Manager *Manager
}

func (handler Handler) Status(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	status, err := handler.Manager.Status(r.Context(), id)
	if err != nil {
		writeManagerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (handler Handler) Input(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	var frame contracts.TerminalClientFrame
	if err := decodeJSON(w, r, &frame); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_FRAME", "invalid terminal frame")
		return
	}
	if err := validateFrame(frame); err != nil || frame.Type != contracts.TerminalFrameInput {
		writeError(w, http.StatusBadRequest, "INVALID_FRAME", "invalid terminal frame")
		return
	}
	if err := handler.Manager.Input(r.Context(), id, frame.Data); err != nil {
		writeManagerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (handler Handler) Stop(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1))
	if err != nil || len(body) != 0 {
		writeError(w, http.StatusBadRequest, "BODY_NOT_ALLOWED", "request body is not allowed")
		return
	}
	if err := handler.Manager.Stop(r.Context(), id); err != nil {
		writeManagerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (handler Handler) WebSocket(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet || !httpsec.IsWebSocketUpgrade(r) || httpsec.NegotiateSubprotocol(r) != httpsec.TerminalSubprotocol {
		writeError(w, http.StatusBadRequest, "INVALID_WEBSOCKET", "invalid WebSocket handshake")
		return
	}
	if _, err := handler.Manager.Status(r.Context(), id); err != nil {
		writeManagerError(w, err)
		return
	}
	connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{httpsec.TerminalSubprotocol}})
	if err != nil {
		return
	}
	defer connection.CloseNow()
	if connection.Subprotocol() != httpsec.TerminalSubprotocol {
		_ = connection.Close(websocket.StatusPolicyViolation, "required subprotocol missing")
		return
	}
	connection.SetReadLimit(MaxInputBytes + 1024)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client, err := handler.Manager.Attach(ctx, id, DefaultCols, DefaultRows)
	if err != nil {
		_ = connection.Close(websocket.StatusInternalError, "terminal unavailable")
		return
	}
	var output sync.WaitGroup
	defer func() {
		cancel()
		_ = client.Close()
		output.Wait()
	}()

	outputDone := make(chan error, 1)
	output.Add(1)
	go func() {
		defer output.Done()
		buffer := make([]byte, 32<<10)
		for {
			count, readErr := client.Read(buffer)
			if count > 0 {
				payload := append([]byte(nil), buffer[:count]...)
				if writeErr := connection.Write(ctx, websocket.MessageBinary, payload); writeErr != nil {
					outputDone <- writeErr
					return
				}
			}
			if readErr != nil {
				_ = connection.Close(websocket.StatusNormalClosure, "terminal detached")
				outputDone <- readErr
				return
			}
		}
	}()

	for {
		select {
		case <-outputDone:
			_ = connection.Close(websocket.StatusNormalClosure, "terminal detached")
			return
		default:
		}
		messageType, data, readErr := connection.Read(ctx)
		if readErr != nil {
			return
		}
		if messageType != websocket.MessageText {
			_ = connection.Close(websocket.StatusUnsupportedData, "text control frames required")
			return
		}
		var frame contracts.TerminalClientFrame
		if decodeStrict(data, &frame) != nil || validateFrame(frame) != nil {
			_ = connection.Close(websocket.StatusPolicyViolation, "invalid terminal frame")
			return
		}
		switch frame.Type {
		case contracts.TerminalFrameInput:
			if _, err := client.Write([]byte(frame.Data)); err != nil {
				_ = connection.Close(websocket.StatusPolicyViolation, "terminal is not writable")
				return
			}
		case contracts.TerminalFrameResize:
			if err := client.Resize(frame.Cols, frame.Rows); err != nil {
				_ = connection.Close(websocket.StatusPolicyViolation, "invalid terminal dimensions")
				return
			}
		}
	}
}

func validateFrame(frame contracts.TerminalClientFrame) error {
	switch frame.Type {
	case contracts.TerminalFrameInput:
		if frame.Data == "" || len(frame.Data) > MaxInputBytes || frame.Cols != 0 || frame.Rows != 0 || strings.ContainsRune(frame.Data, 0) {
			return errors.New("invalid input frame")
		}
	case contracts.TerminalFrameResize:
		if frame.Data != "" || frame.Cols == 0 || frame.Rows == 0 {
			return errors.New("invalid resize frame")
		}
		_, _, err := dimensions(frame.Cols, frame.Rows)
		return err
	default:
		return errors.New("unknown frame type")
	}
	return nil
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	reader := http.MaxBytesReader(w, r.Body, maxHTTPBody)
	defer reader.Close()
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("multiple JSON values")
	}
	return nil
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("multiple JSON values")
	}
	return nil
}

func writeManagerError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "TERMINAL_NOT_FOUND", "terminal not found")
	case errors.Is(err, ErrReadOnly):
		writeError(w, http.StatusForbidden, "TERMINAL_READ_ONLY", "terminal is read-only")
	default:
		writeError(w, http.StatusInternalServerError, "TERMINAL_ERROR", "terminal operation failed")
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, contracts.ErrorResponse{OK: false, Code: code, Error: message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
