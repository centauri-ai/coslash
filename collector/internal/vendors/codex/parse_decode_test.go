package codex

import "testing"

func TestDecodeCommandResultKeepsEmptyOutputOfWrapper(t *testing.T) {
	got := decodeCodexCommandResult(mustJSON(`{"output":"","exit_code":0}`))
	if got.output != "" || !got.succeeded {
		t.Fatalf("result = %+v", got)
	}
}
