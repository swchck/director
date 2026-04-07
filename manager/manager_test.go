package manager_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/swchck/director/cache"
	"github.com/swchck/director/config"
	"github.com/swchck/director/directus"
	"github.com/swchck/director/manager"
	"github.com/swchck/director/notify"
	"github.com/swchck/director/storage"
)

// Mock implementations

type mockStorage struct {
	mu        sync.Mutex
	snapshots map[string]*storage.Snapshot
	applyLog  map[string]int
	lockHeld  bool
}

func newMockStorage() *mockStorage {
	return &mockStorage{
		snapshots: make(map[string]*storage.Snapshot),
		applyLog:  make(map[string]int),
	}
}

func (s *mockStorage) Migrate(_ context.Context) error { return nil }

func (s *mockStorage) SaveSnapshot(_ context.Context, collection, version string, content []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := collection + ":" + version
	s.snapshots[key] = &storage.Snapshot{
		Collection: collection,
		Version:    version,
		Content:    content,
		Status:     storage.StatusPending,
		CreatedAt:  time.Now(),
	}

	return nil
}

func (s *mockStorage) ActivateSnapshot(_ context.Context, collection, version string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Deactivate old.
	for k, snap := range s.snapshots {
		if snap.Collection == collection && snap.Status == storage.StatusActive {
			s.snapshots[k].Status = storage.StatusInactive
		}
	}

	key := collection + ":" + version
	if snap, ok := s.snapshots[key]; ok {
		snap.Status = storage.StatusActive
	}

	return nil
}

func (s *mockStorage) GetActiveSnapshot(_ context.Context, collection string) (*storage.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, snap := range s.snapshots {
		if snap.Collection == collection && snap.Status == storage.StatusActive {
			return snap, nil
		}
	}

	return nil, storage.ErrSnapshotNotFound
}

func (s *mockStorage) GetSnapshot(_ context.Context, collection, version string) (*storage.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := collection + ":" + version
	if snap, ok := s.snapshots[key]; ok {
		return snap, nil
	}

	return nil, storage.ErrSnapshotNotFound
}

func (s *mockStorage) FailSnapshot(_ context.Context, collection, version string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := collection + ":" + version
	if snap, ok := s.snapshots[key]; ok {
		snap.Status = storage.StatusFailed
	}

	return nil
}

func (s *mockStorage) LogApply(_ context.Context, _, collection, version, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if status == "applied" {
		key := collection + ":" + version
		s.applyLog[key]++
	}

	return nil
}

func (s *mockStorage) CountApplied(_ context.Context, collection, version string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := collection + ":" + version
	return s.applyLog[key], nil
}

func (s *mockStorage) AcquireLock(_ context.Context, _ int64) (func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.lockHeld {
		return nil, storage.ErrLockNotAcquired
	}

	s.lockHeld = true
	return func() {
		s.mu.Lock()
		s.lockHeld = false
		s.mu.Unlock()
	}, nil
}

type mockNotifier struct {
	mu     sync.Mutex
	events []notify.Event
	subCh  chan notify.Event
	closed bool
}

func newMockNotifier() *mockNotifier {
	return &mockNotifier{
		subCh: make(chan notify.Event, 32),
	}
}

func (n *mockNotifier) Publish(_ context.Context, event notify.Event) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.events = append(n.events, event)
	return nil
}

func (n *mockNotifier) Subscribe(_ context.Context) (<-chan notify.Event, error) {
	return n.subCh, nil
}

func (n *mockNotifier) Close() error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if !n.closed {
		n.closed = true
		close(n.subCh)
	}

	return nil
}

func (n *mockNotifier) publishedEvents() []notify.Event {
	n.mu.Lock()
	defer n.mu.Unlock()

	result := make([]notify.Event, len(n.events))
	copy(result, n.events)
	return result
}

type mockRegistry struct {
	mu    sync.Mutex
	count int
}

func newMockRegistry() *mockRegistry {
	return &mockRegistry{count: 1}
}

func (r *mockRegistry) Register(_ context.Context, _, _ string) error { return nil }
func (r *mockRegistry) Heartbeat(_ context.Context, _ string) error   { return nil }
func (r *mockRegistry) Deregister(_ context.Context, _ string) error  { return nil }

