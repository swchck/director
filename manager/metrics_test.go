package manager_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/swchck/director/config"
	"github.com/swchck/director/directus"
	"github.com/swchck/director/manager"
)

// recordingMetrics captures every Metrics call so tests can assert which
// observability hooks fired during a scenario.
type recordingMetrics struct {
	mu sync.Mutex

	syncCompleted    []string
	syncFailed       []string
	followerApplied  []string
	followerFailed   []string
	cacheHit         []string
	cacheMiss        []string
	storageLoaded    []string
	wsEventReceived  []string
	prepareStarted   []string
	prepareSucceeded []string
	prepareFailed    []string
	followerPrepared []string
	followerPrepFail []string
	stagedDropped    []string
	validationFailed []string
	leaderAcquired   []string
	leaderLost       []string
}

func (r *recordingMetrics) SyncCompleted(c string, _ time.Duration, _ int) {
	r.mu.Lock()
	r.syncCompleted = append(r.syncCompleted, c)
	r.mu.Unlock()
}

func (r *recordingMetrics) SyncFailed(c string, _ error) {
	r.mu.Lock()
	r.syncFailed = append(r.syncFailed, c)
	r.mu.Unlock()
}

func (r *recordingMetrics) FollowerApplied(c string) {
	r.mu.Lock()
	r.followerApplied = append(r.followerApplied, c)
	r.mu.Unlock()
}

func (r *recordingMetrics) FollowerFailed(c string, _ error) {
	r.mu.Lock()
	r.followerFailed = append(r.followerFailed, c)
	r.mu.Unlock()
}

func (r *recordingMetrics) CacheHit(c string) {
	r.mu.Lock()
	r.cacheHit = append(r.cacheHit, c)
	r.mu.Unlock()
}

func (r *recordingMetrics) CacheMiss(c string) {
	r.mu.Lock()
	r.cacheMiss = append(r.cacheMiss, c)
	r.mu.Unlock()
}

func (r *recordingMetrics) StorageLoaded(c string) {
	r.mu.Lock()
	r.storageLoaded = append(r.storageLoaded, c)
	r.mu.Unlock()
}

func (r *recordingMetrics) WSEventReceived(c string) {
	r.mu.Lock()
	r.wsEventReceived = append(r.wsEventReceived, c)
	r.mu.Unlock()
}

func (r *recordingMetrics) PreparePhaseStarted(c, _ string) {
	r.mu.Lock()
	r.prepareStarted = append(r.prepareStarted, c)
	r.mu.Unlock()
}

func (r *recordingMetrics) PreparePhaseSucceeded(c, _ string) {
	r.mu.Lock()
	r.prepareSucceeded = append(r.prepareSucceeded, c)
	r.mu.Unlock()
}

func (r *recordingMetrics) PreparePhaseFailed(c, _, _ string) {
	r.mu.Lock()
	r.prepareFailed = append(r.prepareFailed, c)
	r.mu.Unlock()
}

func (r *recordingMetrics) FollowerPrepared(c string) {
	r.mu.Lock()
	r.followerPrepared = append(r.followerPrepared, c)
	r.mu.Unlock()
}

func (r *recordingMetrics) FollowerPrepareFailed(c string, _ error) {
	r.mu.Lock()
	r.followerPrepFail = append(r.followerPrepFail, c)
	r.mu.Unlock()
}

func (r *recordingMetrics) StagedDropped(c, _ string) {
	r.mu.Lock()
	r.stagedDropped = append(r.stagedDropped, c)
	r.mu.Unlock()
}

func (r *recordingMetrics) ValidationFailed(c string) {
	r.mu.Lock()
	r.validationFailed = append(r.validationFailed, c)
	r.mu.Unlock()
}

func (r *recordingMetrics) LeaderAcquired(s string) {
	r.mu.Lock()
	r.leaderAcquired = append(r.leaderAcquired, s)
	r.mu.Unlock()
}

func (r *recordingMetrics) LeaderLost(s string) {
	r.mu.Lock()
	r.leaderLost = append(r.leaderLost, s)
	r.mu.Unlock()
}

func (r *recordingMetrics) snapshot() recordingMetricsSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return recordingMetricsSnapshot{
		syncCompleted:    append([]string(nil), r.syncCompleted...),
		syncFailed:       append([]string(nil), r.syncFailed...),
		leaderAcquired:   append([]string(nil), r.leaderAcquired...),
		leaderLost:       append([]string(nil), r.leaderLost...),
		validationFailed: append([]string(nil), r.validationFailed...),
	}
}

