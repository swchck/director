package notify_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/swchck/director/notify"
)

// memoryChannel is a simple in-process implementation of notify.Channel.
type memoryChannel struct {
	mu          sync.Mutex
	subscribers []chan notify.Event
	closed      bool
}

func newMemoryChannel() *memoryChannel {
	return &memoryChannel{}
}

func (c *memoryChannel) Publish(_ context.Context, event notify.Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return notify.ErrClosed
	}

	for _, ch := range c.subscribers {
		select {
		case ch <- event:
		default:
			// drop if full
		}
	}

	return nil
}

func (c *memoryChannel) Subscribe(_ context.Context) (<-chan notify.Event, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil, notify.ErrClosed
	}

	ch := make(chan notify.Event, 16)
	c.subscribers = append(c.subscribers, ch)

	return ch, nil
}

func (c *memoryChannel) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}

	c.closed = true
	for _, ch := range c.subscribers {
		close(ch)
	}

	return nil
}

func TestMemoryChannel_PublishAndSubscribe(t *testing.T) {
	ch := newMemoryChannel()
	defer ch.Close()

	ctx := context.Background()
	sub, err := ch.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Subscribe() error: %v", err)
	}

	event := notify.Event{
		Action:     "sync",
		Collection: "products",
		Version:    "2025-01-01T00:00:00Z",
	}

	if err := ch.Publish(ctx, event); err != nil {
		t.Fatalf("Publish() error: %v", err)
	}

	select {
	case got := <-sub:
		if got.Action != "sync" {
			t.Errorf("Action = %q, want 'sync'", got.Action)
		}
		if got.Collection != "products" {
			t.Errorf("Collection = %q, want 'products'", got.Collection)
		}
		if got.Version != "2025-01-01T00:00:00Z" {
			t.Errorf("Version = %q, want '2025-01-01T00:00:00Z'", got.Version)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestMemoryChannel_MultipleSubscribers(t *testing.T) {
	ch := newMemoryChannel()
	defer ch.Close()

	ctx := context.Background()
	sub1, _ := ch.Subscribe(ctx)
	sub2, _ := ch.Subscribe(ctx)

	event := notify.Event{Action: "sync", Collection: "items", Version: "v1"}
	ch.Publish(ctx, event)

	for i, sub := range []<-chan notify.Event{sub1, sub2} {
		select {
		case got := <-sub:
			if got.Collection != "items" {
				t.Errorf("sub%d: Collection = %q, want 'items'", i+1, got.Collection)
			}
		case <-time.After(time.Second):
			t.Fatalf("sub%d: timed out", i+1)
		}
	}
}

func TestMemoryChannel_Close_ClosesSubscribers(t *testing.T) {
	ch := newMemoryChannel()

	ctx := context.Background()
	sub, _ := ch.Subscribe(ctx)

	ch.Close()

	// Channel should be closed — reading should return zero value and ok=false.
	select {
	case _, ok := <-sub:
		if ok {
			t.Error("expected channel to be closed")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for channel close")
	}
}

func TestMemoryChannel_PublishAfterClose_ReturnsError(t *testing.T) {
	ch := newMemoryChannel()
	ch.Close()

	err := ch.Publish(context.Background(), notify.Event{Action: "sync"})
	if !errors.Is(err, notify.ErrClosed) {
		t.Errorf("Publish after close: err = %v, want ErrClosed", err)
	}
}

func TestMemoryChannel_SubscribeAfterClose_ReturnsError(t *testing.T) {
	ch := newMemoryChannel()
	ch.Close()

	_, err := ch.Subscribe(context.Background())
	if !errors.Is(err, notify.ErrClosed) {
		t.Errorf("Subscribe after close: err = %v, want ErrClosed", err)
	}
}

func TestMemoryChannel_DoubleClose_Safe(t *testing.T) {
	ch := newMemoryChannel()
	ch.Close()
	// Should not panic
	ch.Close()
}

func TestMemoryChannel_ImplementsChannel(t *testing.T) {
	var _ notify.Channel = newMemoryChannel()
}

func TestEvent_Fields(t *testing.T) {
	e := notify.Event{
		Action:     "rollback",
		Collection: "settings",
		Version:    "2025-06-15T12:00:00Z",
	}

	if e.Action != "rollback" {
		t.Errorf("Action = %q", e.Action)
	}
	if e.Collection != "settings" {
		t.Errorf("Collection = %q", e.Collection)
	}
	if e.Version != "2025-06-15T12:00:00Z" {
		t.Errorf("Version = %q", e.Version)
	}
}

func TestMemoryChannel_ConcurrentPublish(t *testing.T) {
	ch := newMemoryChannel()
	defer ch.Close()

	ctx := context.Background()
	sub, _ := ch.Subscribe(ctx)

	const n = 100
	var wg sync.WaitGroup

	for range n {
		wg.Go(func() {
			ch.Publish(ctx, notify.Event{
				Action:     "sync",
				Collection: "test",
				Version:    "v1",
			})
		})
	}

	wg.Wait()

	// Drain all received events.
	received := 0
	for {
		select {
		case <-sub:
			received++
		default:
			goto done
		}
	}
done:
	if received == 0 {
		t.Error("expected to receive at least some events")
	}
}
