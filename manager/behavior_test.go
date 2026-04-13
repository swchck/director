package manager_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/swchck/director/config"
	"github.com/swchck/director/directus"
	dlog "github.com/swchck/director/log"
	"github.com/swchck/director/manager"
	"github.com/swchck/director/notify"
	"github.com/swchck/director/storage"
)

// testLogWriter routes manager log output to t.Log.
type testLogWriter struct{ t *testing.T }

func (w testLogWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

// behaviorSource is a controllable source used by behavior tests to verify
// that the manager honors documented invariants (independence, rollback,
// register-after-Start panic).
type behaviorSource struct {
	name         string
	mu           sync.Mutex
	items        []twoPCArticle
	lastModified time.Time
	listCalls    atomic.Int32
}

func (s *behaviorSource) List(_ context.Context) ([]twoPCArticle, error) {
	s.listCalls.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]twoPCArticle, len(s.items))
	copy(out, s.items)
	return out, nil
}

func (s *behaviorSource) LastModified(_ context.Context) (time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastModified, nil
}

// TestManager_RollbackEvent_RevertsToActiveSnapshot:
// Documented behavior: "Rollback — if the confirmation timeout expires before
// all replicas confirm, leader publishes rollback; all replicas load the
// previous active snapshot and swap back."
//
// We test the follower-side behavior: when a `rollback` event arrives, the
// follower must load the currently-active snapshot from storage and swap
// to it, regardless of its current in-memory version.
func TestManager_RollbackEvent_RevertsToActiveSnapshot(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	store := newMockStorage()
	notif := newMockNotifier()
	reg := newMockRegistry()

	src := &behaviorSource{
		name:         "products",
		items:        []twoPCArticle{{ID: 1, Name: "v1"}},
		lastModified: now,
	}

	products := config.NewCollection[twoPCArticle]("products")

	mgr := manager.New(store, notif, reg, manager.Options{
		PollInterval:             time.Hour,
		WaitConfirmationsTimeout: 500 * time.Millisecond,
		ServiceName:              "test-svc",
	}, manager.WithInstanceID("inst-1"))
	manager.RegisterCollectionSource(mgr, products, src)

	errCh := make(chan error, 1)
	go func() { errCh <- mgr.Start(ctx) }()

	// Wait for initial sync to FULLY settle: data in memory AND snapshot
	// activated in storage. We must wait for ActivateSnapshot so our subsequent
	// activate doesn't race with it.
	initialVersion := config.NewVersion(now).String()
	waitFor(t, 3*time.Second, func() bool {
		if _, ok := products.Find(func(a twoPCArticle) bool { return a.ID == 1 }); !ok {
			return false
		}
		snap, err := store.GetActiveSnapshot(ctx, "products")
		return err == nil && snap.Version == initialVersion
	})
	if products.Count() != 1 {
		t.Fatalf("after initial sync: Count=%d, want 1", products.Count())
	}

	// Now seed a "rollback target" snapshot AFTER initial sync so it ends up
	// as the active snapshot (the one rollback handlers load).
	goodVersion := config.NewVersion(now.Add(-time.Hour)).String()
	goodPayload, _ := json.Marshal([]twoPCArticle{{ID: 99, Name: "rolled-back"}})
	if err := store.SaveSnapshot(ctx, "products", goodVersion, goodPayload); err != nil {
		t.Fatalf("SaveSnapshot good: %v", err)
	}
	if err := store.ActivateSnapshot(ctx, "products", goodVersion); err != nil {
		t.Fatalf("Activate good: %v", err)
	}

	// Send a rollback event.
	notif.subCh <- notify.Event{
		Action:     "rollback",
		Collection: "products",
		Version:    goodVersion,
	}

	// Manager should swap to the active snapshot (id=99).
	waitFor(t, 2*time.Second, func() bool {
		_, ok := products.Find(func(a twoPCArticle) bool { return a.ID == 99 })
		return ok
	})

	if _, ok := products.Find(func(a twoPCArticle) bool { return a.ID == 99 }); !ok {
		t.Errorf("rollback did not apply: items=%v", products.All())
	}
	if products.Count() != 1 {
		t.Errorf("after rollback Count=%d, want 1", products.Count())
	}

	cancel()
	<-errCh
}

