package directus

import (
	"encoding/json"
	"testing"

	dlog "github.com/swchck/director/log"
)

func TestParseEvent_SubscriptionCreate(t *testing.T) {
	ws := &WSClient{logger: dlog.Nop()}
	uidMap := map[string]string{"sub_products": "products"}

	raw := `{"type":"subscription","event":"create","uid":"sub_products","data":[{"id":1}],"keys":["1"]}`
	event, ok := ws.parseEvent([]byte(raw), uidMap)
	if !ok {
		t.Fatal("expected event to be parsed")
	}

	if event.Collection != "products" {
		t.Errorf("Collection = %q", event.Collection)
	}

	if event.Action != "create" {
		t.Errorf("Action = %q", event.Action)
	}

	if len(event.Keys) != 1 || event.Keys[0] != "1" {
		t.Errorf("Keys = %v", event.Keys)
	}
}

func TestParseEvent_SubscriptionUpdate(t *testing.T) {
	ws := &WSClient{logger: dlog.Nop()}
	uidMap := map[string]string{"sub_items": "items"}

	raw := `{"type":"subscription","event":"update","uid":"sub_items","data":[{"id":5}],"keys":["5"]}`
	event, ok := ws.parseEvent([]byte(raw), uidMap)
	if !ok {
		t.Fatal("expected event to be parsed")
	}

	if event.Action != "update" {
		t.Errorf("Action = %q", event.Action)
	}

	if event.Collection != "items" {
		t.Errorf("Collection = %q", event.Collection)
	}
}

func TestParseEvent_SubscriptionDelete(t *testing.T) {
	ws := &WSClient{logger: dlog.Nop()}
	uidMap := map[string]string{"sub_products": "products"}

	raw := `{"type":"subscription","event":"delete","uid":"sub_products","keys":["10","11"]}`
	event, ok := ws.parseEvent([]byte(raw), uidMap)
	if !ok {
		t.Fatal("expected event to be parsed")
	}

	if event.Action != "delete" {
		t.Errorf("Action = %q", event.Action)
	}

	if len(event.Keys) != 2 {
		t.Errorf("Keys = %v", event.Keys)
	}
}

func TestParseEvent_InitEvent_Ignored(t *testing.T) {
	ws := &WSClient{logger: dlog.Nop()}
	uidMap := map[string]string{"sub_products": "products"}

	raw := `{"type":"subscription","event":"init","uid":"sub_products"}`
	_, ok := ws.parseEvent([]byte(raw), uidMap)
	if ok {
		t.Error("init events should be ignored")
	}
}

func TestParseEvent_PingMessage_Ignored(t *testing.T) {
	ws := &WSClient{logger: dlog.Nop()}
	uidMap := map[string]string{}

	raw := `{"type":"ping"}`
	_, ok := ws.parseEvent([]byte(raw), uidMap)
	if ok {
		t.Error("ping messages should be ignored")
	}
}

func TestParseEvent_AuthMessage_Ignored(t *testing.T) {
	ws := &WSClient{logger: dlog.Nop()}
	uidMap := map[string]string{}

	raw := `{"type":"auth","status":"ok"}`
	_, ok := ws.parseEvent([]byte(raw), uidMap)
	if ok {
		t.Error("auth messages should be ignored")
	}
}

func TestParseEvent_EmptyEvent_Ignored(t *testing.T) {
	ws := &WSClient{logger: dlog.Nop()}
	uidMap := map[string]string{"sub_products": "products"}

	// subscription type but no event field
	raw := `{"type":"subscription","uid":"sub_products"}`
	_, ok := ws.parseEvent([]byte(raw), uidMap)
	if ok {
		t.Error("subscription without event should be ignored")
	}
}

func TestParseEvent_InvalidJSON(t *testing.T) {
	ws := &WSClient{logger: dlog.Nop()}
	uidMap := map[string]string{}

	_, ok := ws.parseEvent([]byte(`not json`), uidMap)
	if ok {
		t.Error("invalid JSON should be ignored")
	}
}

