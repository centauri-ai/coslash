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
