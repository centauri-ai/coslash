package review

import "testing"

func TestKeyIncludesSource(t *testing.T) {
	local := Key("local", "codex", "same-id")
	remote := Key("r_0123456789abcdef", "codex", "same-id")
	if local == remote {
		t.Fatal("reviews from different sources share state")
	}
}