func (r *mockRegistry) AliveCount(_ context.Context, _ string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.count, nil
}

// Tests

type testArticle struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category"`
}

func TestManager_RegisterAndStart_SyncsFromDirectus(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Version check (sort=-date_updated&limit=1).
		if r.URL.Query().Get("limit") == "1" && r.URL.Query().Get("sort") == "-date_updated" {
			json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{"date_updated": now.Format(time.RFC3339)},
				},
			})
			return
		}

		// Full fetch.
		json.NewEncoder(w).Encode(map[string]any{
			"data": []testArticle{
				{ID: 1, Name: "Alpha", Category: "food"},
				{ID: 2, Name: "Beta", Category: "drink"},
			},
		})
	}))
	defer srv.Close()

	dc := directus.NewClient(srv.URL, "token")
	store := newMockStorage()
	notif := newMockNotifier()
	reg := newMockRegistry() // only this instance

	articles := config.NewCollection[testArticle]("articles")
	items := directus.NewItems[testArticle](dc, "articles")

	mgr := manager.New(store, notif, reg, manager.Options{
		PollInterval:             time.Hour, // long, we test initial sync only
		WaitConfirmationsTimeout: time.Second,
		ServiceName:              "test-svc",
	})

	manager.RegisterCollection(mgr, articles, items)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Start in goroutine (blocking call).
	errCh := make(chan error, 1)
	go func() {
		errCh <- mgr.Start(ctx)
	}()

	// Give it time to do initial sync.
	time.Sleep(500 * time.Millisecond)

	// Verify data was synced.
	if articles.Count() != 2 {
		t.Errorf("Count() = %d, want 2", articles.Count())
	}

	found, ok := articles.Find(func(b testArticle) bool { return b.ID == 1 })
	if !ok || found.Name != "Alpha" {
		t.Errorf("Find(1) = %+v, ok=%v", found, ok)
	}

	if articles.Version().IsZero() {
		t.Error("Version should not be zero after sync")
	}

	// Verify a sync notification was published.
	events := notif.publishedEvents()
	if len(events) == 0 {
		t.Error("expected at least one sync event published")
	} else if events[0].Action != "sync" || events[0].Collection != "articles" {
		t.Errorf("event = %+v, want sync/articles", events[0])
	}

	// Stop.
	cancel()
	<-errCh
}

func TestManager_NoConfigs_ReturnsError(t *testing.T) {
	store := newMockStorage()
	notif := newMockNotifier()
	reg := newMockRegistry()

	mgr := manager.New(store, notif, reg, manager.Options{ServiceName: "test"})

	err := mgr.Start(context.Background())
	if err == nil {
		t.Fatal("expected error for no configs")
	}
}

func TestManager_ViewRecomputesOnSync(t *testing.T) {
	callCount := 0
	now := time.Now().UTC().Truncate(time.Second)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Query().Get("limit") == "1" {
			json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{"date_updated": now.Format(time.RFC3339)},
				},
			})
			return
		}

		callCount++
		json.NewEncoder(w).Encode(map[string]any{
			"data": []testArticle{
				{ID: 1, Name: "Alpha", Category: "food"},
				{ID: 2, Name: "Beta", Category: "drink"},
				{ID: 3, Name: "Gamma", Category: "food"},
			},
		})
	}))
	defer srv.Close()

	dc := directus.NewClient(srv.URL, "token")
	store := newMockStorage()
	notif := newMockNotifier()
	reg := newMockRegistry()

	articles := config.NewCollection[testArticle]("articles")
	items := directus.NewItems[testArticle](dc, "articles")

	// Create a view BEFORE manager starts.
	foodView := config.NewView("food-only", articles,
		[]config.FilterOption[testArticle]{
			config.Where(func(b testArticle) bool { return b.Category == "food" }),
		},
	)

	mgr := manager.New(store, notif, reg, manager.Options{
		PollInterval:             time.Hour,
		WaitConfirmationsTimeout: time.Second,
		ServiceName:              "test-svc",
	})

	manager.RegisterCollection(mgr, articles, items)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go mgr.Start(ctx)
	time.Sleep(500 * time.Millisecond)

	// View should have recomputed from the synced data.
	if foodView.Count() != 2 {
		t.Errorf("food view Count() = %d, want 2 (Alpha + Gamma)", foodView.Count())
	}

	all := foodView.All()
	for _, b := range all {
		if b.Category != "food" {
			t.Errorf("food view contains non-food item: %+v", b)
		}
	}

	cancel()
}

