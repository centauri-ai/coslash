package syncv4

import (
	"errors"
	"net"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
)

func TestDeferReasonMapsConnectionRefusedToConnect(t *testing.T) {
	err := errors.Join(ErrStaleConsent, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connect: connection refused")})
	if got := DeferReason(err); got != hubclient.FailureConnect {
		t.Fatalf("DeferReason(connection refused) = %q, want %s", got, hubclient.FailureConnect)
	}
}

func TestDeferReasonUsesHubFailureCauses(t *testing.T) {
	for _, test := range []struct {
		err  error
		want string
	}{
		{hubclient.V4Problem{Code: "maintenance", HTTPStatus: 503}, hubclient.FailureHubUnavailable},
		{hubclient.V4Problem{Code: "device_removed", HTTPStatus: 403}, hubclient.FailureRejected},
	} {
		if got := DeferReason(test.err); got != test.want {
			t.Errorf("DeferReason(%v) = %q, want %q", test.err, got, test.want)
		}
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