// TestManager_RegisterAfterStart_Panics:
// Documented behavior: "Runtime guard: manager.register() panics if called
// after Start()."
func TestManager_RegisterAfterStart_Panics(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	store := newMockStorage()
	notif := newMockNotifier()
	reg := newMockRegistry()

	src := &behaviorSource{lastModified: time.Now().UTC()}
	first := config.NewCollection[twoPCArticle]("first")

	// Use Subscribe as a synchronization point: it is called by Start AFTER
	// the started flag is set, so once Subscribe is invoked we know the guard
	// is active.
	startedSig := make(chan struct{})
	signalNotif := &startSignalNotifier{
		mockNotifier: notif,
		signal:       startedSig,
	}

	mgr := manager.New(store, signalNotif, reg, manager.Options{
		PollInterval:             time.Hour,
		WaitConfirmationsTimeout: 500 * time.Millisecond,
		ServiceName:              "test-svc",
	})
	manager.RegisterCollectionSource(mgr, first, src)

	errCh := make(chan error, 1)
	go func() { errCh <- mgr.Start(ctx) }()

	select {
	case <-startedSig:
	case <-time.After(2 * time.Second):
		t.Fatal("Subscribe was never called — manager did not finish startup")
	}

	// Final assertion: registering a fresh collection now MUST panic.
	defer func() {
		r := recover()
		if r == nil {
			t.Error("expected panic on RegisterCollectionSource after Start, got nil")
		} else if msg, ok := r.(string); ok {
			if !strings.Contains(msg, "register called after Start") {
				t.Errorf("panic message = %q, want it to mention 'register called after Start'", msg)
			}
		}
		cancel()
		<-errCh
	}()

	late := config.NewCollection[twoPCArticle]("late")
	manager.RegisterCollectionSource(mgr, late, src)
}

// startSignalNotifier wraps mockNotifier and signals when Subscribe is called.
// Subscribe is invoked by Manager.Start AFTER `started` is set, so it is a
// safe synchronization point for "manager has fully entered run state".
type startSignalNotifier struct {
	*mockNotifier
	signal chan struct{}
	once   sync.Once
}

func (n *startSignalNotifier) Subscribe(ctx context.Context) (<-chan notify.Event, error) {
	n.once.Do(func() { close(n.signal) })
	return n.mockNotifier.Subscribe(ctx)
}

// TestManager_CollectionsAreIndependent: Documented behavior: "Each collection
// is independent — a change in collection A does NOT trigger sync/rebuild of
// collection B." Verified via List() call counts.
func TestManager_CollectionsAreIndependent(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	store := newMockStorage()
	notif := newMockNotifier()
	reg := newMockRegistry()

	srcA := &behaviorSource{name: "alpha", items: []twoPCArticle{{ID: 1}}, lastModified: now}
	srcB := &behaviorSource{name: "beta", items: []twoPCArticle{{ID: 100}}, lastModified: now}

	collA := config.NewCollection[twoPCArticle]("alpha")
	collB := config.NewCollection[twoPCArticle]("beta")

	mgr := manager.New(store, notif, reg, manager.Options{
		PollInterval:             time.Hour,
		WaitConfirmationsTimeout: 500 * time.Millisecond,
		ServiceName:              "test-svc",
	}, manager.WithInstanceID("inst-1"))
	manager.RegisterCollectionSource(mgr, collA, srcA)
	manager.RegisterCollectionSource(mgr, collB, srcB)

	errCh := make(chan error, 1)
	go func() { errCh <- mgr.Start(ctx) }()

	// Wait for initial sync to fetch both collections once.
	waitFor(t, 3*time.Second, func() bool {
		return srcA.listCalls.Load() >= 1 && srcB.listCalls.Load() >= 1
	})

	beforeA := srcA.listCalls.Load()
	beforeB := srcB.listCalls.Load()

	// Pre-seed a "newer" snapshot of alpha in storage so the follower can apply it.
	newAlphaVer := config.NewVersion(now.Add(time.Hour)).String()
	newAlphaPayload, _ := json.Marshal([]twoPCArticle{{ID: 1}, {ID: 2}})
	if err := store.SaveSnapshot(ctx, "alpha", newAlphaVer, newAlphaPayload); err != nil {
		t.Fatalf("SaveSnapshot alpha: %v", err)
	}

	// Send a sync event ONLY for alpha. beta must not be affected at all.
	notif.subCh <- notify.Event{
		Action:     "sync",
		Collection: "alpha",
		Version:    newAlphaVer,
	}

	// Wait for alpha to gain id=2.
	waitFor(t, 2*time.Second, func() bool {
		_, ok := collA.Find(func(a twoPCArticle) bool { return a.ID == 2 })
		return ok
	})

	// Beta's source must NOT have been re-fetched, and beta's data unchanged.
	if got := srcB.listCalls.Load(); got != beforeB {
		t.Errorf("beta List was called %d times after alpha sync (was %d) — collections are NOT independent", got, beforeB)
	}
	if collB.Count() != 1 {
		t.Errorf("beta count changed: %d, want 1", collB.Count())
	}
	if _, ok := collB.Find(func(a twoPCArticle) bool { return a.ID == 100 }); !ok {
		t.Errorf("beta lost original item id=100")
	}

	// Sanity: alpha did NOT re-fetch from source either (followers load from snapshot).
	if got := srcA.listCalls.Load(); got != beforeA {
		t.Errorf("alpha List was called %d times during follower sync (was %d) — should load from snapshot", got, beforeA)
	}

	cancel()
	<-errCh
}

