package syncv4

import (
	"errors"
	"net"
	"testing"
)

func TestDeferReasonMapsConnectionRefusedToHubUnreachable(t *testing.T) {
	err := errors.Join(ErrStaleConsent, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connect: connection refused")})
	if got := DeferReason(err); got != "hub_unreachable" {
		t.Fatalf("DeferReason(connection refused) = %q, want hub_unreachable", got)
	}
}
