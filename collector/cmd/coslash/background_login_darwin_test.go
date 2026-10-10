//go:build darwin

package main

import (
	"strings"
	"testing"
)

func TestBackgroundLoginPlistEscapesTheExecutable(t *testing.T) {
	contents, err := backgroundLoginPlist(`/Applications/coSlash & <Local>`)
	if err != nil {
		t.Fatal(err)
	}
	text := string(contents)
	if !strings.Contains(text, `/Applications/coSlash &amp; &lt;Local&gt;`) || !strings.Contains(text, "--background") {
		t.Fatalf("launch agent plist does not safely encode its program: %s", text)
	}
}
