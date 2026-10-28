package sessionbackupproducer

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	sessionbackupv1 "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
)

type piBoundaryContext struct {
	context.Context
	at      string
	hook    func()
	failure error
}

func (ctx *piBoundaryContext) Err() error {
	if ctx.hook != nil {
		frames := make([]uintptr, 32)
		n := runtime.Callers(2, frames)
		callers := runtime.CallersFrames(frames[:n])
		for {
			frame, more := callers.Next()
			if strings.HasSuffix(frame.Function, ctx.at) {
				hook := ctx.hook
				ctx.hook = nil
				hook()
				break
			}
			if !more {
				break
			}
		}
	}
	if ctx.failure != nil {
		return ctx.failure
	}
	return ctx.Context.Err()
}

func TestPiProjectionFailureClassifiesStableSource(t *testing.T) {
	for _, entrypoint := range []string{"prepare", "revision"} {
		for _, scenario := range []string{"root header drift", "ancestor header drift", "restored malformed copy", "restored during failure planning", "stable malformed root", "stable invalid annotation"} {
			t.Run(entrypoint+"/"+scenario, func(t *testing.T) {
				home, path, selection := piFixture(t)
				changedPath := path
				if scenario == "ancestor header drift" {
					changedPath = filepath.Join(home, "private", "parent.jsonl")
					row := piAssistant("a", "", "inherited", "model", 1)
					writePiTranscript(t, changedPath, "parent", "", row)
					writePiTranscript(t, path, selection.SessionID, changedPath, row, piAssistant("b", "a", "root", "model", 2))
				} else if scenario == "stable malformed root" {
					writePiTranscript(t, path, selection.SessionID, "", "{invalid}")
				} else if scenario == "stable invalid annotation" {
					writePiTranscript(t, path, selection.SessionID, "", piAssistant("bad/id", "", "invalid annotation", "model", 1))
				}
				inputs := piInputs(t, selection.SessionID)
				original, err := os.ReadFile(changedPath)
				if err != nil {
					t.Fatal(err)
				}
				ctx := &piBoundaryContext{Context: t.Context()}
				fired := false
				switch scenario {
				case "root header drift", "ancestor header drift":
					ctx.at = ".openPiInput"
					ctx.hook = func() {
						changed := bytes.Replace(original, []byte("project"), []byte("moved-project"), 1)
						if bytes.Equal(changed, original) {
							t.Fatal("fixture header did not change")
						}
						writeGrokInput(t, changedPath, changed)
						fired = true
					}
				case "restored malformed copy":
					ctx.at = ".(*contextReader).Read"
					ctx.hook = func() {
						writeGrokInput(t, path, append(append([]byte(nil), original...), []byte("{invalid}\n")...))
						ctx.at = ".(*piFrozen).project"
						ctx.hook = func() {
							writeGrokInput(t, path, original)
							fired = true
						}
					}
				case "restored during failure planning":
					ctx.at = ".openPiInput"
					ctx.hook = func() {
						writeGrokInput(t, path, append(append([]byte(nil), original...), []byte("{invalid}\n")...))
						ctx.at = ".PlanSourcesContext"
						ctx.hook = func() {
							writeGrokInput(t, path, original)
							fired = true
						}
					}
				}
				spool := t.TempDir()
				manager := grokManager(spool, home, nil)
				var failure error
				if entrypoint == "prepare" {
					_, failure = manager.Prepare(ctx, selection)
				} else {
					_, failure = PiSourceRevision(ctx, inputs)
				}
				transient := !strings.HasPrefix(scenario, "stable")
				var preparation *PreparationError
				if !errors.As(failure, &preparation) || len(preparation.Coverage.Problems) != 1 {
					t.Fatalf("projection failure = %v", failure)
				}
				problem := preparation.Coverage.Problems[0]
				want := sessionbackupv1.ProblemInvalid
				if transient {
					want = sessionbackupv1.ProblemUnstable
				}
				if problem.Code != want || problem.Retryable != transient || transient && !fired {
					t.Fatalf("problem = %+v, mutation fired = %t", problem, fired)
				}
				entries, err := os.ReadDir(spool)
				if err != nil || len(entries) != 0 {
					t.Fatalf("failed capture retained staging: %v, %v", entries, err)
				}
				if transient {
					if entrypoint == "prepare" {
						_, err = manager.Prepare(t.Context(), selection)
					} else {
						_, err = PiSourceRevision(t.Context(), piInputs(t, selection.SessionID))
					}
					if err != nil {
						t.Fatalf("unchanged live source failed fresh retry: %v", err)
					}
				}
			})
		}
	}
}

func TestPiProjectionFailurePreservesCancellation(t *testing.T) {
	for _, entrypoint := range []string{"prepare", "revision"} {
		for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
			t.Run(entrypoint+"/"+cause.Error(), func(t *testing.T) {
				home, path, selection := piFixture(t)
				writePiTranscript(t, path, selection.SessionID, "", "{invalid}")
				inputs := piInputs(t, selection.SessionID)
				parent, cancel := context.WithCancel(t.Context())
				defer cancel()
				fired := false
				ctx := &piBoundaryContext{Context: parent, at: ".(*piFrozen).verify"}
				ctx.hook = func() { fired = true; ctx.failure = cause; cancel() }
				var err error
				if entrypoint == "prepare" {
					_, err = grokManager(t.TempDir(), home, nil).Prepare(ctx, selection)
				} else {
					_, err = PiSourceRevision(ctx, inputs)
				}
				if !fired || !errors.Is(err, cause) {
					t.Fatalf("failure-path cancellation fired=%t: %v", fired, err)
				}
			})
		}
	}
}
