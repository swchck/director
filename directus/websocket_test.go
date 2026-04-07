package directus_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/swchck/director/directus"
)

func TestNewWSClient_SetsBaseURL(t *testing.T) {
	ws := directus.NewWSClient("https://example.com/", "token")
	if ws == nil {
		t.Fatal("expected non-nil WSClient")
	}
}

func TestNewWSClient_TrimsTrailingSlash(t *testing.T) {
	ws := directus.NewWSClient("https://example.com/", "token")
	if ws == nil {
		t.Fatal("expected non-nil WSClient")
	}
}

func TestNewWSClient_WithLogger(t *testing.T) {
	ws := directus.NewWSClient("https://example.com", "token", directus.WithWSLogger(nil))
	if ws == nil {
		t.Fatal("expected non-nil WSClient")
	}
}

func TestWSClient_Close_Idempotent(t *testing.T) {
	ws := directus.NewWSClient("https://example.com", "token")
	if err := ws.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}

	if err := ws.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

func TestWSClient_SubscribeWith_AfterClose(t *testing.T) {
	ws := directus.NewWSClient("https://example.com", "token")
	ws.Close()

	_, err := ws.SubscribeWith(context.TODO(), directus.WSSubscription{Collection: "test"})
	if err == nil {
		t.Fatal("expected error after close")
	}
}

func TestWSSubscription_Types(t *testing.T) {
	sub := directus.WSSubscription{
		Collection: "products",
		Query: &directus.SubscriptionQuery{
			Fields: []string{"*", "translations.*"},
			Event:  []string{"create", "update"},
		},
	}

	if sub.Collection != "products" {
		t.Errorf("Collection = %q", sub.Collection)
	}

	if len(sub.Query.Fields) != 2 {
		t.Errorf("Fields = %v", sub.Query.Fields)
	}

	if len(sub.Query.Event) != 2 {
		t.Errorf("Event = %v", sub.Query.Event)
	}
}

func TestChangeEvent_JSON(t *testing.T) {
	event := directus.ChangeEvent{
		Collection: "products",
		Action:     "create",
		Keys:       []string{"1", "2"},
		Data:       json.RawMessage(`[{"id":1}]`),
	}

	b, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}

	var parsed directus.ChangeEvent
	if err := json.Unmarshal(b, &parsed); err != nil {
		t.Fatal(err)
	}

	if parsed.Collection != "products" {
		t.Errorf("Collection = %q", parsed.Collection)
	}

	if parsed.Action != "create" {
		t.Errorf("Action = %q", parsed.Action)
	}

	if len(parsed.Keys) != 2 {
		t.Errorf("Keys = %v", parsed.Keys)
	}
}
