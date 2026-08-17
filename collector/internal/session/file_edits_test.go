package session

import "testing"

func TestFileEditSetOrdersFilesByMostRecentEdit(t *testing.T) {
	edits := NewFileEditSet()
	edits.Add("hot.go", 1, 0, false)
	edits.Add("cold.go", 2, 0, false)
	edits.Add("hot.go", 3, 1, false)

	if edits.Edits[0].Path != "cold.go" || edits.Edits[1].Path != "hot.go" {
		t.Fatalf("edit order = %#v", edits.Edits)
	}
	if hot := edits.Edits[1]; hot.Additions != 4 || hot.Deletions != 1 || hot.Edits != 2 {
		t.Fatalf("updated edit = %#v", hot)
	}

	edits.Add("cold.go", 1, 1, false)
	if edits.Edits[0].Path != "hot.go" || edits.Edits[1].Path != "cold.go" {
		t.Fatalf("second edit order = %#v", edits.Edits)
	}
}
