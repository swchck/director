package manager_test

import (
	"testing"
	"time"

	"github.com/swchck/director/cache"
	"github.com/swchck/director/config"
	"github.com/swchck/director/manager"
)

// -- Warm start: a replica loads cache first, then the active snapshot. These tests
// pin which wins, because a cache can name a version the cluster rolled back from.

// warmStartFixture is a replica that never becomes leader (the lock is held
// elsewhere), so nothing but the startup sequence decides what it serves.
type warmStartFixture struct {
	store    *mockStorage
	cache    *trackingCache
	logger   *captureLogger
	metrics  *recordingMetrics
	mgr      *manager.Manager
	articles *config.Collection[twoPCArticle]
}

func newWarmStartFixture(t *testing.T) *warmStartFixture {
	t.Helper()

	store := newMockStorage()
	store.lockHeld = true // another replica is leader

	tc := newTrackingCache()
	logger := &captureLogger{}
	metrics := &recordingMetrics{}
	articles := config.NewCollection[twoPCArticle]("articles")

	mgr := manager.New(store, newMockNotifier(), newTwoPCRegistry("inst-1"), manager.Options{
		PollInterval:             time.Hour,
		HeartbeatInterval:        50 * time.Millisecond,
		WaitConfirmationsTimeout: time.Second,
		ServiceName:              "test-svc",
	},
		manager.WithInstanceID("inst-1"),
		manager.WithLogger(logger),
		manager.WithMetrics(metrics),
		manager.WithCache(tc, cache.ReadWriteThrough),
	)

	// The source is never reached: this replica cannot win the lock.
	manager.RegisterCollectionSource(mgr, articles, &twoPCSource{})

	return &warmStartFixture{
		store:    store,
		cache:    tc,
		logger:   logger,
		metrics:  metrics,
		mgr:      mgr,
		articles: articles,
	}
}

// TestWarmStart_ActiveSnapshotWinsOverCache: without this, a pod starting after a
// rollback warm-loads the rolled-back-from version and no later path corrects it.
func TestWarmStart_ActiveSnapshotWinsOverCache(t *testing.T) {
	older := config.NewVersion(time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)).String()
	newer := config.NewVersion(time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)).String()

	tests := []struct {
		name        string
		cacheVer    string
		activeVer   string
		wantVersion string
		wantCount   int
	}{
		{
			name:        "cache ahead of the active snapshot",
			cacheVer:    newer,
			activeVer:   older,
			wantVersion: older,
			wantCount:   1,
		},
		{
			name:        "cache behind the active snapshot",
			cacheVer:    older,
			activeVer:   newer,
			wantVersion: newer,
			wantCount:   2,
		},
		{
			name:        "cache agrees with the active snapshot",
			cacheVer:    newer,
			activeVer:   newer,
			wantVersion: newer,
			wantCount:   2,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newWarmStartFixture(t)

			// One item at the older version, two at the newer, so the item count
			// alone says which payload the replica is serving.
			seedSnapshot(t, f.store, older, []twoPCArticle{{ID: 1, Name: "one"}})
			seedSnapshot(t, f.store, newer, []twoPCArticle{{ID: 1, Name: "one"}, {ID: 2, Name: "two"}})
			f.store.forceActive("articles", tc.activeVer)

			cachedItems := []twoPCArticle{{ID: 1, Name: "one"}}
			if tc.cacheVer == newer {
				cachedItems = []twoPCArticle{{ID: 1, Name: "one"}, {ID: 2, Name: "two"}}
			}
			seedCacheVersion(t, f.cache, tc.cacheVer, cachedItems)

			startManager(t, f.mgr, f.logger)

			if got := f.articles.Version().String(); got != tc.wantVersion {
				t.Errorf("in-memory version = %q, want %q — the active snapshot is canonical at startup", got, tc.wantVersion)
			}
			if got := f.articles.Count(); got != tc.wantCount {
				t.Errorf("articles.Count() = %d, want %d", got, tc.wantCount)
			}
			if !f.mgr.Ready() {
				t.Error("manager is not Ready after warm start")
			}

			// Several reconcile ticks: local and active agree, so nothing may be
			// refused and nothing may move.
			time.Sleep(200 * time.Millisecond)

			if got := f.metrics.followerFailedCount("articles", manager.ErrActiveSnapshotBehind); got != 0 {
				t.Errorf("catch-up refusals = %d, want 0 — the replica is stranded on the cached version", got)
			}
			if got := f.articles.Version().String(); got != tc.wantVersion {
				t.Errorf("in-memory version = %q after reconciling, want %q", got, tc.wantVersion)
			}
		})
	}
}

// TestWarmStart_CacheKeptWhenStorageHasNoActiveSnapshot: making storage canonical must
// not throw away the reason the cache is read first.
func TestWarmStart_CacheKeptWhenStorageHasNoActiveSnapshot(t *testing.T) {
	version := config.NewVersion(time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)).String()

	f := newWarmStartFixture(t)
	seedCacheVersion(t, f.cache, version, []twoPCArticle{{ID: 1, Name: "one"}, {ID: 2, Name: "two"}})

	startManager(t, f.mgr, f.logger)

	if got := f.articles.Version().String(); got != version {
		t.Errorf("in-memory version = %q, want %q — the cached value was discarded", got, version)
	}
	if got := f.articles.Count(); got != 2 {
		t.Errorf("articles.Count() = %d, want 2", got)
	}
	if !f.mgr.Ready() {
		t.Error("manager is not Ready after loading from cache alone")
	}
}
