package opencode

import (
	"context"
	"path/filepath"
	"testing"
)

func TestEffectiveDatabaseRootExplicitOverrides(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PATH", "")
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	for _, override := range []string{filepath.Join(home, "custom.db"), "custom.db"} {
		t.Setenv("OPENCODE_DB", override)
		want := override
		if !filepath.IsAbs(want) {
			want = filepath.Join(home, "data", "opencode", want)
		}
		got, err := RootContext(context.Background())
		if err != nil || got != want {
			t.Fatalf("root=%q error=%v, want %q", got, err, want)
		}
	}
	for _, override := range []string{":memory:", "../escape.db"} {
		t.Setenv("OPENCODE_DB", override)
		if _, err := RootContext(context.Background()); err == nil {
			t.Fatal("accepted unverifiable override")
		}
	}
}

func TestBoundedCommandOutputRejectsOverflow(t *testing.T) {
	output := &boundedOutput{limit: 4}
	if _, err := output.Write([]byte("ok")); err != nil {
		t.Fatal(err)
	}
	if _, err := output.Write([]byte("too long")); err == nil {
		t.Fatal("accepted excess subprocess output")
	}
	if output.Len() != 2 {
		t.Fatal("buffer grew beyond the accepted prefix")
	}
}
