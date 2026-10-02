package opencode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type deletionReadBoundaryContext struct {
	context.Context
	calls, at int
	action    func()
}

func (c *deletionReadBoundaryContext) Err() error {
	prior := c.Context.Err()
	c.calls++
	if c.calls == c.at {
		c.action()
	}
	return prior
}

func TestDeletionJSONOpenIdentityAndCancellation(t *testing.T) {
	for _, mode := range []string{"regular", "directory", "missing", "replacement", "canceled before open", "canceled after open", "canceled initially"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "receipt.json")
			if mode == "directory" {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			} else if mode != "missing" {
				if err := os.WriteFile(path, []byte(`{"Version":1}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := &deletionReadBoundaryContext{Context: parent, at: 2, action: cancel}
			switch mode {
			case "replacement":
				ctx.action = func() {
					if err := os.Rename(path, path+".original"); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte(`{"Version":2}`), 0600); err != nil {
						t.Fatal(err)
					}
				}
			case "canceled after open":
				ctx.at = 3
			case "canceled initially":
				cancel()
			case "canceled before open":
			default:
				ctx.at = 0
			}
			var journal deletionJournal
			found, err := readDeletionJSON(ctx, path, 16<<20, &journal)
			switch mode {
			case "regular":
				if err != nil || !found || journal.Version != 1 {
					t.Fatal("regular receipt rejected")
				}
			case "missing":
				if err != nil || found {
					t.Fatal("missing receipt misreported")
				}
			case "directory", "replacement":
				if err == nil || found {
					t.Fatal("nonregular/replaced receipt accepted")
				}
			default:
				if !errors.Is(err, context.Canceled) || found {
					t.Fatalf("cancellation lost: %v", err)
				}
			}
		})
	}
}
