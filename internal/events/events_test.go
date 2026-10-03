package events

import (
	"testing"
	"time"
)

func TestHubPublishSubscribe(t *testing.T) {
	h := NewHub()
	ch1, cancel1 := h.Subscribe()
	defer cancel1()
	ch2, cancel2 := h.Subscribe()
	defer cancel2()

	if h.SubscriberCount() != 2 {
		t.Fatalf("subscriber count = %d, want 2", h.SubscriberCount())
	}

	h.Publish(Event{Type: KindInsert, Table: "notes", ID: "1"})
	for i, ch := range []<-chan Event{ch1, ch2} {
		select {
		case e := <-ch:
			if e.ID != "1" || e.Table != "notes" {
				t.Fatalf("sub %d got %+v", i, e)
			}
		case <-time.After(time.Second):
			t.Fatalf("sub %d timed out waiting for event", i)
		}
	}
}

func TestHubCancel(t *testing.T) {
	h := NewHub()
	ch, cancel := h.Subscribe()
	cancel()
	if h.SubscriberCount() != 0 {
		t.Fatalf("count after cancel = %d, want 0", h.SubscriberCount())
	}
	// channel is closed
	if _, open := <-ch; open {
		t.Fatal("channel should be closed after cancel")
	}
	// publishing after cancel must not panic, and double-cancel is safe
	h.Publish(Event{Type: KindDelete, Table: "x", ID: "y"})
	cancel()
}

func TestHubDropsSlowSubscriber(t *testing.T) {
	h := NewHub()
	_, cancel := h.Subscribe() // never drained
	defer cancel()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			h.Publish(Event{Type: KindInsert, Table: "t", ID: "x"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a slow subscriber — must drop, not block")
	}
}
