package sessionexport

import (
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

func TestMeasureCorpusReportsSizesWithoutContent(t *testing.T) {
	repository := "github.com/centauri-ai/coslash"
	corpus := make([]session.Session, 100)
	for i := range corpus {
		name := strings.Repeat("x", i)
		corpus[i] = session.Session{Agent: "codex", ID: "sanitized", Name: &name, Repository: &repository, StartedAt: 1, Tokens: map[string]session.ModelTokens{}}
	}
	report, err := MeasureCorpus(corpus, func(int) BuildOptions { return BuildOptions{CollectorVersion: "0.1.0"} })
	if err != nil {
		t.Fatal(err)
	}
	if report.CorpusSize != 100 || report.P50Bytes == 0 || report.P95Bytes < report.P50Bytes || report.P99Bytes < report.P95Bytes || report.MaximumBytes < report.P99Bytes {
		t.Fatalf("report = %#v", report)
	}
	if report.CollectorVersion != "0.1.0" {
		t.Fatalf("collector version = %q", report.CollectorVersion)
	}
}

func TestMeasureCorpusRequiresInput(t *testing.T) {
	if _, err := MeasureCorpus(nil, func(int) BuildOptions { return BuildOptions{} }); err == nil {
		t.Fatal("empty corpus accepted")
	}
}

func TestMeasureCorpusSeparatesDegradationFromRejection(t *testing.T) {
	repository := "github.com/centauri-ai/coslash"
	todos := make([]session.Todo, maxTodoItems)
	for i := range todos {
		todos[i].Text = strings.Repeat("t", maxTodoTextBytes)
	}
	corpus := []session.Session{{
		Agent: "codex", ID: "heavy", Repository: &repository, StartedAt: 1,
		Tokens:         map[string]session.ModelTokens{},
		SessionDetails: session.SessionDetails{Todos: todos},
	}}
	report, err := MeasureCorpus(corpus, func(int) BuildOptions { return BuildOptions{CollectorVersion: "0.1.0"} })
	if err != nil {
		t.Fatal(err)
	}
	if report.MaximumBytes <= report.AggregateLimitBytes || report.Degraded != 1 || report.Rejected != 0 || report.FittedMaximumBytes > report.AggregateLimitBytes {
		t.Fatalf("report = %#v", report)
	}
}

func TestMeasureCorpusCountsUnexportableSessionsAsRejected(t *testing.T) {
	repository := "github.com/centauri-ai/coslash"
	corpus := []session.Session{
		{Agent: "codex", ID: "valid", Repository: &repository, StartedAt: 1, Tokens: map[string]session.ModelTokens{}},
		{Agent: "codex", ID: "missing-repository", StartedAt: 1, Tokens: map[string]session.ModelTokens{}},
	}

	report, err := MeasureCorpus(corpus, func(int) BuildOptions { return BuildOptions{CollectorVersion: "0.1.0"} })
	if err != nil {
		t.Fatal(err)
	}
	if report.Rejected != 1 || report.RejectionRate != 0.5 || report.MaximumBytes == 0 {
		t.Fatalf("report = %#v", report)
	}
}
