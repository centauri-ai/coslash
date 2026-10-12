package syncv4

import (
	"errors"
	"net"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
)

func TestDeferReasonMapsConnectionRefusedToHubUnreachable(t *testing.T) {
	err := errors.Join(ErrStaleConsent, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connect: connection refused")})
	if got := DeferReason(err); got != "hub_unreachable" {
		t.Fatalf("DeferReason(connection refused) = %q, want hub_unreachable", got)
	}
}

func TestDeferReasonMapsNotPairedAndRevokedToNotPaired(t *testing.T) {
	for _, err := range []error{
		hubclient.ErrNotPaired,
		hubclient.V4Problem{Code: "device_revoked"},
	} {
		if got := DeferReason(err); got != "not_paired" {
			t.Errorf("DeferReason(%v) = %q, want not_paired", err, got)
		}
	}
}
