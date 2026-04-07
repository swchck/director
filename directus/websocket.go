package directus

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/coder/websocket"
	dlog "github.com/swchck/director/log"
)

// ChangeEvent represents a real-time subscription event from the Directus WebSocket.
//
// Directus sends events in the format:
//
//	{
//	    "type": "subscription",
//	    "event": "create",
//	    "data": [{"id": 1, "name": "New Item", ...}],
//	    "uid": "collection-uid"
//	}
//
// Data contains the full item payloads as returned by the subscription query.
type ChangeEvent struct {
	Collection string          `json:"collection"`
	Action     string          `json:"action"` // "create", "update", "delete"
	Keys       []string        `json:"keys"`
	Data       json.RawMessage `json:"data"` // raw item data from the subscription
}

// WSSubscription configures a WebSocket subscription for a collection.
type WSSubscription struct {
	Collection string
	// Query configures which fields and relations are included in event data.
	// If nil, defaults to all fields (*).
	Query *SubscriptionQuery
}

// SubscriptionQuery mirrors the Directus subscription query parameter.
type SubscriptionQuery struct {
	Fields []string `json:"fields,omitempty"`
	// Event filters which events to subscribe to.
	// Default: ["create", "update", "delete"].
	Event []string `json:"event,omitempty"`
}

// WSClient is an optional WebSocket client for Directus real-time subscriptions.
// It connects to the Directus WebSocket API and emits ChangeEvents when
// items are created, updated, or deleted.
//
// This is an alternative to polling — use it for lower-latency change detection.
type WSClient struct {
	baseURL string
	token   string
	logger  dlog.Logger

	mu     sync.Mutex
	closed bool
	cancel context.CancelFunc
}

// WSOption configures a WSClient.
type WSOption func(*WSClient)

// WithWSLogger sets the logger for the WebSocket client.
func WithWSLogger(logger dlog.Logger) WSOption {
	return func(ws *WSClient) {
		ws.logger = logger
	}
}

// NewWSClient creates a new WebSocket client for Directus real-time subscriptions.
//
// baseURL should be the root Directus URL (e.g. "https://directus.example.com").
// The client will connect to the /websocket endpoint.
func NewWSClient(baseURL, token string, opts ...WSOption) *WSClient {
	ws := &WSClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		logger:  dlog.Nop(),
	}

	for _, opt := range opts {
		opt(ws)
	}

	return ws
}

// Subscribe connects to the Directus WebSocket and subscribes to changes
// on the specified collections using default settings (all fields, all events).
// Returns a channel that receives ChangeEvents.
//
// The returned channel is closed when ctx is cancelled or Close is called.
func (ws *WSClient) Subscribe(ctx context.Context, collections ...string) (<-chan ChangeEvent, error) {
	subs := make([]WSSubscription, len(collections))
	for i, col := range collections {
		subs[i] = WSSubscription{Collection: col}
	}

	return ws.SubscribeWith(ctx, subs...)
}

// SubscribeWith connects to the Directus WebSocket and subscribes to changes
// using detailed subscription configuration (custom fields, event filters).
//
// Example — subscribe with specific fields for relational data:
//
//	ws.SubscribeWith(ctx,
//	    directus.WSSubscription{
//	        Collection: "articles",
//	        Query: &directus.SubscriptionQuery{
//	            Fields: []string{"*", "translations.*", "owner.*"},
//	        },
//	    },
//	    directus.WSSubscription{
//	        Collection: "app_config",
//	    },
//	)
func (ws *WSClient) SubscribeWith(ctx context.Context, subs ...WSSubscription) (<-chan ChangeEvent, error) {
	ws.mu.Lock()
	if ws.closed {
		ws.mu.Unlock()
		return nil, fmt.Errorf("directus: websocket client closed")
	}
	ws.mu.Unlock()

	wsURL := ws.websocketURL()

	conn, resp, err := websocket.Dial(ctx, wsURL, nil)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return nil, fmt.Errorf("directus: ws dial: %w", err)
	}

	subCtx, cancel := context.WithCancel(ctx)

	ws.mu.Lock()
	ws.cancel = cancel
	ws.mu.Unlock()

	// Authenticate.
	authMsg := map[string]any{
		"type":         "auth",
		"access_token": ws.token,
	}

	if err := ws.writeJSON(subCtx, conn, authMsg); err != nil {
		cancel()
		_ = conn.Close(websocket.StatusNormalClosure, "auth failed")
		return nil, fmt.Errorf("directus: ws auth: %w", err)
	}

	// Read auth response.
	if _, err := ws.readMessage(subCtx, conn); err != nil {
		cancel()
		_ = conn.Close(websocket.StatusNormalClosure, "auth failed")
		return nil, fmt.Errorf("directus: ws auth response: %w", err)
	}

	// Subscribe to each collection with a uid for identification.
	// Directus WS events don't include the collection name — they use uid to
	// identify which subscription the event belongs to.
	uidToCollection := make(map[string]string, len(subs))

	for _, sub := range subs {
		uid := "sub_" + sub.Collection
		uidToCollection[uid] = sub.Collection

		subMsg := ws.buildSubscribeMessage(sub, uid)

		if err := ws.writeJSON(subCtx, conn, subMsg); err != nil {
			cancel()
			_ = conn.Close(websocket.StatusNormalClosure, "subscribe failed")
			return nil, fmt.Errorf("directus: ws subscribe %s: %w", sub.Collection, err)
		}
	}

	ch := make(chan ChangeEvent, 32)

	go ws.readLoop(subCtx, conn, ch, uidToCollection)

	return ch, nil
}