// -- WebSocket behavior tests ---------------------------------------------

// wsBehaviorServer is a minimal Directus-protocol WS server. The events
// channel lets the test push subscription events; closing `closeAfter`
// disconnects the client to test fallback behavior.
type wsBehaviorServer struct {
	t             *testing.T
	srv           *httptest.Server
	uidCh         chan string  // first-subscription UID
	closeAfter    chan struct{} // fires → server forces connection close
	receivedSubs  atomic.Int32
}

func newWSBehaviorServer(t *testing.T, sendEvents func(uid string, write func(map[string]any))) *wsBehaviorServer {
	t.Helper()

	w := &wsBehaviorServer{
		t:          t,
		uidCh:      make(chan string, 4),
		closeAfter: make(chan struct{}),
	}

	w.srv = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(rw, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")

		ctx := r.Context()

		// 1. Read auth, reply ok.
		if _, _, err := conn.Read(ctx); err != nil {
			return
		}
		authResp, _ := json.Marshal(map[string]any{"type": "auth", "status": "ok"})
		_ = conn.Write(ctx, websocket.MessageText, authResp)

		// 2. Read exactly one subscribe message. The Manager subscribes once per
		// collection at startup; tests that need more subs would loop here.
		// We avoid a read-timeout pattern because the websocket library treats
		// a cancelled Read context as a fatal protocol error and closes the conn.
		var firstUID string
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var msg map[string]any
		if json.Unmarshal(data, &msg) == nil && msg["type"] == "subscribe" {
			w.receivedSubs.Add(1)
			if uid, ok := msg["uid"].(string); ok {
				firstUID = uid
				select {
				case w.uidCh <- uid:
				default:
				}
			}
		}

		// 3. Optionally send events using the captured UID.
		write := func(payload map[string]any) {
			b, _ := json.Marshal(payload)
			_ = conn.Write(ctx, websocket.MessageText, b)
		}
		if sendEvents != nil && firstUID != "" {
			sendEvents(firstUID, write)
		}

		// 4. Wait for either the test to ask us to close, or the context to end.
		select {
		case <-w.closeAfter:
			// Force-close the connection — manager's read loop should drop the channel.
			_ = conn.Close(websocket.StatusGoingAway, "test forced close")
		case <-ctx.Done():
		}
	}))

	return w
}

// httpDirectusServer is a minimal Directus REST stand-in for fetch+version.
func httpDirectusServer(t *testing.T, items []twoPCArticle, ts time.Time, fetchCounter *atomic.Int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("limit") == "1" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"date_updated": ts.Format(time.RFC3339)}},
			})
			return
		}
		if fetchCounter != nil {
			fetchCounter.Add(1)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": items})
	}))
}

// TestManager_WebSocketDebouncing_BulkEventsCollapseToOneSync: documented
// behavior: "WS events are debounced per collection (default 2s). During bulk
// operations: ... timer fires → sync 'products' ONCE — one refetch — one
// view recompute."
//
// Strategy: the WS test server fires 5 "create" events back-to-back. Even
// after the debounce window expires, the manager must call List() ONCE for
// the collection (not 5 times).
func TestManager_WebSocketDebouncing_BulkEventsCollapseToOneSync(t *testing.T) {
	// Skip on race-disabled builds because debounce timing is sensitive.
	now := time.Now().UTC().Truncate(time.Second)

	var fetches atomic.Int32
	rest := httpDirectusServer(t, []twoPCArticle{{ID: 1}}, now, &fetches)
	defer rest.Close()

	wsSrv := newWSBehaviorServer(t, func(uid string, write func(map[string]any)) {
		// Fire 5 events in rapid succession, all for the same uid.
		for i := range 5 {
			write(map[string]any{
				"type":  "subscription",
				"uid":   uid,
				"event": "create",
				"data":  []map[string]any{{"id": i}},
				"keys":  []string{"k"},
			})
			time.Sleep(20 * time.Millisecond)
		}
	})
	defer wsSrv.srv.Close()

	dc := directus.NewClient(rest.URL, "token")
	wsClient := directus.NewWSClient(wsSrv.srv.URL, "token")
	defer wsClient.Close()

	store := newMockStorage()
	notif := newMockNotifier()
	reg := newMockRegistry()

	products := config.NewCollection[twoPCArticle]("products")
	items := directus.NewItems[twoPCArticle](dc, "products")

	mgr := manager.New(store, notif, reg, manager.Options{
		PollInterval:             time.Hour,
		WSPollInterval:           time.Hour,
		WSDebounce:               300 * time.Millisecond, // shorter window for tests
		WaitConfirmationsTimeout: 200 * time.Millisecond,
		ServiceName:              "ws-svc",
	}, manager.WithWebSocket(wsClient),
		manager.WithLogger(dlog.NewSlog(slog.New(slog.NewTextHandler(testLogWriter{t: t}, &slog.HandlerOptions{Level: slog.LevelDebug})))),
	)
	manager.RegisterCollection(mgr, products, items)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- mgr.Start(ctx) }()

	// Initial sync issues exactly one List call.
	waitFor(t, 3*time.Second, func() bool { return fetches.Load() >= 1 })
	initial := fetches.Load()

	// Wait for the WS subscription to be received server-side, so we know
	// events sent after this point will reach the manager.
	select {
	case <-wsSrv.uidCh:
	case <-time.After(3 * time.Second):
		t.Fatal("WS subscription was never received by test server")
	}

	// Wait long enough for the burst + debounce window to fire (5*20ms + 300ms + slack).
	time.Sleep(1500 * time.Millisecond)

	got := fetches.Load() - initial
	if got != 1 {
		t.Errorf("debouncing failed: got %d List calls after WS burst, want exactly 1 (events collapsed)", got)
	}

	cancel()
	<-errCh
}

