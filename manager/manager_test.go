package manager_test

import (
	"context"
	"encoding/json"
	"fmt"
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
	"github.com/swchck/director/notify/notifytest"
	"github.com/swchck/director/storage"
)

// Mock implementations

type mockStorage struct {
	mu          sync.Mutex
	snapshots   map[string]*storage.Snapshot
	applyLog    map[string]int                 // key = collection:version, counts "applied" only
	applyByStat map[string]map[string][]string // key = collection:version:status -> []instanceID
	lockHeld    bool

	// onGetSnapshot, when set, is invoked on every GetSnapshot call. Tests use
	// it to count redundant snapshot reads.
	onGetSnapshot func(collection, version string)

	// onActivateSnapshot, when set, gates ActivateSnapshot: a non-nil return
	// aborts activation, leaving snapshot statuses untouched.
	onActivateSnapshot func(collection, version string) error

	// onLogApply runs on every LogApply. Tests count writes that upsert semantics
	// hide, and fail one: a non-nil return replaces recording the row.
	onLogApply func(instanceID, collection, version, status string) error

	// onGetActiveSnapshot, when set, gates GetActiveSnapshot: a non-nil return
	// is surfaced to the caller instead of the stored snapshot.
	onGetActiveSnapshot func(collection string) error
}

func newMockStorage() *mockStorage {
	return &mockStorage{
		snapshots:   make(map[string]*storage.Snapshot),
		applyLog:    make(map[string]int),
		applyByStat: make(map[string]map[string][]string),
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
	hook := s.onActivateSnapshot
	s.mu.Unlock()

	if hook != nil {
		if err := hook(collection, version); err != nil {
			return err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

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

// forceActive marks a snapshot active without consulting onActivateSnapshot, modelling
// a transaction that commits server-side while the client sees a connection error.
func (s *mockStorage) forceActive(collection, version string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, snap := range s.snapshots {
		if snap.Collection == collection && snap.Status == storage.StatusActive {
			snap.Status = storage.StatusInactive
		}
	}

	if snap, ok := s.snapshots[collection+":"+version]; ok {
		snap.Status = storage.StatusActive
	}
}

// activeArticleVersions returns every active version of "articles". More than one, or
// none, means the snapshot lifecycle was left inconsistent.
func (s *mockStorage) activeArticleVersions() []string {
	const collection = "articles"

	s.mu.Lock()
	defer s.mu.Unlock()

	var out []string
	for _, snap := range s.snapshots {
		if snap.Collection == collection && snap.Status == storage.StatusActive {
			out = append(out, snap.Version)
		}
	}

	return out
}

func (s *mockStorage) GetActiveSnapshot(_ context.Context, collection string) (*storage.Snapshot, error) {
	s.mu.Lock()
	hook := s.onGetActiveSnapshot
	s.mu.Unlock()

	if hook != nil {
		if err := hook(collection); err != nil {
			return nil, err
		}
	}

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
	hook := s.onGetSnapshot
	s.mu.Unlock()

	if hook != nil {
		hook(collection, version)
	}

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

func (s *mockStorage) LogApply(_ context.Context, instanceID, collection, version, status string) error {
	s.mu.Lock()
	hook := s.onLogApply
	s.mu.Unlock()

	if hook != nil {
		if err := hook(instanceID, collection, version, status); err != nil {
			return err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if status == "applied" {
		key := collection + ":" + version
		s.applyLog[key]++
	}

	cvKey := collection + ":" + version
	if s.applyByStat[cvKey] == nil {
		s.applyByStat[cvKey] = make(map[string][]string)
	}
	// Remove this instance from other statuses for this (collection, version) — upsert semantics.
	for st, ids := range s.applyByStat[cvKey] {
		filtered := ids[:0]
		for _, id := range ids {
			if id != instanceID {
				filtered = append(filtered, id)
			}
		}
		s.applyByStat[cvKey][st] = filtered
	}
	s.applyByStat[cvKey][status] = append(s.applyByStat[cvKey][status], instanceID)

	return nil
}

func (s *mockStorage) CountApplied(_ context.Context, collection, version string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := collection + ":" + version
	return s.applyLog[key], nil
}

func (s *mockStorage) AppliedInstances(_ context.Context, collection, version, status string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cvKey := collection + ":" + version
	ids := s.applyByStat[cvKey][status]
	out := make([]string, len(ids))
	copy(out, ids)
	return out, nil
}

func (s *mockStorage) ResetApplyLog(_ context.Context, collection, version string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cvKey := collection + ":" + version
	delete(s.applyByStat, cvKey)
	delete(s.applyLog, cvKey)
	return nil
}

func (s *mockStorage) DeleteOldSnapshots(_ context.Context, olderThan time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	deleted := 0
	for k, snap := range s.snapshots {
		if snap.Status == storage.StatusActive {
			continue
		}
		if snap.CreatedAt.Before(olderThan) {
			delete(s.snapshots, k)
			cvKey := snap.Collection + ":" + snap.Version
			delete(s.applyByStat, cvKey)
			delete(s.applyLog, cvKey)
			deleted++
		}
	}
	return deleted, nil
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

// mockNotifier honours the notify.Channel broadcast contract, self-delivery
// included, so manager tests see a leader receive its own events.
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

	if n.closed {
		return notify.ErrClosed
	}

	// Drop instead of block when the buffer is full, as notify/postgres does —
	// a blocking send deadlocks a publish made on the draining goroutine.
	select {
	case n.subCh <- event:
	default:
	}

	return nil
}

// record appends an event without delivering it — for doubles simulating a
// publish failure that still want the attempt visible to assertions.
func (n *mockNotifier) record(event notify.Event) {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.events = append(n.events, event)
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

// TestMockNotifier_HonorsChannelContract keeps the double honest: if it stops
// broadcasting, every manager test above runs against a transport that does not exist.
func TestMockNotifier_HonorsChannelContract(t *testing.T) {
	notifytest.RunContract(t, func(t *testing.T) (notify.Channel, notify.Channel) {
		n := newMockNotifier()
		t.Cleanup(func() { n.Close() })

		return n, n
	})
}

type mockRegistry struct {
	mu               sync.Mutex
	count            int
	instances        []string // if empty, derived from count as ["self"] etc.
	deleteStaleCalls int      // counts DeleteStaleInstances invocations
	heartbeats       int      // counts Heartbeat invocations
}

func newMockRegistry() *mockRegistry {
	return &mockRegistry{count: 1}
}

func (r *mockRegistry) Register(_ context.Context, _, _ string) error { return nil }
func (r *mockRegistry) Deregister(_ context.Context, _ string) error  { return nil }

func (r *mockRegistry) Heartbeat(_ context.Context, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.heartbeats++

	return nil
}

func (r *mockRegistry) heartbeatCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.heartbeats
}

func (r *mockRegistry) AliveCount(_ context.Context, _ string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.count, nil
}

func (r *mockRegistry) DeleteStaleInstances(_ context.Context, _ time.Time) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deleteStaleCalls++
	return 0, nil
}

func (r *mockRegistry) AliveInstances(_ context.Context, _ string) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.instances) > 0 {
		out := make([]string, len(r.instances))
		copy(out, r.instances)
		return out, nil
	}

	// Fallback: synthesize ids matching count so old tests keep working.
	out := make([]string, 0, r.count)
	for i := 0; i < r.count; i++ {
		out = append(out, fmt.Sprintf("mock-instance-%d", i))
	}
	return out, nil
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

	errCh := make(chan error, 1)
	go func() {
		errCh <- mgr.Start(ctx)
	}()

	// Give it time to do initial sync.
	time.Sleep(500 * time.Millisecond)

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

	events := notif.publishedEvents()
	if len(events) == 0 {
		t.Error("expected at least one sync event published")
	} else if events[0].Action != "sync" || events[0].Collection != "articles" {
		t.Errorf("event = %+v, want sync/articles", events[0])
	}

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
	// WSClient.Subscribe needs a live WS server, so the trigger here is SyncNow, which
	// syncs unforced — hence the newer date_updated below.

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

	if articles.Count() != 1 {
		t.Fatalf("initial: Count() = %d, want 1", articles.Count())
	}

	// Simulate Directus data change.
	mu.Lock()
	useV2 = true
	mu.Unlock()

	// SyncNow hands the cycle to the run loop, so wait for the swap.
	mgr.SyncNow(ctx)

	waitFor(t, 5*time.Second, func() bool { return articles.Count() == 3 })

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
