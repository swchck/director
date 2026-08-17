package manager_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/swchck/director/config"
	"github.com/swchck/director/directus"
	"github.com/swchck/director/manager"
)

var (
	_ manager.Metrics           = (*recordingMetrics)(nil)
	_ manager.LeadershipMetrics = (*recordingMetrics)(nil)
	_ manager.SourceMetrics     = (*recordingMetrics)(nil)
	_ manager.Metrics           = (*coreOnlyMetrics)(nil)
)

// followerFailure records one FollowerFailed call with its error, so tests can
// tell reported failure kinds apart.
type followerFailure struct {
	collection string
	err        error
}

// recordingMetrics captures every Metrics call so tests can assert which
// observability hooks fired during a scenario.
type recordingMetrics struct {
	mu sync.Mutex

	syncCompleted    []string
	syncFailed       []string
	followerApplied  []string
	followerFailed   []string
	followerErrs     []followerFailure
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
	sourceRegressed  []string
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

func (r *recordingMetrics) followerAppliedCount(collection string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, c := range r.followerApplied {
		if c == collection {
			n++
		}
	}
	return n
}

func (r *recordingMetrics) FollowerFailed(c string, err error) {
	r.mu.Lock()
	r.followerFailed = append(r.followerFailed, c)
	r.followerErrs = append(r.followerErrs, followerFailure{collection: c, err: err})
	r.mu.Unlock()
}

func (r *recordingMetrics) followerFailedCount(collection string, target error) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, f := range r.followerErrs {
		if f.collection == collection && errors.Is(f.err, target) {
			n++
		}
	}
	return n
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

func (r *recordingMetrics) StagedDropped(c, reason string) {
	r.mu.Lock()
	r.stagedDropped = append(r.stagedDropped, c+":"+reason)
	r.mu.Unlock()
}

// stagedDroppedCount counts drops matching a "collection:reason" key.
func (r *recordingMetrics) stagedDroppedCount(key string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, d := range r.stagedDropped {
		if d == key {
			n++
		}
	}
	return n
}

func (r *recordingMetrics) ValidationFailed(c string) {
	r.mu.Lock()
	r.validationFailed = append(r.validationFailed, c)
	r.mu.Unlock()
}

func (r *recordingMetrics) SourceVersionRegressed(c string) {
	r.mu.Lock()
	r.sourceRegressed = append(r.sourceRegressed, c)
	r.mu.Unlock()
}

func (r *recordingMetrics) sourceRegressedCount(collection string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return countMatches(r.sourceRegressed, collection)
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

// TestMetrics_SyncCompletedFiresOnSuccess: the SyncCompleted/LeaderAcquired pair fires
// exactly once on an instance's first successful leader sync.
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

// TestMetrics_LeaderLostOnLockContention: LeaderLost fires when an instance that held
// the lock fails to reacquire it on the next poll.
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

	waitForArticleSync(t, mgr, func(c manager.ConfigStatus) bool {
		return !c.LastSyncAt.IsZero() && c.LastSyncErr == ""
	})

	if got := rec.snapshot().leaderAcquired; len(got) != 1 {
		t.Fatalf("LeaderAcquired = %v, want one entry after initial sync", got)
	}

	// Holding the lock elsewhere makes the next syncAll see ErrLockNotAcquired
	// and demote this instance.
	store.mu.Lock()
	store.lockHeld = true
	store.mu.Unlock()

	mgr.SyncNow(ctx)

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

// coreOnlyMetrics implements Metrics but deliberately not the optional
// LeadershipMetrics, mirroring an adapter written before leadership telemetry existed.
type coreOnlyMetrics struct {
	mu            sync.Mutex
	syncCompleted []string
}

func (c *coreOnlyMetrics) SyncCompleted(collection string, _ time.Duration, _ int) {
	c.mu.Lock()
	c.syncCompleted = append(c.syncCompleted, collection)
	c.mu.Unlock()
}

func (c *coreOnlyMetrics) syncCompletedCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return len(c.syncCompleted)
}

func (*coreOnlyMetrics) SyncFailed(string, error)                  {}
func (*coreOnlyMetrics) FollowerApplied(string)                    {}
func (*coreOnlyMetrics) FollowerFailed(string, error)              {}
func (*coreOnlyMetrics) CacheHit(string)                           {}
func (*coreOnlyMetrics) CacheMiss(string)                          {}
func (*coreOnlyMetrics) StorageLoaded(string)                      {}
func (*coreOnlyMetrics) WSEventReceived(string)                    {}
func (*coreOnlyMetrics) PreparePhaseStarted(string, string)        {}
func (*coreOnlyMetrics) PreparePhaseSucceeded(string, string)      {}
func (*coreOnlyMetrics) PreparePhaseFailed(string, string, string) {}
func (*coreOnlyMetrics) FollowerPrepared(string)                   {}
func (*coreOnlyMetrics) FollowerPrepareFailed(string, error)       {}
func (*coreOnlyMetrics) StagedDropped(string, string)              {}
func (*coreOnlyMetrics) ValidationFailed(string)                   {}

// TestMetrics_LeadershipOptionalForImplementors drives an acquire/lose cycle against a
// Metrics without LeadershipMetrics: no panic, required telemetry still arrives.
func TestMetrics_LeadershipOptionalForImplementors(t *testing.T) {
	rec := &coreOnlyMetrics{}
	if _, ok := manager.Metrics(rec).(manager.LeadershipMetrics); ok {
		t.Fatal("coreOnlyMetrics implements LeadershipMetrics; the fallback path is no longer covered")
	}

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

	// Becoming leader would call LeaderAcquired on an implementor that has it.
	waitForArticleSync(t, mgr, func(c manager.ConfigStatus) bool {
		return !c.LastSyncAt.IsZero() && c.LastSyncErr == ""
	})

	// Losing the lock would call LeaderLost on an implementor that has it.
	store.mu.Lock()
	store.lockHeld = true
	store.mu.Unlock()

	mgr.SyncNow(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && mgr.Status().IsLeader {
		time.Sleep(20 * time.Millisecond)
	}

	if mgr.Status().IsLeader {
		t.Error("instance still reports leadership after the lock was taken")
	}

	cancel()
	if err := <-errCh; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Start: %v", err)
	}

	if rec.syncCompletedCount() == 0 {
		t.Error("SyncCompleted not fired; required telemetry lost")
	}
}

// TestNopMetrics_ImplementsOptionalInterfaces pins that the shipped default
// stays a complete implementation as optional interfaces are added.
func TestNopMetrics_ImplementsOptionalInterfaces(t *testing.T) {
	if _, ok := manager.NopMetrics().(manager.LeadershipMetrics); !ok {
		t.Error("NopMetrics() does not implement LeadershipMetrics")
	}
	if _, ok := manager.NopMetrics().(manager.SourceMetrics); !ok {
		t.Error("NopMetrics() does not implement SourceMetrics")
	}
}
