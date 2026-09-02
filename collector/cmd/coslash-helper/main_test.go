package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/remoteprotocol"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestReadRequestRejectsSecondLine(t *testing.T) {
	request := remoteprotocol.Request{
		RequestID: "req-1", Protocol: remoteprotocol.VersionRange{Min: 1, Max: 1},
		Schema: remoteprotocol.VersionRange{Min: 1, Max: 1}, ParserVersion: vendors.ParserVersion,
		BaselineMode: remoteprotocol.BaselineKnown, BaselineID: "base-1",
		CollectedAtMs: 1, Vendors: []string{"codex"}, Limits: remoteprotocol.Limits{
			MaxRecordBytes: 1024, MaxResponseBytes: 4096, MaxRecords: 10, MaxInventoryFamilies: 10,
		},
	}
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readRequest(strings.NewReader(string(payload) + "\n{}\n")); err == nil {
		t.Fatal("accepted a second request line")
	}
}
