package opencode

import "testing"

func TestEarliestMessageTimeIgnoresMissingTimestamps(t *testing.T) {
	messages := make([]storedMessage, 4)
	messages[0].Time.Created = 0
	messages[1].Time.Created = 500
	messages[2].Time.Created = 200
	messages[3].Time.Created = -1

	if got := earliestMessageTime(messages); got != 200 {
		t.Fatalf("earliest message time = %d; want 200", got)
	}
}
