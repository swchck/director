// Package notifytest provides a conformance suite that every notify.Channel
// implementation is expected to pass.
package notifytest

import (
	"context"
	"testing"
	"time"

	"github.com/swchck/director/notify"
)

const (
	// deliverTimeout bounds how long a subtest waits for an event.
	deliverTimeout = 5 * time.Second

	// republishInterval retries the publish while waiting. Some transports
	// establish a subscription asynchronously (PostgreSQL issues LISTEN on a
	// background connection), so the first event can legitimately predate it.
	republishInterval = 100 * time.Millisecond
)

// NewPair returns two Channel instances connected to the same transport, as two
// replicas of a service would be. Returning the same instance twice is valid
// for a transport that is a single in-process value. Each call must be isolated
// from previous calls (e.g. a fresh channel name), and the implementation is
// responsible for cleanup via t.Cleanup.
type NewPair func(t *testing.T) (publisher, peer notify.Channel)

// RunContract runs the notify.Channel conformance suite against newPair.
func RunContract(t *testing.T, newPair NewPair) {
	t.Helper()

	t.Run("delivers to peer subscriber", func(t *testing.T) {
		pub, peer := newPair(t)

		got := deliver(t, pub, subscribe(t, peer), notify.Event{
			Action:     notify.ActionSync,
			Collection: "products",
			Version:    "v1",
		})
		if got.Collection != "products" || got.Action != notify.ActionSync {
			t.Errorf("peer received %+v, want sync/products", got)
		}
	})

	t.Run("delivers to publisher own subscription", func(t *testing.T) {
		pub, _ := newPair(t)

		got := deliver(t, pub, subscribe(t, pub), notify.Event{
			Action:     notify.ActionSync,
			Collection: "products",
			Version:    "v1",
		})
		if got.Collection != "products" {
			t.Errorf("publisher received %+v, want its own sync/products event", got)
		}
	})

	t.Run("preserves event fields", func(t *testing.T) {
		pub, peer := newPair(t)

		want := notify.Event{
			Action:     notify.ActionPrepare,
			Collection: "products",
			Version:    "2026-08-17T10:00:00Z",
			RoundID:    "round-1",
			InstanceID: "instance-1",
		}
		if got := deliver(t, pub, subscribe(t, peer), want); got != want {
			t.Errorf("round trip = %+v, want %+v", got, want)
		}
	})

	t.Run("delivers after resubscribe", func(t *testing.T) {
		pub, _ := newPair(t)

		subscribe(t, pub)
		got := deliver(t, pub, subscribe(t, pub), notify.Event{
			Action:     notify.ActionCommit,
			Collection: "products",
			Version:    "v2",
			RoundID:    "round-2",
		})
		if got.RoundID != "round-2" {
			t.Errorf("resubscribed channel received %+v, want round-2", got)
		}
	})
}

func subscribe(t *testing.T, ch notify.Channel) <-chan notify.Event {
	t.Helper()

	sub, err := ch.Subscribe(context.Background())
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	return sub
}

// deliver publishes event until sub yields one and returns it. Republishing is
// idempotent for every action, so retries cannot change the outcome.
func deliver(t *testing.T, pub notify.Channel, sub <-chan notify.Event, event notify.Event) notify.Event {
	t.Helper()

	deadline := time.After(deliverTimeout)
	for {
		if err := pub.Publish(context.Background(), event); err != nil {
			t.Fatalf("Publish: %v", err)
		}

		select {
		case got, ok := <-sub:
			if !ok {
				t.Fatal("subscription channel closed before an event arrived")
			}
			return got

		case <-time.After(republishInterval):
		case <-deadline:
			t.Fatalf("no event within %s", deliverTimeout)
		}
	}
}