// Close shuts down the WebSocket connection.
func (ws *WSClient) Close() error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	if ws.closed {
		return nil
	}

	ws.closed = true

	if ws.cancel != nil {
		ws.cancel()
	}

	return nil
}

func (ws *WSClient) buildSubscribeMessage(sub WSSubscription, uid string) map[string]any {
	msg := map[string]any{
		"type":       "subscribe",
		"collection": sub.Collection,
		"uid":        uid,
	}

	if sub.Query != nil {
		query := make(map[string]any)

		if len(sub.Query.Fields) > 0 {
			query["fields"] = sub.Query.Fields
		}

		if len(query) > 0 {
			msg["query"] = query
		}

		if len(sub.Query.Event) > 0 {
			msg["event"] = sub.Query.Event
		}
	}

	return msg
}

func (ws *WSClient) readLoop(ctx context.Context, conn *websocket.Conn, ch chan<- ChangeEvent, uidMap map[string]string) {
	defer close(ch)
	defer conn.Close(websocket.StatusNormalClosure, "closing") //nolint:errcheck // best-effort close in goroutine

	for {
		msg, err := ws.readMessage(ctx, conn)
		if err != nil {
			if ctx.Err() != nil {
				return
			}

			ws.logger.Error("directus: ws read failed", dlog.Err(err))
			return
		}

		// Directus sends {"type":"ping"} periodically to keep the connection alive.
		// We must respond with {"type":"pong"} or the server closes the connection.
		if ws.handlePing(ctx, conn, msg) {
			continue
		}

		event, ok := ws.parseEvent(msg, uidMap)
		if !ok {
			continue
		}

		select {
		case ch <- event:
		case <-ctx.Done():
			return
		}
	}
}

// handlePing checks if the message is a Directus ping and responds with pong.
// Returns true if the message was a ping (and was handled).
func (ws *WSClient) handlePing(ctx context.Context, conn *websocket.Conn, msg []byte) bool {
	var envelope struct {
		Type string `json:"type"`
	}

	if err := json.Unmarshal(msg, &envelope); err != nil {
		return false
	}

	if envelope.Type != "ping" {
		return false
	}

	pong := map[string]string{"type": "pong"}
	if err := ws.writeJSON(ctx, conn, pong); err != nil {
		ws.logger.Warn("directus: ws pong failed", dlog.Err(err))
	}

	return true
}

// wsMessage is the Directus WebSocket message envelope.
type wsMessage struct {
	Type  string          `json:"type"`
	UID   string          `json:"uid"`
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
	Keys  []string        `json:"keys"`
}

func (ws *WSClient) parseEvent(raw []byte, uidMap map[string]string) (ChangeEvent, bool) {
	var msg wsMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		ws.logger.Warn("directus: ws unmarshal", dlog.Err(err))
		return ChangeEvent{}, false
	}

	// Only process actual change events, not subscription confirmations ("init").
	if msg.Type != "subscription" || msg.Event == "" || msg.Event == "init" {
		return ChangeEvent{}, false
	}

	// Resolve collection from uid.
	collection := uidMap[msg.UID]

	return ChangeEvent{
		Collection: collection,
		Action:     msg.Event,
		Keys:       msg.Keys,
		Data:       msg.Data,
	}, true
}

func (ws *WSClient) writeJSON(ctx context.Context, conn *websocket.Conn, msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	return conn.Write(ctx, websocket.MessageText, data)
}

func (ws *WSClient) readMessage(ctx context.Context, conn *websocket.Conn) ([]byte, error) {
	_, data, err := conn.Read(ctx)
	if err != nil {
		return nil, err
	}

	return data, nil
}

func (ws *WSClient) websocketURL() string {
	url := ws.baseURL + "/websocket"
	url = strings.Replace(url, "https://", "wss://", 1)
	url = strings.Replace(url, "http://", "ws://", 1)

	return url
}
