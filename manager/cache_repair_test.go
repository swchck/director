package manager_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/swchck/director/cache"
	"github.com/swchck/director/config"
	"github.com/swchck/director/manager"
	"github.com/swchck/director/storage"
)

// trackingCache is a Cache that tracks Set call count and can fail Get.
type trackingCache struct {
	mu       sync.Mutex
	entries  map[string]*cache.Entry
	setCalls int32
	getErr   error // if non-nil, Get returns this error instead of looking up
}

func newTrackingCache() *trackingCache {
	return &trackingCache{entries: make(map[string]*cache.Entry)}
}

func (c *trackingCache) Get(_ context.Context, collection string) (*cache.Entry, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.getErr != nil {
		return nil, c.getErr
	}

	e, ok := c.entries[collection]
	if !ok {
		return nil, cache.ErrCacheMiss
	}
	return e, nil
}

func (c *trackingCache) Set(_ context.Context, entry cache.Entry) error {
	atomic.AddInt32(&c.setCalls, 1)
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[entry.Collection] = &entry
	return nil
}

func (c *trackingCache) Delete(_ context.Context, _ string) error { return nil }
func (c *trackingCache) Close() error                             { return nil }

func (c *trackingCache) setCount() int32 { return atomic.LoadInt32(&c.setCalls) }
func (c *trackingCache) getEntry(collection string) *cache.Entry {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.entries[collection]
}

// seedActiveSnapshot writes an active snapshot to mock storage.
func seedActiveSnapshot(t *testing.T, store *mockStorage, collection, version string, items []twoPCArticle) {
	t.Helper()
	payload, err := json.Marshal(items)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	ctx := context.Background()
	if err := store.SaveSnapshot(ctx, collection, version, payload); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	if err := store.ActivateSnapshot(ctx, collection, version); err != nil {
		t.Fatalf("ActivateSnapshot: %v", err)
	}
}

// TestCacheRepair_LeaderSyncWarmsColdCache: storage has active v1, cache is
// empty, source reports v1 (no version change). The leader's version-skip
// branch must repair the cache.
func TestCacheRepair_LeaderSyncWarmsColdCache(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	versionStr := config.NewVersion(now).String()

	store := newMockStorage()
	notif := newMockNotifier()
	reg := newMockRegistry()

	seedActiveSnapshot(t, store, "articles", versionStr, []twoPCArticle{{ID: 1, Name: "Alpha"}})

	tc := newTrackingCache()

	src := &twoPCSource{
		items:        []twoPCArticle{{ID: 1, Name: "Alpha"}},
		lastModified: now,
	}

	articles := config.NewCollection[twoPCArticle]("articles")

	mgr := manager.New(store, notif, reg, manager.Options{
		PollInterval:             time.Hour,
		WaitConfirmationsTimeout: time.Second,
		ServiceName:              "test-svc",
	},
		manager.WithCache(tc, cache.ReadWriteThrough),
	)
	manager.RegisterCollectionSource(mgr, articles, src)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- mgr.Start(ctx) }()

	waitFor(t, 3*time.Second, func() bool {
		return tc.getEntry("articles") != nil
	})

	entry := tc.getEntry("articles")
	if entry == nil {
		t.Fatal("cache was not warmed after version-skip sync")
	}
	if entry.Version != versionStr {
		t.Errorf("cache version = %q, want %q", entry.Version, versionStr)
	}

	cancel()
	<-errCh
}

