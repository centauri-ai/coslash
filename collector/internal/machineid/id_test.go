package machineid

import "testing"

func TestNormalize(t *testing.T) {
	if got := Normalize("  {BBBBBBBB-BBBB-4BBB-8BBB-BBBBBBBBBBBB}\n"); got != "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb" {
		t.Fatalf("uuid=%q", got)
	}
	if got := Normalize("0123456789abcdef0123456789abcdef"); got != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("hex=%q", got)
	}
	if got := Normalize("short"); got != "" {
		t.Fatalf("short=%q", got)
	}
}
