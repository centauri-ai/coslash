package remoteviewv1

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestViewRoundTripAllowsOptionalEnvironment(t *testing.T) {
	view := sampleView()
	view.Sessions[0].WorkingDirectory = nil
	view.Sessions[0].Repository = nil
	view.Sessions[0].Branch = nil
	data, err := Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Sessions[0].WorkingDirectory != nil || decoded.Sessions[0].Repository != nil || decoded.Sessions[0].Branch != nil {
		t.Fatalf("optional environment was not preserved: %#v", decoded.Sessions[0])
	}
}

func TestDecodeRejectsUnknownFieldsAndTrailingJSON(t *testing.T) {
	data, err := Marshal(sampleView())
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	raw["extra"] = true
	withExtra, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(withExtra); err == nil {
		t.Fatal("unknown field accepted")
	}
	if _, err := Decode(append(data, []byte("\n{}")...)); err == nil {
		t.Fatal("trailing JSON accepted")
	}
}

func TestDecodeRejectsInconsistentCoverageAndTruncation(t *testing.T) {
	view := sampleView()
	view.CoverageSinceMs = view.RequestedSinceMs + 1
	data, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(data); err == nil {
		t.Fatal("inconsistent coverage accepted")
	}
	view = sampleView()
	view.Truncated = true
	data, err = json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(data); err == nil {
		t.Fatal("truncated without reason accepted")
	}
}

func TestDecodeRejectsInvalidUTF8AndNegativeValues(t *testing.T) {
	view := sampleView()
	invalid := string([]byte{0xff, 0xfe})
	view.Sessions[0].Name = &invalid
	if err := ValidateView(view); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
	view = sampleView()
	view.Sessions[0].Counts.Turns = -1
	data, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(data); err == nil {
		t.Fatal("negative count accepted")
	}
}

func TestDecodeRejectsOversizedPayload(t *testing.T) {
	huge := bytes.Repeat([]byte{'a'}, MaxPayloadBytes+1)
	if _, err := Decode(huge); err == nil {
		t.Fatal("oversized payload accepted")
	}
}

func TestProbeAllowsAdditionalCapabilities(t *testing.T) {
	probe := Probe{
		SchemaVersion:    SchemaVersion,
		CollectorVersion: "dev",
		Capabilities:     []string{CapabilityRemoteView, "remote-launch/v1", "remote-diagnostics/v2"},
		LaunchableAgents: []string{AgentClaude},
		HostNowMs:        100,
		Host:             Host{OS: "linux", Arch: "amd64"},
	}
	data, err := MarshalProbe(probe)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeProbe(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Capabilities) != 3 {
		t.Fatalf("capabilities = %#v", decoded.Capabilities)
	}
}

func TestLinuxCutoffUsesElapsedWindow(t *testing.T) {
	hostNow := int64(2_000_000)
	since := int64(1_000_000)
	requestNow := int64(1_500_000)
	got := LinuxCutoffMs(hostNow, since, requestNow)
	want := hostNow - (requestNow - since)
	if got != want {
		t.Fatalf("cutoff = %d, want %d", got, want)
	}
	if LinuxCutoffMs(hostNow, 0, requestNow) != 0 {
		t.Fatal("full-history cutoff must be zero")
	}
}

func TestFitViewDropsOldestWholeSessions(t *testing.T) {
	view := sampleView()
	view.Sessions = nil
	for i := 0; i < 12; i++ {
		session := sampleSession()
		session.SourceSessionID = fmt.Sprintf("33333333-3333-3333-3333-%012d", i)
		session.LastActivityAtMs = int64(1000 + i)
		session.SessionStartedAtMs = session.LastActivityAtMs
		digest := make([]Digest, MaxDigestItems)
		for j := range digest {
			digest[j] = Digest{
				Turn:        j,
				Category:    "user",
				Description: strings.Repeat("d", MaxDigestTextBytes),
			}
		}
		session.Digest = digest
		view.Sessions = append(view.Sessions, session)
	}
	before, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) <= MaxPayloadBytes {
		t.Fatalf("test fixture is only %d bytes; want over %d", len(before), MaxPayloadBytes)
	}
	fitted, err := FitView(view)
	if err != nil {
		t.Fatal(err)
	}
	data, err := Marshal(fitted)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > MaxPayloadBytes {
		t.Fatalf("fitted size %d exceeds limit", len(data))
	}
	if !fitted.Truncated || fitted.TruncationReason == nil || *fitted.TruncationReason != TruncationReasonPayload {
		t.Fatalf("truncation = %v %#v", fitted.Truncated, fitted.TruncationReason)
	}
	if len(fitted.Sessions) == 0 || len(fitted.Sessions) >= len(view.Sessions) {
		t.Fatalf("kept %d of %d sessions", len(fitted.Sessions), len(view.Sessions))
	}
	for i := 1; i < len(fitted.Sessions); i++ {
		if fitted.Sessions[i-1].LastActivityAtMs < fitted.Sessions[i].LastActivityAtMs {
			t.Fatalf("sessions are not newest-first: %#v", fitted.Sessions)
		}
	}
}

func TestFrameRoundTripAndRejection(t *testing.T) {
	payload := []byte(`{"ok":true}`)
	framed, err := EncodeFrame(payload)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeExactFrame(framed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload = %q", got)
	}
	withPrefix := append([]byte("banner\n"), framed...)
	extracted, prefix, err := ExtractFrame(withPrefix)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(extracted, payload) || string(prefix) != "banner\n" {
		t.Fatalf("extract = %q prefix=%q", extracted, prefix)
	}
	if _, err := DecodeExactFrame(append(framed, []byte("x")...)); err == nil {
		t.Fatal("trailing content accepted")
	}
	if _, _, err := ExtractFrame([]byte("no frame here")); err == nil {
		t.Fatal("missing frame accepted")
	}
	duplicate := append(append([]byte{}, framed...), framed...)
	if _, _, err := ExtractFrame(duplicate); err == nil {
		t.Fatal("duplicate frame accepted")
	}
}

func sampleView() View {
	return View{
		SchemaVersion:    SchemaVersion,
		CollectorVersion: "dev",
		Capabilities:     []string{CapabilityRemoteView},
		LaunchableAgents: []string{AgentClaude, AgentCodex},
		RequestedSinceMs: 1000,
		RequestNowMs:     2000,
		HostNowMs:        2100,
		CollectedAtMs:    2050,
		CoverageSinceMs:  1000,
		Host:             Host{OS: "linux", Arch: "arm64"},
		Sessions:         []Session{sampleSession()},
	}
}

func sampleSession() Session {
	cwd := "/home/user/project"
	repo := "github.com/example/project"
	branch := "main"
	return Session{
		Agent:              AgentClaude,
		SourceSessionID:    "11111111-1111-1111-1111-111111111111",
		WorkingDirectory:   &cwd,
		Repository:         &repo,
		Branch:             &branch,
		SessionStartedAtMs: 1500,
		LastActivityAtMs:   1800,
		Counts:             Counts{},
		Usage: Usage{
			Models:         []ModelUsage{},
			UnpricedModels: []string{},
		},
		Digest:    []Digest{},
		Todos:     []Todo{},
		FileEdits: []FileEdit{},
		Commits:   []string{},
		Subagents: []Subagent{},
	}
}