// TestCacheRepair_NoOpWhenCacheHot: cache and storage both have v1, version
// matches source → repair must NOT call Set.
func TestCacheRepair_NoOpWhenCacheHot(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	versionStr := config.NewVersion(now).String()

	store := newMockStorage()
	notif := newMockNotifier()
	reg := newMockRegistry()

	seedActiveSnapshot(t, store, "articles", versionStr, []twoPCArticle{{ID: 1, Name: "Alpha"}})

	payload, _ := json.Marshal([]twoPCArticle{{ID: 1, Name: "Alpha"}})
	tc := newTrackingCache()
	tc.entries["articles"] = &cache.Entry{
		Collection: "articles",
		Version:    versionStr,
		Content:    payload,
	}

	src := &twoPCSource{
		items:        []twoPCArticle{{ID: 1, Name: "Alpha"}},
		lastModified: now,
	}

	articles := config.NewCollection[twoPCArticle]("articles")

	mgr := manager.New(store, notif, reg, manager.Options{
		PollInterval:             time.Hour,
		WaitConfirmationsTimeout: time.Second,
		ServiceName:              "test-svc",
	},
		manager.WithCache(tc, cache.ReadWriteThrough),
	)
	manager.RegisterCollectionSource(mgr, articles, src)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- mgr.Start(ctx) }()

	// Allow initial syncAll to finish.
	waitFor(t, 3*time.Second, func() bool {
		return articles.Count() == 1
	})
	// Small grace so any spurious Set could land.
	time.Sleep(100 * time.Millisecond)

	if got := tc.setCount(); got != 0 {
		t.Errorf("cache.Set call count = %d, want 0 (cache was hot)", got)
	}

	cancel()
	<-errCh
}

// TestCacheRepair_NoOpWhenWritesDisabled: ReadThrough strategy never writes
// to cache, so repair must be a no-op even with a cold cache.
func TestCacheRepair_NoOpWhenWritesDisabled(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	versionStr := config.NewVersion(now).String()

	store := newMockStorage()
	notif := newMockNotifier()
	reg := newMockRegistry()

	seedActiveSnapshot(t, store, "articles", versionStr, []twoPCArticle{{ID: 1, Name: "Alpha"}})

	tc := newTrackingCache()

	src := &twoPCSource{
		items:        []twoPCArticle{{ID: 1, Name: "Alpha"}},
		lastModified: now,
	}

	articles := config.NewCollection[twoPCArticle]("articles")

	mgr := manager.New(store, notif, reg, manager.Options{
		PollInterval:             time.Hour,
		WaitConfirmationsTimeout: time.Second,
		ServiceName:              "test-svc",
	},
		manager.WithCache(tc, cache.ReadThrough), // read only — no writes
	)
	manager.RegisterCollectionSource(mgr, articles, src)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- mgr.Start(ctx) }()

	waitFor(t, 3*time.Second, func() bool {
		return articles.Count() == 1
	})
	time.Sleep(100 * time.Millisecond)

	if got := tc.setCount(); got != 0 {
		t.Errorf("cache.Set called %d times under ReadThrough; want 0", got)
	}
	if tc.getEntry("articles") != nil {
		t.Error("cache populated under ReadThrough strategy")
	}

	cancel()
	<-errCh
}

// TestCacheRepair_ManualSyncOnly_WarmsOnStartup: with ManualSyncOnly=true,
// storage has data, cache is cold — syncAll is skipped, but the explicit
// warmCacheIfMissing step in Start() must populate the cache. The source
// must NOT be called (no version fetch, no list).
func TestCacheRepair_ManualSyncOnly_WarmsOnStartup(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	versionStr := config.NewVersion(now).String()

	store := newMockStorage()
	notif := newMockNotifier()
	reg := newMockRegistry()

	seedActiveSnapshot(t, store, "articles", versionStr, []twoPCArticle{{ID: 1, Name: "Alpha"}})

	tc := newTrackingCache()

	src := &countingSource{}
	articles := config.NewCollection[twoPCArticle]("articles")

	mgr := manager.New(store, notif, reg, manager.Options{
		PollInterval:             time.Hour,
		WaitConfirmationsTimeout: time.Second,
		ServiceName:              "test-svc",
		ManualSyncOnly:           true,
	},
		manager.WithCache(tc, cache.ReadWriteThrough),
	)
	manager.RegisterCollectionSource(mgr, articles, src)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- mgr.Start(ctx) }()

	waitFor(t, 3*time.Second, func() bool {
		return tc.getEntry("articles") != nil
	})

	entry := tc.getEntry("articles")
	if entry == nil {
		t.Fatal("cache not warmed in ManualSyncOnly startup")
	}
	if entry.Version != versionStr {
		t.Errorf("cache version = %q, want %q", entry.Version, versionStr)
	}

	if src.lastModifiedCalls.Load() != 0 {
		t.Errorf("source LastModified called %d times in ManualSyncOnly bootstrap; want 0",
			src.lastModifiedCalls.Load())
	}
	if src.listCalls.Load() != 0 {
		t.Errorf("source List called %d times in ManualSyncOnly bootstrap; want 0",
			src.listCalls.Load())
	}

	cancel()
	<-errCh
}

