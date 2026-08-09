package contracts

// TerminalClientFrameType discriminates the bounded JSON frames accepted from
// an authenticated terminal WebSocket client.
type TerminalClientFrameType string

const (
	TerminalFrameInput  TerminalClientFrameType = "input"
	TerminalFrameResize TerminalClientFrameType = "resize"
)

// TerminalClientFrame is decoded before input or resize limits are enforced by
// the terminal package. Fields not used by the selected Type must be rejected.
type TerminalClientFrame struct {
	Type TerminalClientFrameType `json:"type"`
	Data string                  `json:"data,omitempty"`
	Cols uint16                  `json:"cols,omitempty"`
	Rows uint16                  `json:"rows,omitempty"`
}

// TerminalServerFrame is the terminal byte payload sent by the server. It is a
// WebSocket binary payload, not a JSON or base64 envelope.
type TerminalServerFrame []byte

// TerminalStatus is the reconnect-safe HTTP representation of a terminal.
type TerminalStatus struct {
	TerminalID string `json:"terminalId"`
	State      string `json:"state"`
	Writable   bool   `json:"writable"`
}
