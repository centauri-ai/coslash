package terminal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/httpsec"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/contracts"
	"github.com/coder/websocket"
)

func TestGuardedWebSocketRejectsMissingTokenAndCrossOrigin(t *testing.T) {
	runner := newFakeRunner()
	manager := New(Options{Runner: runner, PTYFactory: &fakePTYFactory{created: make(chan *fakePTY, 1)}})
	name, _ := Name("session", "guard")
	if _, err := manager.Create(context.Background(), Spec{ID: "guarded", TmuxName: name, Command: testCommand(t, ""), Writable: true}); err != nil {
		t.Fatal(err)
	}
	handler := Handler{Manager: manager}
	var guard httpsec.Guard
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		guard.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.WebSocket(w, r, "guarded") })).ServeHTTP(w, r)
	}))
	defer server.Close()
	guard = httpsec.Guard{Addr: server.Listener.Addr().String(), Token: "secret"}
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/terminals/guarded/ws"

	_, response, err := websocket.Dial(context.Background(), url, &websocket.DialOptions{Subprotocols: []string{httpsec.TerminalSubprotocol}})
	if err == nil || response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing token response=%v err=%v", response, err)
	}
	_, response, err = websocket.Dial(context.Background(), url, &websocket.DialOptions{Subprotocols: []string{httpsec.TerminalSubprotocol, "coslash.token.wrong"}})
	if err == nil || response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong token response=%v err=%v", response, err)
	}
	_, response, err = websocket.Dial(context.Background(), url, &websocket.DialOptions{
		Subprotocols: []string{httpsec.TerminalSubprotocol, "coslash.token.secret"},
		HTTPHeader:   http.Header{"Origin": []string{"https://evil.example"}},
	})
	if err == nil || response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin response=%v err=%v", response, err)
	}
}

func TestWebSocketBridgesBinaryOutputInputResizeAndReconnect(t *testing.T) {
	runner := newFakeRunner()
	factory := &fakePTYFactory{created: make(chan *fakePTY, 2)}
	manager := New(Options{Runner: runner, PTYFactory: factory})
	name, _ := Name("session", "bridge")
	if _, err := manager.Create(context.Background(), Spec{ID: "bridge", TmuxName: name, Command: testCommand(t, ""), Writable: true, PreserveOnClose: true}); err != nil {
		t.Fatal(err)
	}
	handler := Handler{Manager: manager}
	var guard httpsec.Guard
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		guard.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.WebSocket(w, r, "bridge") })).ServeHTTP(w, r)
	}))
	defer server.Close()
	guard = httpsec.Guard{Addr: server.Listener.Addr().String(), Token: "secret"}
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/terminals/bridge/ws"
	dial := func() *websocket.Conn {
		connection, _, err := websocket.Dial(context.Background(), url, &websocket.DialOptions{Subprotocols: []string{httpsec.TerminalSubprotocol, "coslash.token.secret"}})
		if err != nil {
			t.Fatal(err)
		}
		if connection.Subprotocol() != httpsec.TerminalSubprotocol {
			t.Fatalf("subprotocol = %q", connection.Subprotocol())
		}
		return connection
	}
	connection := dial()
	pty := <-factory.created
	pty.reads <- []byte("hello")
	messageType, payload, err := connection.Read(context.Background())
	if err != nil || messageType != websocket.MessageBinary || string(payload) != "hello" {
		t.Fatalf("type=%v payload=%q err=%v", messageType, payload, err)
	}
	input, _ := json.Marshal(map[string]any{"type": "input", "data": "ls\n"})
	if err := connection.Write(context.Background(), websocket.MessageText, input); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-pty.writes:
		if string(got) != "ls\n" {
			t.Fatalf("input = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("input was not written")
	}
	resize, _ := json.Marshal(map[string]any{"type": "resize", "cols": 140, "rows": 45})
	if err := connection.Write(context.Background(), websocket.MessageText, resize); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-pty.resizes:
		if got != [2]uint16{140, 45} {
			t.Fatalf("resize = %#v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("resize was not applied")
	}
	_ = connection.Close(websocket.StatusNormalClosure, "done")
	select {
	case <-pty.closed:
	case <-time.After(time.Second):
		t.Fatal("disconnect did not close PTY")
	}

	reconnected := dial()
	secondPTY := <-factory.created
	_ = secondPTY.Close()
	readCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, _, err := reconnected.Read(readCtx); err == nil {
		t.Fatal("PTY exit did not close the WebSocket")
	}
	_ = reconnected.CloseNow()
	select {
	case <-secondPTY.closed:
	case <-time.After(time.Second):
		t.Fatal("reconnect PTY did not close")
	}
}

func TestWebSocketClosesOversizedMessages(t *testing.T) {
	runner := newFakeRunner()
	factory := &fakePTYFactory{created: make(chan *fakePTY, 1)}
	manager := New(Options{Runner: runner, PTYFactory: factory})
	name, _ := Name("session", "large")
	if _, err := manager.Create(context.Background(), Spec{ID: "large", TmuxName: name, Command: testCommand(t, ""), Writable: true}); err != nil {
		t.Fatal(err)
	}
	handler := Handler{Manager: manager}
	var guard httpsec.Guard
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		guard.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.WebSocket(w, r, "large") })).ServeHTTP(w, r)
	}))
	defer server.Close()
	guard = httpsec.Guard{Addr: server.Listener.Addr().String(), Token: "secret"}
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/terminals/large/ws"
	connection, _, err := websocket.Dial(context.Background(), url, &websocket.DialOptions{Subprotocols: []string{httpsec.TerminalSubprotocol, "coslash.token.secret"}})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	<-factory.created
	frame, _ := json.Marshal(map[string]any{"type": "input", "data": strings.Repeat("x", MaxInputBytes+2048)})
	_ = connection.Write(context.Background(), websocket.MessageText, frame)
	readCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, _, err := connection.Read(readCtx); err == nil {
		t.Fatal("oversized WebSocket message was not rejected")
	}
}

func TestWebSocketRejectsUnknownAndLargeFrames(t *testing.T) {
	frame := []byte(`{"type":"input","data":"ok","extra":true}`)
	var target map[string]any
	if decodeStrict(frame, &target) != nil {
		t.Fatal("generic map should accept fields")
	}
	var typed struct {
		Type string `json:"type"`
	}
	if decodeStrict(frame, &typed) == nil {
		t.Fatal("strict decoder accepted unknown field")
	}
	if validateFrame(contracts.TerminalClientFrame{Type: contracts.TerminalFrameInput, Data: strings.Repeat("x", MaxInputBytes+1)}) == nil {
		t.Fatal("large input frame was accepted")
	}
	if validateFrame(contracts.TerminalClientFrame{Type: contracts.TerminalFrameResize}) == nil {
		t.Fatal("zero resize frame was accepted")
	}
}
