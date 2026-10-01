package synthesis

import (
	"context"
	"database/sql"
	"encoding/json"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/settings"
)

func TestOpenCodeComponentCostsSurviveBadOrMissingSiblings(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		want        int64
		coverage    string
	}{
		{"malformed tokens", `{"type":"step_finish","sessionID":"s","part":{"id":"p","cost":0.25,"tokens":{"input":"bad"}}}`, 250000, "complete"},
		{"missing cost", `{"type":"step_finish","sessionID":"s","part":{"id":"a","cost":0.01}}` + "\n" + `{"type":"step_finish","sessionID":"s","part":{"id":"b"}}`, 10000, "partial"},
		{"negative cost", `{"type":"step_finish","sessionID":"s","part":{"id":"a","cost":-0.25}}` + "\n" + `{"type":"step_finish","sessionID":"s","part":{"id":"b","cost":0.50}}`, 500000, "partial"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := parseOpenCodeUsage([]byte(tc.input), "", false)
			if got.ReportedCostMicroUSD == nil || *got.ReportedCostMicroUSD != tc.want || got.Coverage != tc.coverage {
				t.Fatalf("usage = %#v, want cost %d and coverage %s", got, tc.want, tc.coverage)
			}
		})
	}
}

func TestOpenCodeMatchingScratchAndStreamTotals(t *testing.T) {
	const first = `{"providerID":"openai","modelID":"gpt-5","cost":0.01,"tokens":{"input":2,"output":1,"cache":{"read":0,"write":0}},"time":{"completed":1}}`
	const second = `{"providerID":"openai","modelID":"gpt-5","cost":0.02,"tokens":{"input":3,"output":2,"cache":{"read":0,"write":0}},"time":{"completed":2}}`
	const step1 = `{"type":"step_finish","sessionID":"run","part":{"id":"p1","providerID":"openai","modelID":"gpt-5","cost":0.01,"tokens":{"input":2,"output":1,"cache":{"read":0,"write":0}}}}` + "\n"
	const step2 = `{"type":"step_finish","sessionID":"run","part":{"id":"p2","providerID":"openai","modelID":"gpt-5","cost":0.02,"tokens":{"input":3,"output":2,"cache":{"read":0,"write":0}}}}` + "\n"
	for _, v2 := range []bool{false, true} {
		for _, dbSteps := range []int{1, 2} {
			t.Run(string(rune('0'+dbSteps))+map[bool]string{false: "v1", true: "v2"}[v2], func(t *testing.T) {
				t.Setenv("COSLASH_HOME", t.TempDir())
				runner := &CLIRunner{Backend: settings.BackendOpenCode, Model: settings.OpenCodeDefaultModel, Timeout: time.Second, openCodeV2: v2}
				runner.exec = func(_ context.Context, spec commandSpec) ([]byte, error) {
					var path string
					for _, entry := range spec.env {
						if len(entry) > 12 && entry[:12] == "OPENCODE_DB=" {
							path = entry[12:]
						}
					}
					db, err := sql.Open("sqlite", path)
					if err != nil {
						t.Fatal(err)
					}
					defer db.Close()
					if v2 {
						_, err = db.Exec(`CREATE TABLE session_message (session_id TEXT, type TEXT, data TEXT)`)
					} else {
						_, err = db.Exec(`CREATE TABLE message (session_id TEXT, data TEXT)`)
					}
					if err != nil {
						t.Fatal(err)
					}
					insert := func(id, raw string) {
						var err error
						if v2 {
							var fields map[string]any
							if err = json.Unmarshal([]byte(raw), &fields); err != nil {
								t.Fatal(err)
							}
							delete(fields, "providerID")
							delete(fields, "modelID")
							fields["model"] = map[string]string{"providerID": "openai", "id": "gpt-5"}
							var data []byte
							data, err = json.Marshal(fields)
							if err == nil {
								_, err = db.Exec(`INSERT INTO session_message VALUES (?,?,?)`, id, "assistant", string(data))
							}
						} else {
							_, err = db.Exec(`INSERT INTO message VALUES (?,?)`, id, `{"role":"assistant",`+raw[1:])
						}
						if err != nil {
							t.Fatal(err)
						}
					}
					insert("run", first)
					if dbSteps == 2 {
						insert("run", second)
					}
					insert("other", `{"cost":99,"time":{"completed":3}}`)
					if dbSteps == 2 {
						return []byte(step1), &exec.ExitError{}
					}
					return []byte(step1 + step2), &exec.ExitError{}
				}
				got, err := runner.Run(context.Background(), "facts")
				if err == nil || got.Usage.ReportedCostMicroUSD == nil || *got.Usage.ReportedCostMicroUSD != 30000 || got.Usage.Tokens["openai/gpt-5"].InputTokens != 5 {
					t.Fatalf("dbSteps=%d v2=%v: usage=%#v err=%v", dbSteps, v2, got.Usage, err)
				}
			})
		}
	}
}

func TestOpenCodeScannerBoundsRetainsCompletedUsage(t *testing.T) {
	const summary = `{"goals":["ship"],"outcome":"done","keyDecisions":[],"nextStep":"review"}`
	opencode := `{"type":"text","part":{"id":"t","text":` + strconv.Quote(summary) + `}}` + "\n" +
		`{"type":"step_finish","sessionID":"s","part":{"id":"p","cost":0.01,"providerID":"openai","modelID":"gpt-5","tokens":{"input":7,"output":2,"cache":{"read":9,"write":1}}}}` + "\n"
	for _, tc := range []struct {
		name, backend, output, model string
		input, cost                  int64
	}{
		{"opencode", settings.BackendOpenCode, opencode, "openai/gpt-5", 7, 10000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &CLIRunner{Backend: tc.backend, Model: "gpt-5", Timeout: time.Second}
			runner.exec = func(context.Context, commandSpec) ([]byte, error) {
				return []byte(tc.output + strings.Repeat("x", 4<<20) + "\n"), nil
			}
			got, err := runner.Run(context.Background(), "facts")
			if err == nil || got.Usage.Coverage != "partial" || int64(got.Usage.Tokens[tc.model].InputTokens) != tc.input {
				t.Fatalf("usage = %#v, err = %v", got.Usage, err)
			}
			if tc.cost != 0 && (got.Usage.ReportedCostMicroUSD == nil || *got.Usage.ReportedCostMicroUSD != tc.cost) {
				t.Fatalf("reported cost = %#v", got.Usage)
			}
		})
	}
}