func TestManager_WebSocket_TriggersImmediateSync(t *testing.T) {
	// The WS test works by simulating what happens when handleWSChange is called.
	// We can't easily mock WSClient.Subscribe (it connects to a real server),
	// but we can test the syncOne/handleWSChange path directly by running the
	// manager and injecting a ws-style trigger via SyncNow for the same code path.
	//
	// Instead, we test the full flow: start manager, then after initial sync
	// update the Directus server response and trigger SyncNow (same code path
	// as WS-triggered syncOne).

	fetchCount := 0
	now := time.Now().UTC().Truncate(time.Second)
	v2Time := now.Add(time.Hour)

	var mu sync.Mutex
	useV2 := false

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		mu.Lock()
		v2 := useV2
		mu.Unlock()

		if r.URL.Query().Get("limit") == "1" {
			ts := now
			if v2 {
				ts = v2Time
			}
			json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{"date_updated": ts.Format(time.RFC3339)},
				},
			})
			return
		}

		fetchCount++
		if v2 {
			json.NewEncoder(w).Encode(map[string]any{
				"data": []testArticle{
					{ID: 1, Name: "Updated", Category: "food"},
					{ID: 2, Name: "Beta", Category: "drink"},
					{ID: 3, Name: "New", Category: "food"},
				},
			})
		} else {
			json.NewEncoder(w).Encode(map[string]any{
				"data": []testArticle{
					{ID: 1, Name: "Alpha", Category: "food"},
				},
			})
		}
	}))
	defer srv.Close()

	dc := directus.NewClient(srv.URL, "token")
	store := newMockStorage()
	notif := newMockNotifier()
	reg := newMockRegistry()

	articles := config.NewCollection[testArticle]("articles")
	items := directus.NewItems[testArticle](dc, "articles")

	mgr := manager.New(store, notif, reg, manager.Options{
		PollInterval:             time.Hour, // very long — won't trigger during test
		WaitConfirmationsTimeout: 2 * time.Second,
		ServiceName:              "test-svc",
	})

	manager.RegisterCollection(mgr, articles, items)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	go mgr.Start(ctx)
	// Wait for initial sync to complete (includes ~500ms waitConfirmations tick).
	time.Sleep(2 * time.Second)

	// Initial sync: 1 item.
	if articles.Count() != 1 {
		t.Fatalf("initial: Count() = %d, want 1", articles.Count())
	}

	// Simulate Directus data change.
	mu.Lock()
	useV2 = true
	mu.Unlock()

	// Trigger immediate sync (same code path as WS-triggered syncOne → leaderSync).
	// SyncNow is synchronous — by the time it returns, data is swapped.
	mgr.SyncNow(ctx)

	// Should now have 3 items.
	if articles.Count() != 3 {
		t.Errorf("after sync: Count() = %d, want 3", articles.Count())
	}

	found, ok := articles.Find(func(b testArticle) bool { return b.ID == 1 })
	if !ok || found.Name != "Updated" {
		t.Errorf("after sync: Find(1) = %+v, ok=%v, want Name='Updated'", found, ok)
	}

	cancel()
}

func TestManager_Options_Defaults(t *testing.T) {
	store := newMockStorage()
	notif := newMockNotifier()
	reg := newMockRegistry()

	mgr := manager.New(store, notif, reg, manager.Options{ServiceName: "test"})

	if mgr.InstanceID() == "" {
		t.Error("InstanceID should be auto-generated")
	}
}

func TestManager_WithInstanceID(t *testing.T) {
	store := newMockStorage()
	notif := newMockNotifier()
	reg := newMockRegistry()

	mgr := manager.New(store, notif, reg, manager.Options{ServiceName: "test"},
		manager.WithInstanceID("custom-id"),
	)

	if mgr.InstanceID() != "custom-id" {
		t.Errorf("InstanceID = %q, want 'custom-id'", mgr.InstanceID())
	}
}

