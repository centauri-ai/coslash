package syncv4

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadStoredBindingDoesNotRewriteQueue(t *testing.T) {
	root := t.TempDir()
	q, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	binding := strings.Repeat("a", 64)
	if err = q.Rebind(binding); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "queue.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ReadStoredBinding(root)
	if err != nil || got != binding {
		t.Fatalf("binding %q: %v", got, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatal("binding read changed queue")
	}
	if err = os.WriteFile(path, []byte(`{"version":1,"installId":"id","binding":`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = ReadStoredBinding(root); err == nil {
		t.Fatal("corrupt queue accepted")
	}
}
