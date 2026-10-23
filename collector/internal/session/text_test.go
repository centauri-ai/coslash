package session

import (
	"io"
	"strings"
	"testing"
	"testing/iotest"
	"unicode/utf8"
)

func TestTruncateOutput(t *testing.T) {
	exact := strings.Repeat("a", CommandOutputLimit)
	if got := TruncateOutput(exact); got != exact {
		t.Fatal("output at the limit must stay whole")
	}
	if got := TruncateOutput(""); got != "" {
		t.Fatalf("empty output = %q", got)
	}

	long := strings.Repeat("h", 8192) + strings.Repeat("m", 100) + strings.Repeat("t", 8192)
	want := strings.Repeat("h", 8192) + "\n… [100 bytes truncated] …\n" + strings.Repeat("t", 8192)
	if got := TruncateOutput(long); got != want {
		t.Fatalf("ascii truncation mismatch: %q", got[8100:8300])
	}

	// A 3-byte rune straddles both cut points: head and tail give up the split rune.
	runes := strings.Repeat("€", 20000)
	got := TruncateOutput(runes)
	if !utf8.ValidString(got) {
		t.Fatal("truncated output is not valid UTF-8")
	}
	wantRunes := strings.Repeat("€", 2730)
	if want := wantRunes + "\n… [43620 bytes truncated] …\n" + wantRunes; got != want {
		t.Fatalf("rune truncation mismatch: %d bytes", len(got))
	}

	if got := TruncateOutput("ok\xff"); got != "ok�" {
		t.Fatalf("invalid UTF-8 = %q", got)
	}
}

func TestCommandLogPairsOutputsByCallID(t *testing.T) {
	var log CommandLog
	if log.Outputs() != nil {
		t.Fatal("empty log must not report outputs")
	}
	log.Note("untracked", "")
	if log.Outputs() != nil {
		t.Fatal("Note alone must not report outputs")
	}
	log.NoteCall("a", "go test", "run tests")
	log.NoteCall("b", "sleep 9", "")
	log.NoteCall("", "echo hi", "")
	log.NoteCall("d", "true", "")
	log.AttachOutput("d", "")
	log.AttachOutput("a", "PASS\n")
	log.AttachOutput("a", "ignored duplicate")
	log.AttachOutput("zzz", "unknown id")

	outputs := log.Outputs()
	if len(outputs) != 5 || outputs[0] != nil {
		t.Fatalf("outputs = %v", outputs)
	}
	if outputs[1] == nil || *outputs[1] != "PASS\n" || outputs[2] != nil || outputs[3] != nil {
		t.Fatalf("pairing wrong: %v", outputs)
	}
	if outputs[4] == nil || *outputs[4] != "" {
		t.Fatal("empty output must be kept as a real empty string")
	}
	if got := log.Raw(); len(got) != 5 || got[1] != "go test" {
		t.Fatalf("Raw = %v", got)
	}
	if got := log.Labelled(); got[1].Label != "run tests" || got[1].Output != outputs[1] {
		t.Fatalf("Labelled = %+v", got[1])
	}
}

func TestTruncateOutputReaderMatchesTruncateOutput(t *testing.T) {
	inputs := []string{
		"",
		"short\n",
		strings.Repeat("a", CommandOutputLimit),
		strings.Repeat("a", CommandOutputLimit+1),
		strings.Repeat("€", 20000),
		strings.Repeat("é", 100000) + "x",
		strings.Repeat("ab😀", 70000),
	}
	for _, input := range inputs {
		want := TruncateOutput(input)
		for name, reader := range map[string]io.Reader{
			"whole": strings.NewReader(input),
			"byte":  iotest.OneByteReader(strings.NewReader(input)),
			"half":  iotest.HalfReader(strings.NewReader(input)),
		} {
			got, err := TruncateOutputReader(reader)
			if err != nil || got != want {
				t.Fatalf("%s reader, %d input bytes: err %v, match %v", name, len(input), err, got == want)
			}
		}
	}
	if _, err := TruncateOutputReader(iotest.ErrReader(io.ErrClosedPipe)); err == nil {
		t.Fatal("read error was swallowed")
	}
}
