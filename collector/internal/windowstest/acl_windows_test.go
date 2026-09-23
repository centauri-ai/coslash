//go:build windows

package windowstest

import "testing"

func TestPrivateACLTrusteesCountsLocalSystemTwice(t *testing.T) {
	trustees := privateACLTrustees("S-1-5-18")
	if trustees["S-1-5-18"] != 2 || trustees["S-1-5-32-544"] != 1 || len(trustees) != 2 {
		t.Fatalf("LocalSystem trustees = %#v", trustees)
	}
}
