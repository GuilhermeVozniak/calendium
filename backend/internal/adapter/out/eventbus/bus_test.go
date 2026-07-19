package eventbus

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"calendium/backend/internal/port"
)

// Compile-time proof the broker satisfies the port.
var _ port.EventBus = (*Bus)(nil)

func event(topic, typ, payload string) port.CollabEvent {
	return port.CollabEvent{Topic: topic, Type: typ, Payload: json.RawMessage(payload)}
}

// recv waits for one event with a deadline so a broken bus fails fast
// instead of hanging the suite.
func recv(t *testing.T, ch <-chan port.CollabEvent) port.CollabEvent {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatal("channel closed while waiting for an event")
		}
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for an event")
	}
	return port.CollabEvent{}
}

func TestPublishSubscribeRoundTrip(t *testing.T) {
	b := New()
	ch, cancel := b.Subscribe([]string{"team:t1", "user:u1"})
	defer cancel()

	b.Publish(event("team:t1", "comment.created", `{"id":"c1"}`))
	got := recv(t, ch)
	if got.Topic != "team:t1" || got.Type != "comment.created" || string(got.Payload) != `{"id":"c1"}` {
		t.Fatalf("unexpected event: %+v", got)
	}

	// A multi-topic subscriber receives events on every subscribed topic.
	b.Publish(event("user:u1", "mention", `{}`))
	if got := recv(t, ch); got.Topic != "user:u1" {
		t.Fatalf("want user:u1 event, got %+v", got)
	}
}

func TestTopicIsolation(t *testing.T) {
	b := New()
	mine, cancelMine := b.Subscribe([]string{"team:mine"})
	defer cancelMine()
	other, cancelOther := b.Subscribe([]string{"team:other"})
	defer cancelOther()

	// Publish a foreign-team event first: if scoping leaked, the marker
	// event published second would arrive after it.
	b.Publish(event("team:other", "comment.created", `{"foreign":true}`))
	b.Publish(event("team:mine", "comment.created", `{"marker":true}`))

	if got := recv(t, mine); got.Topic != "team:mine" {
		t.Fatalf("subscriber received another team's event: %+v", got)
	}
	select {
	case ev := <-mine:
		t.Fatalf("unexpected extra event: %+v", ev)
	default:
	}
	if got := recv(t, other); string(got.Payload) != `{"foreign":true}` {
		t.Fatalf("other-team subscriber got wrong event: %+v", got)
	}
}

func TestPublishNeverBlocksOnFullSubscriber(t *testing.T) {
	b := New()
	ch, cancel := b.Subscribe([]string{"team:t1"})
	defer cancel()

	const extra = 10
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < subscriberBuffer+extra; i++ {
			b.Publish(event("team:t1", "comment.created", fmt.Sprintf(`{"i":%d}`, i)))
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a full subscriber buffer")
	}

	// Exactly the buffered events survive; the overflow was dropped.
	count := 0
	for drained := false; !drained; {
		select {
		case <-ch:
			count++
		default:
			drained = true
		}
	}
	if count != subscriberBuffer {
		t.Fatalf("want %d buffered events, got %d", subscriberBuffer, count)
	}
}

func TestCancelClosesChannelAndStopsDelivery(t *testing.T) {
	b := New()
	ch, cancel := b.Subscribe([]string{"team:t1"})

	cancel()
	if _, ok := <-ch; ok {
		t.Fatal("channel still open after cancel")
	}

	// Publishing after cancel must not panic (send on closed channel).
	b.Publish(event("team:t1", "comment.created", `{}`))

	// cancel is idempotent.
	cancel()
}

func TestPublishWithoutSubscribersIsANoOp(t *testing.T) {
	b := New()
	b.Publish(event("team:t1", "comment.created", `{}`)) // must not panic or block
}

// TestConcurrentPublishSubscribeCancel exercises the add/remove/publish
// races under -race: publishers fan out while subscribers churn, including
// cancels that race in-flight publishes (guards send-on-closed-channel).
func TestConcurrentPublishSubscribeCancel(t *testing.T) {
	b := New()
	var wg sync.WaitGroup

	for p := 0; p < 8; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				b.Publish(event(fmt.Sprintf("team:t%d", i%4), "activity.updated", `{}`))
			}
		}(p)
	}

	for s := 0; s < 8; s++ {
		wg.Add(1)
		go func(s int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				ch, cancel := b.Subscribe([]string{fmt.Sprintf("team:t%d", (s+i)%4)})
				// Drain whatever is buffered, then cancel mid-stream.
				select {
				case <-ch:
				default:
				}
				cancel()
			}
		}(s)
	}

	wg.Wait()
}
