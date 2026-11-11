package workspaceeventsapi

import "testing"

func TestUnit_DiscardQueued_EmptiesTheWakeQueue(t *testing.T) {
	wake := make(chan []byte, 4)
	wake <- nil
	wake <- nil
	wake <- nil
	if !discardQueued(wake) {
		t.Fatal("an open channel must keep the stream going")
	}
	if len(wake) != 0 {
		t.Fatalf("%d wakes left queued", len(wake))
	}
}

func TestUnit_DiscardQueued_ReportsAClosedSubscription(t *testing.T) {
	wake := make(chan []byte, 1)
	close(wake)
	if discardQueued(wake) {
		t.Fatal("a closed subscription must end the stream")
	}
}
