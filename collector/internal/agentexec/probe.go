package agentexec

import (
	"bytes"
	"errors"
	"os/exec"
	"time"
)

// ProbeOutput bounds discovery output and drains owned pipes on cancellation.
func ProbeOutput(command *exec.Cmd, limit int) ([]byte, error) {
	cleanup := configureProbeProcess(command)
	defer cleanup()
	command.WaitDelay = time.Second
	output := probeBuffer{remaining: limit}
	command.Stdout = &output
	command.Stderr = &probeBuffer{remaining: 1024}
	err := Run(command)
	if err == nil && output.overflow {
		err = errors.New("CLI probe output exceeds limit")
	}
	return output.buffer.Bytes(), err
}

type probeBuffer struct {
	buffer    bytes.Buffer
	remaining int
	overflow  bool
}

func (buffer *probeBuffer) Write(data []byte) (int, error) {
	count := min(len(data), buffer.remaining)
	buffer.remaining -= count
	_, _ = buffer.buffer.Write(data[:count])
	if count < len(data) {
		buffer.overflow = true
	}
	return len(data), nil
}
