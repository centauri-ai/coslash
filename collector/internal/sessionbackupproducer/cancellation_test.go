package sessionbackupproducer

import (
	"context"
	"errors"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
	backup "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
)

// Interrupt composition itself without production hooks or wall-clock sleeps.
type compositionInterruptedContext struct {
	context.Context
	cancel      context.CancelFunc
	failure     error
	interrupted atomic.Bool
}

func (ctx *compositionInterruptedContext) Err() error {
	pc, _, _, _ := runtime.Caller(1)
	if strings.HasSuffix(runtime.FuncForPC(pc).Name(), ".cloneParsedFamilyContext") {
		ctx.interrupted.Store(true)
		ctx.cancel()
	}
	if ctx.interrupted.Load() {
		return ctx.failure
	}
	return ctx.Context.Err()
}

func TestPrepareCompositionInterruptionPreservesCauseAndLastGood(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, agent := range []string{vendors.AgentCodex, vendors.AgentClaude, vendors.AgentOpenCode, vendors.AgentCursor, vendors.AgentGrok} {
		t.Run(agent, func(t *testing.T) {
			var home string
			selection := localSelection()
			switch agent {
			case vendors.AgentCodex:
				home, _ = writeFamilyFixture(t, 0)
			case vendors.AgentClaude:
				home, _, _, selection = claudeFixture(t)
			case vendors.AgentOpenCode:
				_ = openCodeBackupDB(t)
				home = t.TempDir()
				selection = Selection{SourceKind: backup.SourceLocal, SourceID: "local", Agent: agent, SessionID: "root"}
			case vendors.AgentGrok:
				home, _, selection = grokFixture(t)
			case vendors.AgentCursor:
				home, _, _ = writeCursorBackupFixture(t, "cursor-ide")
				selection = Selection{SourceKind: backup.SourceLocal, SourceID: "local", Agent: agent, SessionID: cursorTestID}
			}
			spool := t.TempDir()
			manager := New(Options{Root: spool, LocalHome: func() (string, error) { return home, nil }, OpenSource: func(context.Context, Selection) (SourceHandle, error) {
				return SourceHandle{Source: vendors.LocalReadSource, Home: home}, nil
			}})
			good, err := manager.Prepare(t.Context(), selection)
			if err != nil {
				t.Fatal(err)
			}
			for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
				t.Run(cause.Error(), func(t *testing.T) {
					base, cancel := context.WithCancel(t.Context())
					defer cancel()
					ctx := &compositionInterruptedContext{Context: base, cancel: cancel, failure: cause}
					got, err := manager.Prepare(ctx, selection)
					if !ctx.interrupted.Load() || got != nil || !errors.Is(err, cause) {
						t.Fatalf("interrupted=%t prepared=%t error=%v", ctx.interrupted.Load(), got != nil, err)
					}
					entries, err := os.ReadDir(spool)
					if err != nil || len(entries) != 1 || entries[0].Name() != good.BundleID {
						t.Fatalf("cancelled capture changed spool: %v, %v", entries, err)
					}
					if _, err := manager.Open(good.BundleID); err != nil {
						t.Fatal(err)
					}
					recovered, err := manager.Prepare(t.Context(), selection)
					if err != nil || recovered.BundleID != good.BundleID {
						t.Fatalf("identical input recovery=%v, %v", recovered, err)
					}
				})
			}
		})
	}
}