// TestCacheRepair_SwallowsCacheReadError: a transient error from cache.Get
// during repair must not break the sync cycle and must not write to cache.
func TestCacheRepair_SwallowsCacheReadError(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	versionStr := config.NewVersion(now).String()

	store := newMockStorage()
	notif := newMockNotifier()
	reg := newMockRegistry()

	seedActiveSnapshot(t, store, "articles", versionStr, []twoPCArticle{{ID: 1, Name: "Alpha"}})

	tc := newTrackingCache()
	tc.getErr = errors.New("simulated redis network blip")

	src := &twoPCSource{
		items:        []twoPCArticle{{ID: 1, Name: "Alpha"}},
		lastModified: now,
	}

	articles := config.NewCollection[twoPCArticle]("articles")

	mgr := manager.New(store, notif, reg, manager.Options{
		PollInterval:             time.Hour,
		WaitConfirmationsTimeout: time.Second,
		ServiceName:              "test-svc",
	},
		manager.WithCache(tc, cache.ReadWriteThrough),
	)
	manager.RegisterCollectionSource(mgr, articles, src)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- mgr.Start(ctx) }()

	waitFor(t, 3*time.Second, func() bool {
		return articles.Count() == 1
	})
	time.Sleep(100 * time.Millisecond)

	if got := tc.setCount(); got != 0 {
		t.Errorf("cache.Set called %d times after Get error; want 0", got)
	}
	// Sync still completed: storage has the active snapshot.
	if _, err := store.GetActiveSnapshot(ctx, "articles"); err != nil {
		t.Errorf("active snapshot unexpectedly missing: %v", err)
	}

	cancel()
	<-errCh
}

// TestCacheRepair_2PCWarmsColdCache: same cold-cache scenario but with
// RequireUnanimousApply=true. The 2PC version-skip branch must also repair.
func TestCacheRepair_2PCWarmsColdCache(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	versionStr := config.NewVersion(now).String()

	store := newMockStorage()
	reg := newTwoPCRegistry("leader") // single instance, no followers
	notif := newTwoPCNotifier()

	seedActiveSnapshot(t, store, "articles", versionStr, []twoPCArticle{{ID: 1, Name: "Alpha"}})

	tc := newTrackingCache()

	src := &twoPCSource{
		items:        []twoPCArticle{{ID: 1, Name: "Alpha"}},
		lastModified: now,
	}

	articles := config.NewCollection[twoPCArticle]("articles")

	mgr := manager.New(store, notif, reg, manager.Options{
		PollInterval:             time.Hour,
		WaitConfirmationsTimeout: time.Second,
		PrepareTTL:               2 * time.Second,
		ServiceName:              "test-svc",
		RequireUnanimousApply:    true,
	},
		manager.WithInstanceID("leader"),
		manager.WithCache(tc, cache.ReadWriteThrough),
	)
	manager.RegisterCollectionSource(mgr, articles, src)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- mgr.Start(ctx) }()

	waitFor(t, 3*time.Second, func() bool {
		return tc.getEntry("articles") != nil
	})

	entry := tc.getEntry("articles")
	if entry == nil {
		t.Fatal("2PC version-skip did not warm cache")
	}
	if entry.Version != versionStr {
		t.Errorf("cache version = %q, want %q", entry.Version, versionStr)
	}

	cancel()
	<-errCh
}

// countingSource counts source calls; used to verify that ManualSyncOnly
// bootstrap does not contact the source when storage already has data.
type countingSource struct {
	lastModifiedCalls atomic.Int32
	listCalls         atomic.Int32
}

func (s *countingSource) List(_ context.Context) ([]twoPCArticle, error) {
	s.listCalls.Add(1)
	return nil, nil
}

func (s *countingSource) LastModified(_ context.Context) (time.Time, error) {
	s.lastModifiedCalls.Add(1)
	return time.Time{}, nil
}

// Prevent unused-import warnings on rare combinations.
var _ = storage.ErrSnapshotNotFound