type recordingMetricsSnapshot struct {
	syncCompleted    []string
	syncFailed       []string
	leaderAcquired   []string
	leaderLost       []string
	validationFailed []string
}

// TestMetrics_SyncCompletedFiresOnSuccess verifies the basic
// SyncCompleted/LeaderAcquired pair fires exactly once when an instance
// successfully syncs as leader for the first time.
func TestMetrics_SyncCompletedFiresOnSuccess(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("limit") == "1" && r.URL.Query().Get("sort") == "-date_updated" {
			json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"date_updated": now.Format(time.RFC3339)}},
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"data": []testArticle{{ID: 1, Name: "A"}},
		})
	}))
	defer srv.Close()

	dc := directus.NewClient(srv.URL, "tok")
	store := newMockStorage()
	notif := newMockNotifier()
	reg := newMockRegistry()
	rec := &recordingMetrics{}

	articles := config.NewCollection[testArticle]("articles")
	mgr := manager.New(store, notif, reg, manager.Options{
		PollInterval:             time.Hour,
		WaitConfirmationsTimeout: time.Second,
		ServiceName:              "metrics-svc",
	}, manager.WithMetrics(rec))

	manager.RegisterCollection(mgr, articles, directus.NewItems[testArticle](dc, "articles"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- mgr.Start(ctx) }()

	waitForArticleSync(t, mgr, func(c manager.ConfigStatus) bool {
		return !c.LastSyncAt.IsZero() && c.LastSyncErr == ""
	})

	cancel()
	<-errCh

	snap := rec.snapshot()
	if len(snap.syncCompleted) == 0 {
		t.Error("SyncCompleted not fired after successful sync")
	}
	if len(snap.syncFailed) != 0 {
		t.Errorf("SyncFailed fired unexpectedly: %v", snap.syncFailed)
	}
	if len(snap.leaderAcquired) == 0 || snap.leaderAcquired[0] != "metrics-svc" {
		t.Errorf("LeaderAcquired = %v, want [metrics-svc]", snap.leaderAcquired)
	}
	if len(snap.leaderLost) != 0 {
		t.Errorf("LeaderLost fired unexpectedly: %v", snap.leaderLost)
	}
}

// TestMetrics_LeaderLostOnLockContention verifies LeaderLost fires when an
// instance that previously held the lock fails to reacquire it on the next
// poll.
func TestMetrics_LeaderLostOnLockContention(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("limit") == "1" && r.URL.Query().Get("sort") == "-date_updated" {
			json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"date_updated": now.Format(time.RFC3339)}},
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"data": []testArticle{{ID: 1, Name: "A"}},
		})
	}))
	defer srv.Close()

	dc := directus.NewClient(srv.URL, "tok")
	store := newMockStorage()
	notif := newMockNotifier()
	reg := newMockRegistry()
	rec := &recordingMetrics{}

	articles := config.NewCollection[testArticle]("articles")
	mgr := manager.New(store, notif, reg, manager.Options{
		PollInterval:             time.Hour,
		WaitConfirmationsTimeout: time.Second,
		ServiceName:              "metrics-svc",
	}, manager.WithMetrics(rec))

	manager.RegisterCollection(mgr, articles, directus.NewItems[testArticle](dc, "articles"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- mgr.Start(ctx) }()

	// Wait for initial leader sync.
	waitForArticleSync(t, mgr, func(c manager.ConfigStatus) bool {
		return !c.LastSyncAt.IsZero() && c.LastSyncErr == ""
	})

	if got := rec.snapshot().leaderAcquired; len(got) != 1 {
		t.Fatalf("LeaderAcquired = %v, want one entry after initial sync", got)
	}

	// Simulate another instance holding the lock by manually marking it held
	// — next syncAll will see ErrLockNotAcquired and demote this instance.
	store.mu.Lock()
	store.lockHeld = true
	store.mu.Unlock()

	// Trigger another sync cycle externally.
	mgr.SyncNow(ctx)

	// Poll for LeaderLost to be recorded.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(rec.snapshot().leaderLost) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	cancel()
	<-errCh

	snap := rec.snapshot()
	if len(snap.leaderLost) == 0 {
		t.Errorf("LeaderLost not fired after losing the lock; acquired=%v", snap.leaderAcquired)
	} else if snap.leaderLost[0] != "metrics-svc" {
		t.Errorf("LeaderLost = %v, want [metrics-svc]", snap.leaderLost)
	}
}
