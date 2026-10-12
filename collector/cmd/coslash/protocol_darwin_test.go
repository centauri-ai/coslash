//go:build darwin

package main

import "testing"

func TestParseLaunchServicesDumpFindsOnlyCoslashHandlers(t *testing.T) {
	dump := `bundle id: 12
path: /Users/example/old/coSlash Local Pairing Handler.app (0xabc)
claimed schemes: 1
    scheme: coslash
bundle id: 13
path: /Applications/Other.app (0xdef)
claimed schemes: https:
bundle id: 14
path: /Users/example/new/coSlash Local Pairing Handler.app
claimed schemes: coslash:
`
	handlers := parseLaunchServicesDump(dump)
	if len(handlers) != 2 {
		t.Fatalf("handlers = %#v, want two coSlash registrations", handlers)
	}
	if handlers[0].Path != "/Users/example/old/coSlash Local Pairing Handler.app" || handlers[1].Path != "/Users/example/new/coSlash Local Pairing Handler.app" {
		t.Fatalf("handler paths = %#v", handlers)
	}
}
