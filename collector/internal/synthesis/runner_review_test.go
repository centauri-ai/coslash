package synthesis

import (
	"context"
	"database/sql"
	"encoding/json"
	"os/exec"
	"path/filepath"
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

func TestOpenCodeScratchCostSurvivesMalformedTokens(t *testing.T) {
	for _, v2 := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "usage.db")
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		table := `CREATE TABLE message (session_id TEXT, data TEXT)`
		insert := `INSERT INTO message VALUES (?,?)`
		part := `{"role":"assistant","providerID":"openai","modelID":"gpt-5","cost":0.25,"tokens":{"input":"bad"},"time":{"completed":1}}`
		if v2 {
			table = `CREATE TABLE session_message (session_id TEXT, type TEXT, data TEXT)`
			insert = `INSERT INTO session_message VALUES (?,"assistant",?)`
			part = `{"model":{"providerID":"openai","id":"gpt-5"},"cost":0.25,"tokens":{"input":"bad"},"time":{"completed":1}}`
		}
		if _, err = db.Exec(table); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(insert, "run", part); err != nil {
			t.Fatal(err)
		}
		if err = db.Close(); err != nil {
			t.Fatal(err)
		}
		got, count, incomplete := readOpenCodeScratchUsage(path, "run", v2)
		if incomplete || count != 1 || got.ReportedCostMicroUSD == nil || *got.ReportedCostMicroUSD != 250000 || got.Tokens != nil {
			t.Fatalf("v2=%v: count=%d usage=%#v", v2, count, got)
		}
	}
}

func TestOpenCodeScratchSkipsUnsafeRowsAndKeepsKnownUsage(t *testing.T) {
	const start = `{"role":"assistant","cost":0.3,"time":{"completed":1},"padding":"`
	const end = `"}`
	for _, tc := range []struct {
		name, extra string
		want        int64
	}{
		{"oversized", start + strings.Repeat("x", 262144) + end, 20000},
		{"multibyte", `{"role":"assistant","cost":0.4,"time":{"completed":1},"padding":"` + strings.Repeat("界", 90000) + `"}`, 20000},
		{"malformed", `{"role":"assistant","cost":0.3,"time":{"completed":1}`, 20000},
		{"byte-limit", start + strings.Repeat("x", 262144-len(start)-len(end)) + end, 320000},
	} {
		for _, v2 := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "-v1", true: "-v2"}[v2], func(t *testing.T) {
				t.Setenv("COSLASH_HOME", t.TempDir())
				runner := &CLIRunner{Backend: settings.BackendOpenCode, Model: settings.OpenCodeDefaultModel, Timeout: time.Second, openCodeV2: v2}
				runner.exec = func(_ context.Context, spec commandSpec) ([]byte, error) {
					var path string
					for _, entry := range spec.env {
						if strings.HasPrefix(entry, "OPENCODE_DB=") {
							path = strings.TrimPrefix(entry, "OPENCODE_DB=")
						}
					}
					db, err := sql.Open("sqlite", path)
					if err != nil {
						t.Fatal(err)
					}
					defer db.Close()
					table, insert := `CREATE TABLE message (session_id TEXT, data TEXT)`, `INSERT INTO message VALUES (?,?)`
					valid := `{"role":"assistant","providerID":"openai","modelID":"gpt-5","cost":0.02,"tokens":{"input":3,"output":2,"cache":{"read":0,"write":0}},"time":{"completed":2}}`
					if v2 {
						table, insert = `CREATE TABLE session_message (session_id TEXT, type TEXT, data TEXT)`, `INSERT INTO session_message VALUES (?,"assistant",?)`
						valid = `{"model":{"providerID":"openai","id":"gpt-5"},"cost":0.02,"tokens":{"input":3,"output":2,"cache":{"read":0,"write":0}},"time":{"completed":2}}`
					}
					if _, err := db.Exec(table); err != nil {
						t.Fatal(err)
					}
					for _, row := range []struct{ id, raw string }{{"run", tc.extra}, {"run", valid}, {"other", `{"role":"assistant","cost":99,"time":{"completed":3}}`}} {
						if _, err := db.Exec(insert, row.id, row.raw); err != nil {
							t.Fatal(err)
						}
					}
					user := `{"role":"user","cost":11,"time":{"completed":4}}`
					if v2 {
						_, err = db.Exec(`INSERT INTO session_message VALUES (?,"user",?)`, "run", user)
					} else {
						_, err = db.Exec(insert, "run", user)
					}
					if err != nil {
						t.Fatal(err)
					}
					return []byte(`{"type":"text","sessionID":"run","part":{"id":"reply","text":"broken"}}` + "\n"), nil
				}
				got, err := runner.Run(context.Background(), "facts")
				if err == nil || got.Usage.ReportedCostMicroUSD == nil || *got.Usage.ReportedCostMicroUSD != tc.want || got.Usage.Tokens["openai/gpt-5"].InputTokens != 3 || got.Usage.Coverage != "partial" {
					t.Fatalf("usage = %#v, err = %v", got.Usage, err)
				}
			})
		}
	}
}

