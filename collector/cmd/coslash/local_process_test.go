package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type localTestProcess struct {
	cmd    *exec.Cmd
	output *bytes.Buffer
	desc   runtimeDescriptor
	token  string
}

func startLocalTestProcess(t *testing.T, home, childFlag, testName string) *localTestProcess {
	t.Helper()
	return startLocalTestCommand(t, home, childFlag, os.Args[0], "-test.run=^"+testName+"$")
}

func startLocalTestCommand(t *testing.T, home, childFlag, binary string, args ...string) *localTestProcess {
	t.Helper()
	p := &localTestProcess{output: &bytes.Buffer{}}
	p.cmd = exec.Command(binary, args...)
	p.cmd.Env = append(os.Environ(), childFlag+"=1")
	p.cmd.Stdout, p.cmd.Stderr = p.output, p.output
	if err := p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if p.cmd.ProcessState == nil {
			_ = p.cmd.Process.Kill()
			_ = p.cmd.Wait()
		}
	})
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		data, e1 := os.ReadFile(filepath.Join(home, "runtime.json"))
		token, e2 := os.ReadFile(filepath.Join(home, "token"))
		if e1 == nil && e2 == nil && json.Unmarshal(data, &p.desc) == nil && p.desc.BaseURL != "" {
			p.token = strings.TrimSpace(string(token))
			return p
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = p.cmd.Process.Kill()
	_ = p.cmd.Wait()
	t.Fatalf("Local did not start: %s", p.output.String())
	return nil
}

func (p *localTestProcess) request(t *testing.T, method, path, body string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, p.desc.BaseURL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Coslash-Token", p.token)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, data
}

func (p *localTestProcess) stop(t *testing.T) {
	t.Helper()
	status, data := p.request(t, http.MethodPost, "/api/shutdown", "{}")
	if status != http.StatusAccepted {
		t.Fatalf("shutdown %d: %s", status, data)
	}
	done := make(chan error, 1)
	go func() { done <- p.cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Local exit: %v: %s", err, p.output.String())
		}
	case <-time.After(20 * time.Second):
		_ = p.cmd.Process.Kill()
		<-done
		t.Fatal("Local did not shut down")
	}
}