func TestManager_FollowerReceivesNotification(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Query().Get("limit") == "1" {
			json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"date_updated": now.Format(time.RFC3339)}},
			})
			return
		}

		json.NewEncoder(w).Encode(map[string]any{
			"data": []testArticle{{ID: 1, Name: "Alpha", Category: "food"}},
		})
	}))
	defer srv.Close()

	dc := directus.NewClient(srv.URL, "token")
	store := newMockStorage()
	notif := newMockNotifier()
	reg := newMockRegistry()

	articles := config.NewCollection[testArticle]("articles")
	items := directus.NewItems[testArticle](dc, "articles")

	mgr := manager.New(store, notif, reg, manager.Options{
		PollInterval:             time.Hour,
		WaitConfirmationsTimeout: time.Second,
		ServiceName:              "test-svc",
	})

	manager.RegisterCollection(mgr, articles, items)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	go mgr.Start(ctx)
	time.Sleep(2 * time.Second)

	if articles.Count() != 1 {
		t.Fatalf("initial: Count() = %d, want 1", articles.Count())
	}

	// Simulate a follower receiving a sync notification.
	snapshotContent, _ := json.Marshal([]testArticle{
		{ID: 1, Name: "Alpha"},
		{ID: 2, Name: "Beta"},
	})

	versionStr := now.Format(time.RFC3339Nano)
	_ = store.SaveSnapshot(ctx, "articles", versionStr, snapshotContent)

	notif.subCh <- notify.Event{
		Action:     "sync",
		Collection: "articles",
		Version:    versionStr,
	}

	time.Sleep(500 * time.Millisecond)
	cancel()
}

func TestManager_CacheLoadOnStartup(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Query().Get("limit") == "1" {
			json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"date_created": now.Format(time.RFC3339)}},
			})
			return
		}

		json.NewEncoder(w).Encode(map[string]any{
			"data": []testArticle{{ID: 1, Name: "FromDirectus"}},
		})
	}))
	defer srv.Close()

	dc := directus.NewClient(srv.URL, "token")
	store := newMockStorage()
	notif := newMockNotifier()
	reg := newMockRegistry()

	mockCache := &mockCacheImpl{
		entries: map[string]*cache.Entry{
			"articles": {
				Collection: "articles",
				Version:    now.Add(-time.Hour).Format(time.RFC3339Nano),
				Content:    []byte(`[{"id":99,"name":"FromCache","category":"cached"}]`),
			},
		},
	}

	articles := config.NewCollection[testArticle]("articles")
	items := directus.NewItems[testArticle](dc, "articles")

	mgr := manager.New(store, notif, reg, manager.Options{
		PollInterval:             time.Hour,
		WaitConfirmationsTimeout: time.Second,
		ServiceName:              "test-svc",
	},
		manager.WithCache(mockCache, cache.ReadWriteThrough),
	)

	manager.RegisterCollection(mgr, articles, items)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	go mgr.Start(ctx)
	time.Sleep(2 * time.Second)

	// After startup, data should be from Directus (source of truth).
	found, ok := articles.Find(func(b testArticle) bool { return b.Name == "FromDirectus" })
	if !ok {
		t.Error("expected data from Directus after full sync")
	} else if found.ID != 1 {
		t.Errorf("found = %+v", found)
	}

	cancel()
}

type mockCacheImpl struct {
	mu      sync.Mutex
	entries map[string]*cache.Entry
}

func (c *mockCacheImpl) Get(_ context.Context, collection string) (*cache.Entry, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	e, ok := c.entries[collection]
	if !ok {
		return nil, cache.ErrCacheMiss
	}

	return e, nil
}

func (c *mockCacheImpl) Set(_ context.Context, entry cache.Entry) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[entry.Collection] = &entry

	return nil
}

func (c *mockCacheImpl) Delete(_ context.Context, _ string) error { return nil }
func (c *mockCacheImpl) Close() error                             { return nil }
