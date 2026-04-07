package directus_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/swchck/director/directus"
)

// wsTestServer creates an httptest server that speaks the Directus WS protocol.
// It handles auth, subscriptions, sends a ping, and optionally sends a change event.
func wsTestServer(t *testing.T, sendEvent bool) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Logf("ws accept: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")

		ctx := r.Context()

		// 1. Read auth message.
		_, authData, err := conn.Read(ctx)
		if err != nil {
			return
		}

		var authMsg map[string]any
		json.Unmarshal(authData, &authMsg)
		if authMsg["type"] != "auth" {
			t.Errorf("expected auth message, got %v", authMsg)
			return
		}

		// Send auth response.
		authResp, _ := json.Marshal(map[string]any{
			"type":   "auth",
			"status": "ok",
		})
		conn.Write(ctx, websocket.MessageText, authResp)

		// 2. Read subscription messages.
		var subscriptions []map[string]any
		for {
			_, subData, err := conn.Read(ctx)
			if err != nil {
				return
			}

			var subMsg map[string]any
			json.Unmarshal(subData, &subMsg)

			if subMsg["type"] == "subscribe" {
				subscriptions = append(subscriptions, subMsg)
				// We expect exactly 1 subscription in our test.
				break
			}
		}

		// 3. Send a ping.
		pingMsg, _ := json.Marshal(map[string]string{"type": "ping"})
		conn.Write(ctx, websocket.MessageText, pingMsg)

		// Read pong response.
		_, pongData, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var pong map[string]string
		json.Unmarshal(pongData, &pong)
		if pong["type"] != "pong" {
			t.Errorf("expected pong, got %v", pong)
		}

		// 4. Send a change event if requested.
		if sendEvent && len(subscriptions) > 0 {
			uid := subscriptions[0]["uid"].(string)
			eventMsg, _ := json.Marshal(map[string]any{
				"type":  "subscription",
				"event": "create",
				"uid":   uid,
				"data":  []map[string]any{{"id": 1, "name": "New Item"}},
				"keys":  []string{"1"},
			})
			conn.Write(ctx, websocket.MessageText, eventMsg)
		}

		// Keep connection alive briefly to let client read.
		time.Sleep(200 * time.Millisecond)
	}))

	return srv
}

func TestWSClient_Subscribe_ReceivesEvent(t *testing.T) {
	srv := wsTestServer(t, true)
	defer srv.Close()

	// Convert http:// to ws://
	wsURL := strings.Replace(srv.URL, "http://", "http://", 1)

	ws := directus.NewWSClient(wsURL, "test-token")
	defer ws.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := ws.Subscribe(ctx, "products")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	select {
	case event := <-ch:
		if event.Collection != "products" {
			t.Errorf("Collection = %q, want 'products'", event.Collection)
		}
		if event.Action != "create" {
			t.Errorf("Action = %q, want 'create'", event.Action)
		}
		if len(event.Keys) != 1 || event.Keys[0] != "1" {
			t.Errorf("Keys = %v", event.Keys)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for event")
	}
}

func TestWSClient_Subscribe_PingPong(t *testing.T) {
	srv := wsTestServer(t, false)
	defer srv.Close()

	wsURL := strings.Replace(srv.URL, "http://", "http://", 1)

	ws := directus.NewWSClient(wsURL, "test-token")
	defer ws.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	ch, err := ws.Subscribe(ctx, "products")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	// Channel should close without any events (only ping/pong happened).
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("expected channel to close without events")
		}
		// Channel closed - OK
	case <-time.After(2 * time.Second):
		// Also OK - no events is expected
	}
}

func TestWSClient_SubscribeWith_CustomQuery(t *testing.T) {
	subCh := make(chan map[string]any, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")

		ctx := r.Context()

		// Auth
		conn.Read(ctx)
		authResp, _ := json.Marshal(map[string]any{"type": "auth", "status": "ok"})
		conn.Write(ctx, websocket.MessageText, authResp)

		// Read subscription
		_, subData, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var sub map[string]any
		json.Unmarshal(subData, &sub)
		subCh <- sub

		time.Sleep(100 * time.Millisecond)
	}))
	defer srv.Close()

	ws := directus.NewWSClient(srv.URL, "test-token")
	defer ws.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := ws.SubscribeWith(ctx, directus.WSSubscription{
		Collection: "products",
		Query: &directus.SubscriptionQuery{
			Fields: []string{"*", "translations.*"},
			Event:  []string{"create", "update"},
		},
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	var gotSubscription map[string]any
	select {
	case gotSubscription = <-subCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for subscription")
	}

	if gotSubscription["collection"] != "products" {
		t.Errorf("collection = %v", gotSubscription["collection"])
	}

	query, ok := gotSubscription["query"].(map[string]any)
	if !ok {
		t.Fatal("expected query in subscription")
	}

	fields, ok := query["fields"].([]any)
	if !ok || len(fields) != 2 {
		t.Errorf("fields = %v", query["fields"])
	}
}
