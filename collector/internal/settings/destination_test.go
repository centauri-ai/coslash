package settings

import "testing"

func TestParseSSHDestination(t *testing.T) {
	for _, test := range []struct {
		input string
		want  []string
	}{
		{"agent-box", []string{"agent-box"}},
		{"jane.doe@devvm1872.cln0", []string{"-l", "jane.doe", "devvm1872.cln0"}},
	} {
		destination, err := ParseSSHDestination(test.input)
		if err != nil {
			t.Fatalf("ParseSSHDestination(%q): %v", test.input, err)
		}
		if got := destination.Args(); len(got) != len(test.want) {
			t.Fatalf("Args(%q) = %#v, want %#v", test.input, got, test.want)
		} else {
			for i := range got {
				if got[i] != test.want[i] {
					t.Fatalf("Args(%q) = %#v, want %#v", test.input, got, test.want)
				}
			}
		}
	}
}

func TestParseSSHDestinationRejectsUnsafeForms(t *testing.T) {
	for _, input := range []string{"", "-oProxyCommand=x", "user@host@again", "user@host:22", "user@[::1]", "user @host", "user@host;id"} {
		if _, err := ParseSSHDestination(input); err == nil {
			t.Errorf("ParseSSHDestination(%q) succeeded", input)
		}
	}
}
