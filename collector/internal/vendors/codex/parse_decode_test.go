package codex

import "testing"

func TestDecodeCommandResultKeepsEmptyOutputOfWrapper(t *testing.T) {
	got := decodeCodexCommandResult(mustJSON(`{"output":"","exit_code":0}`))
	if got.output != "" || !got.succeeded {
		t.Fatalf("result = %+v", got)
	}
}

func TestDecodeCommandResultUnwrapsExecBlock(t *testing.T) {
	raw := mustJSON([]map[string]string{
		{"type": "input_text", "text": "Script completed\nWall time 0.2 seconds\nOutput:\n"},
		{"type": "input_text", "text": `{"chunk_id":"a1","exit_code":0,"output":"/work\n"}`},
	})
	got := decodeCodexCommandResult(raw)
	if want := "Script completed\nWall time 0.2 seconds\nOutput:\n\n/work\n"; got.output != want || !got.succeeded {
		t.Fatalf("result = %+v", got)
	}
}
