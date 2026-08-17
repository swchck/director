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

// ChangeEvent is a real-time subscription event from the Directus WebSocket.
// Collection is not on the wire — parseEvent recovers it from the subscription uid.
type ChangeEvent struct {
	Collection string          `json:"collection"`
	Action     string          `json:"action"` // "create", "update", "delete"
	Keys       []string        `json:"keys"`
	Data       json.RawMessage `json:"data"` // raw item data from the subscription
}

// WSSubscription configures a WebSocket subscription for a collection.
type WSSubscription struct {
	Collection string
	// Query selects the fields and relations included in event data.
	// If nil, no query is sent and Directus falls back to all fields.
	Query *SubscriptionQuery
}

// SubscriptionQuery mirrors the Directus subscription query parameter.
type SubscriptionQuery struct {
	Fields []string `json:"fields,omitempty"`
	// Event filters which events to subscribe to.
	// Default: ["create", "update", "delete"].
	Event []string `json:"event,omitempty"`
}

// WSClient subscribes to Directus real-time item changes and emits ChangeEvents,
// as a lower-latency alternative to polling.
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

// NewWSClient creates a client for Directus real-time subscriptions. baseURL is the
// root Directus URL; the /websocket endpoint and wss:// scheme are derived from it.
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

// Subscribe watches collections with Directus defaults (all fields, all events).
// The returned channel is closed when ctx is cancelled or Close is called.
func (ws *WSClient) Subscribe(ctx context.Context, collections ...string) (<-chan ChangeEvent, error) {
	subs := make([]WSSubscription, len(collections))
	for i, col := range collections {
		subs[i] = WSSubscription{Collection: col}
	}

	return ws.SubscribeWith(ctx, subs...)
}

// SubscribeWith is Subscribe with per-collection fields and event filters.
// See docs/directus-package.md for an example.
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

	authMsg := map[string]any{
		"type":         "auth",
		"access_token": ws.token,
	}

	if err := ws.writeJSON(subCtx, conn, authMsg); err != nil {
		cancel()
		_ = conn.Close(websocket.StatusNormalClosure, "auth failed")
		return nil, fmt.Errorf("directus: ws auth: %w", err)
	}

	if _, err := ws.readMessage(subCtx, conn); err != nil {
		cancel()
		_ = conn.Close(websocket.StatusNormalClosure, "auth failed")
		return nil, fmt.Errorf("directus: ws auth response: %w", err)
	}

	// Events carry only the subscription uid, so keep the mapping back.
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

// handlePing answers Directus keepalives and reports whether msg was one. They are
// JSON {"type":"ping"}, not WS frames, and go unanswered at the cost of the connection.
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