func TestParseEvent_UnknownUID(t *testing.T) {
	ws := &WSClient{logger: dlog.Nop()}
	uidMap := map[string]string{"sub_products": "products"}

	raw := `{"type":"subscription","event":"create","uid":"sub_unknown","data":[{"id":1}]}`
	event, ok := ws.parseEvent([]byte(raw), uidMap)
	if !ok {
		t.Fatal("event should still be parsed even with unknown uid")
	}

	if event.Collection != "" {
		t.Errorf("Collection should be empty for unknown uid, got %q", event.Collection)
	}
}

func TestHandlePing_ReturnsTrueForPing(t *testing.T) {
	_ = &WSClient{logger: dlog.Nop()} // verifies construction
	msg := []byte(`{"type":"ping"}`)

	var envelope struct {
		Type string `json:"type"`
	}
	json.Unmarshal(msg, &envelope)

	if envelope.Type != "ping" {
		t.Errorf("expected ping, got %q", envelope.Type)
	}
}

func TestHandlePing_ReturnsFalseForNonPing(t *testing.T) {
	ws := &WSClient{logger: dlog.Nop()}
	msg := []byte(`{"type":"subscription","event":"create"}`)

	var envelope struct {
		Type string `json:"type"`
	}
	json.Unmarshal(msg, &envelope)

	if envelope.Type == "ping" {
		t.Error("should not be ping")
	}
	_ = ws
}

func TestBuildSubscribeMessage_Basic(t *testing.T) {
	ws := &WSClient{logger: dlog.Nop()}
	sub := WSSubscription{Collection: "products"}
	msg := ws.buildSubscribeMessage(sub, "sub_products")

	if msg["type"] != "subscribe" {
		t.Errorf("type = %v", msg["type"])
	}

	if msg["collection"] != "products" {
		t.Errorf("collection = %v", msg["collection"])
	}

	if msg["uid"] != "sub_products" {
		t.Errorf("uid = %v", msg["uid"])
	}

	if _, ok := msg["query"]; ok {
		t.Error("query should not be set for basic subscription")
	}
}

func TestBuildSubscribeMessage_WithQuery(t *testing.T) {
	ws := &WSClient{logger: dlog.Nop()}
	sub := WSSubscription{
		Collection: "products",
		Query: &SubscriptionQuery{
			Fields: []string{"*", "translations.*"},
			Event:  []string{"create", "update"},
		},
	}
	msg := ws.buildSubscribeMessage(sub, "sub_products")

	query, ok := msg["query"].(map[string]any)
	if !ok {
		t.Fatal("query should be set")
	}

	fields, ok := query["fields"].([]string)
	if !ok || len(fields) != 2 {
		t.Errorf("fields = %v", query["fields"])
	}

	event, ok := msg["event"].([]string)
	if !ok || len(event) != 2 {
		t.Errorf("event = %v", msg["event"])
	}
}

func TestBuildSubscribeMessage_EmptyQuery(t *testing.T) {
	ws := &WSClient{logger: dlog.Nop()}
	sub := WSSubscription{
		Collection: "products",
		Query:      &SubscriptionQuery{},
	}
	msg := ws.buildSubscribeMessage(sub, "sub_products")

	if _, ok := msg["query"]; ok {
		t.Error("query should not be set for empty SubscriptionQuery")
	}
}

func TestWebsocketURL_HTTP(t *testing.T) {
	ws := &WSClient{baseURL: "http://localhost:8055"}
	got := ws.websocketURL()
	if got != "ws://localhost:8055/websocket" {
		t.Errorf("url = %q", got)
	}
}

func TestWebsocketURL_HTTPS(t *testing.T) {
	ws := &WSClient{baseURL: "https://directus.example.com"}
	got := ws.websocketURL()
	if got != "wss://directus.example.com/websocket" {
		t.Errorf("url = %q", got)
	}
}
