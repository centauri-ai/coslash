package syncv4

import "testing"

func TestWaitReasonClosedSet(t *testing.T) {
	for _, test := range []struct {
		name string
		in   waitConditions
		want string
	}{
		{"ready", waitConditions{battery: -1}, ""},
		{"low battery", waitConditions{battery: 19}, "on_battery"},
		{"battery allowed", waitConditions{battery: 20}, ""},
		{"metered", waitConditions{battery: -1, metered: true}, "metered_network"},
		{"local pause", waitConditions{battery: -1, pausedLocal: true}, "paused_local"},
		{"Hub pause", waitConditions{battery: -1, pausedHub: true}, "paused_hub"},
		{"device off", waitConditions{battery: -1, deviceOff: true}, "device_off"},
		{"backoff", waitConditions{battery: -1, backoff: true}, "backoff"},
		{"condition unavailable", waitConditions{battery: -1, conditionErr: true}, "backoff"},
		{"space full", waitConditions{battery: -1, spaceFull: true}, "space_full"},
		{"update", waitConditions{battery: -1, updateRequired: true}, "update_required"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := waitReason(test.in); got != test.want {
				t.Fatalf("wait reason = %q, want %q", got, test.want)
			}
		})
	}
}