func TestOpenCodeScratchUnsafeOnlyRemainsUnknown(t *testing.T) {
	for _, v2 := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "usage.db")
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		table, insert := `CREATE TABLE message (session_id TEXT, data TEXT)`, `INSERT INTO message VALUES (?,?)`
		if v2 {
			table, insert = `CREATE TABLE session_message (session_id TEXT, type TEXT, data TEXT)`, `INSERT INTO session_message VALUES (?,"assistant",?)`
		}
		if _, err = db.Exec(table); err == nil {
			_, err = db.Exec(insert, "run", `{"role":"assistant","padding":"`+strings.Repeat("x", 262144)+`"}`)
		}
		if err != nil {
			t.Fatal(err)
		}
		if err = db.Close(); err != nil {
			t.Fatal(err)
		}
		got, count, incomplete := readOpenCodeScratchUsage(path, "run", v2)
		if !incomplete || count != 0 || got.Coverage != "unknown" || got.ReportedCostMicroUSD != nil || got.Tokens != nil {
			t.Fatalf("v2=%v: count=%d incomplete=%v usage=%#v", v2, count, incomplete, got)
		}
	}
}

func TestOpenCodeScratchRowCapRetainsKnownSubtotal(t *testing.T) {
	for _, v2 := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "usage.db")
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		table, insert := `CREATE TABLE message (session_id TEXT, data TEXT)`, `INSERT INTO message VALUES (?,?)`
		if v2 {
			table, insert = `CREATE TABLE session_message (session_id TEXT, type TEXT, data TEXT)`, `INSERT INTO session_message VALUES (?,"assistant",?)`
		}
		if _, err = db.Exec(table); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 65; i++ {
			if _, err = db.Exec(insert, "run", `{"role":"assistant","cost":0.01,"time":{"completed":1}}`); err != nil {
				t.Fatal(err)
			}
		}
		if err = db.Close(); err != nil {
			t.Fatal(err)
		}
		got, count, incomplete := readOpenCodeScratchUsage(path, "run", v2)
		if !incomplete || count != 64 || got.Coverage != "partial" || got.ReportedCostMicroUSD == nil || *got.ReportedCostMicroUSD != 640000 {
			t.Fatalf("v2=%v: count=%d incomplete=%v usage=%#v", v2, count, incomplete, got)
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
		{"unpriced", settings.BackendOpenCode, strings.Replace(strings.Replace(opencode, `"cost":0.01,`, "", 1), "gpt-5", "future-model", 1), "openai/future-model", 7, 0},
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
			if tc.cost == 0 && (got.Usage.ReportedCostMicroUSD != nil || len(got.Usage.UnpricedModels) != 1) {
				t.Fatalf("unpriced usage = %#v", got.Usage)
			}
		})
	}
}
