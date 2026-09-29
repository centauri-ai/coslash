package hubclient

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestV4ListItemUsesFrozenWireFields(t *testing.T) {
	item := V4ListItem{V4Session: V4Session{InstallID: "install", LocalKeyHash: "key", Agent: "codex", Title: "title"},
		ActivityAt: time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC), ContentBytes: 42}
	data, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"installId", "localKeyHash", "agent", "title", "activityAt", "contentBytes"} {
		if _, ok := fields[required]; !ok {
			t.Fatalf("missing %s in %s", required, data)
		}
	}
	if _, ok := fields["live"]; ok {
		t.Fatalf("uncontracted live field in %s", data)
	}
}

func TestV4ListBatchRejectsInvalidCountBeforeRequest(t *testing.T) {
	client := &Client{}
	for _, count := range []int{0, 51} {
		if _, err := client.V4ListBatch(context.Background(), make([]V4ListItem, count)); err == nil {
			t.Fatalf("accepted %d listing items", count)
		}
	}
}
