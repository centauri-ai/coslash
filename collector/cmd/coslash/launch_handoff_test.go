package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/launch"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	remoteviewv1 "github.com/centauri-ai/coslash/collector/remoteview/v1"
)

func TestRunHandoffPutFramesOpaqueID(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	stdin := strings.NewReader("brief text")
	code := runHandoff(&stdout, &stderr, stdin, []string{
		"put",
		"--agent", vendors.AgentClaude,
		"--session", "9c73be46-52af-4b1d-9ee7-123456789abc",
	})
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	payload, err := remoteviewv1.DecodeExactFrame(stdout.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	var response handoffPutResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatal(err)
	}
	if err := launch.ValidateHandoffID(response.ID); err != nil {
		t.Fatal(err)
	}
}

func TestRunHandoffPutRejectsInvalidArgs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runHandoff(&stdout, &stderr, strings.NewReader("x"), []string{
		"put",
		"--agent", "opencode",
		"--session", "9c73be46-52af-4b1d-9ee7-123456789abc",
	})
	if code != 2 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunLaunchRejectsInvalidArgs(t *testing.T) {
	cases := [][]string{
		{"--agent", "claude", "--session", "bad", "--mode", "resume"},
		{"--agent", "opencode", "--session", "9c73be46-52af-4b1d-9ee7-123456789abc", "--mode", "resume"},
		{"--agent", "claude", "--session", "9c73be46-52af-4b1d-9ee7-123456789abc", "--mode", "new"},
		{"--agent", "claude", "--session", "9c73be46-52af-4b1d-9ee7-123456789abc", "--mode", "resume", "extra"},
	}
	for _, args := range cases {
		var stdout, stderr bytes.Buffer
		code := runLaunch(&stdout, &stderr, args)
		if code != 2 {
			t.Fatalf("args=%v exit=%d stderr=%s", args, code, stderr.String())
		}
	}
}

func TestProbeRequiresLaunchCapabilityAndAvailabilityCheck(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runSnapshot(&stdout, &stderr, []string{"--probe"})
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	payload, err := remoteviewv1.DecodeExactFrame(stdout.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	probe, err := remoteviewv1.DecodeProbe(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(probe.Capabilities, remoteviewv1.CapabilityRemoteLaunch) {
		t.Fatal("probe lost remote-launch/v1")
	}
	// launchableAgents must come from runtime LookPath, never inferred from binary version.
	for _, agent := range probe.LaunchableAgents {
		if agent != remoteviewv1.AgentClaude && agent != remoteviewv1.AgentCodex {
			t.Fatalf("unexpected agent %q", agent)
		}
	}
}