// TestManager_WebSocketFallback_OnChannelClose: documented behavior: "If the
// WebSocket connection drops: WS channel closes → Manager sets wsEvents=nil
// → poll ticker resets to normal PollInterval → no panics, no goroutine leaks."
//
// We assert: (1) no panic, (2) Start does not return an error before ctx is
// cancelled, (3) goroutine count is bounded after the WS dies.
func TestManager_WebSocketFallback_OnChannelClose(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	rest := httpDirectusServer(t, []twoPCArticle{{ID: 1}}, now, nil)
	defer rest.Close()

	wsSrv := newWSBehaviorServer(t, nil) // no events, just close on demand
	defer wsSrv.srv.Close()

	dc := directus.NewClient(rest.URL, "token")
	wsClient := directus.NewWSClient(wsSrv.srv.URL, "token")

	store := newMockStorage()
	notif := newMockNotifier()
	reg := newMockRegistry()

	products := config.NewCollection[twoPCArticle]("products")
	items := directus.NewItems[twoPCArticle](dc, "products")

	mgr := manager.New(store, notif, reg, manager.Options{
		PollInterval:             time.Hour,
		WSPollInterval:           time.Hour,
		WSDebounce:               -1, // disable debounce for this test
		WaitConfirmationsTimeout: 200 * time.Millisecond,
		ServiceName:              "ws-svc",
	}, manager.WithWebSocket(wsClient))
	manager.RegisterCollection(mgr, products, items)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				errCh <- &recoverErr{val: r}
				return
			}
		}()
		errCh <- mgr.Start(ctx)
	}()

	// Wait for the WS subscription to be established server-side.
	select {
	case <-wsSrv.uidCh:
	case <-time.After(3 * time.Second):
		t.Fatal("WS subscription was never received by test server")
	}

	// Snapshot goroutine count after subscription is up.
	runtime.GC()
	gBefore := runtime.NumGoroutine()

	// Force the server to drop the WS connection — manager must fall back gracefully.
	close(wsSrv.closeAfter)

	// Give the manager a moment to react and clean up.
	time.Sleep(600 * time.Millisecond)

	// No panic should have occurred.
	select {
	case err := <-errCh:
		// If Start returned, it must be due to context cancellation, not a WS-related error.
		var pe *recoverErr
		if errors.As(err, &pe) {
			t.Fatalf("manager panicked on WS close: %v", err)
		}
		t.Fatalf("manager exited prematurely on WS close: %v (ctx err: %v)", err, ctx.Err())
	default:
		// Expected: still running.
	}

	runtime.GC()
	gAfter := runtime.NumGoroutine()
	// Allow some slack — we just want to ensure no obvious leak (e.g. doubling).
	if gAfter > gBefore+5 {
		t.Errorf("goroutine leak suspected after WS close: before=%d after=%d", gBefore, gAfter)
	}

	// Cancel and verify clean shutdown.
	cancel()
	select {
	case err := <-errCh:
		var pe *recoverErr
		if errors.As(err, &pe) {
			t.Fatalf("manager panicked during shutdown: %v", err)
		}
		// context-cancelled error is acceptable
	case <-time.After(2 * time.Second):
		t.Error("manager did not shut down within 2s of ctx cancel")
	}
}

// recoverErr wraps a recovered panic value as an error.
type recoverErr struct{ val any }

func (e *recoverErr) Error() string { return "panic: \u00ab" }

// Sanity: behaviorSource implements source contract used elsewhere.
var _ = (*behaviorSource).List

// Sanity: storage interface still satisfied by mockStorage (used in this file).
var _ storage.Storage = (*mockStorage)(nil)
