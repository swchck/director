//go:build e2e

package e2e_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	dcfg "github.com/swchck/director/config"
	"github.com/swchck/director/manager"
	"github.com/swchck/director/notify"
	pgnotify "github.com/swchck/director/notify/postgres"
	pgregistry "github.com/swchck/director/registry/postgres"
	"github.com/swchck/director/storage"
	pgstorage "github.com/swchck/director/storage/postgres"
)

// flakyStorage wraps a real storage and lets a test fail GetSnapshot for one
// specific instance (simulating a follower that can't load the snapshot
// during 2PC prepare — a realistic prepare-time failure mode).
//
// neverLeader, when true, makes AcquireLock always return ErrLockNotAcquired
// so this instance is pinned as a follower. Without this, after the leader's
// round aborts the lock is released and the "broken" replica can take
// leadership on the next poll cycle — where a leader uses fetchAndStage
// (source path) and bypasses the flaky GetSnapshot entirely, masking the bug.
type flakyStorage struct {
	storage.Storage
	failGetSnapshot atomic.Bool
	neverLeader     atomic.Bool
}

func (s *flakyStorage) GetSnapshot(ctx context.Context, collection, version string) (*storage.Snapshot, error) {
	if s.failGetSnapshot.Load() {
		return nil, errors.New("flakyStorage: simulated GetSnapshot failure")
	}
	return s.Storage.GetSnapshot(ctx, collection, version)
}

func (s *flakyStorage) AcquireLock(ctx context.Context, key int64) (func(), error) {
	if s.neverLeader.Load() {
		return nil, storage.ErrLockNotAcquired
	}
	return s.Storage.AcquireLock(ctx, key)
}

// e2eTPCArticle is the payload for 2PC e2e tests.
type e2eTPCArticle struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Score int    `json:"score"`
}

// controllableSource lets a test produce deterministic version changes,
// bypassing the Directus 11 date_updated quirk. The underlying real
// Postgres storage / notify / registry are still exercised end-to-end.
type controllableSource struct {
	mu           sync.Mutex
	items        []e2eTPCArticle
	lastModified time.Time
	failList     atomic.Bool
}

func (s *controllableSource) snapshot() ([]e2eTPCArticle, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]e2eTPCArticle, len(s.items))
	copy(out, s.items)
	return out, s.lastModified
}

func (s *controllableSource) update(items []e2eTPCArticle, ts time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = items
	s.lastModified = ts
}

func (s *controllableSource) List(_ context.Context) ([]e2eTPCArticle, error) {
	if s.failList.Load() {
		return nil, errors.New("simulated replica failure")
	}
	items, _ := s.snapshot()
	return items, nil
}

func (s *controllableSource) LastModified(_ context.Context) (time.Time, error) {
	_, ts := s.snapshot()
	return ts, nil
}

// resetTPCState wipes 2PC-related Postgres rows so every test starts clean.
// Tables: director.config_snapshots, director.config_apply_log,
// director.config_instances. Idempotent — errors on missing tables ignored.
func resetTPCState(t *testing.T, pool *pgxpool.Pool, collection, serviceName string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, q := range []struct{ sql, args string }{
		{"DELETE FROM director.config_snapshots WHERE collection_name = $1", collection},
		{"DELETE FROM director.config_apply_log WHERE collection_name = $1", collection},
		{"DELETE FROM director.config_instances WHERE service_name = $1", serviceName},
	} {
		if _, err := pool.Exec(ctx, q.sql, q.args); err != nil {
			t.Logf("cleanup %q: %v", q.sql, err)
		}
	}
}

// TestE2E_TwoPhaseCommit_HappyPath: two managers with RequireUnanimousApply
// converge on the same version and the leader publishes a commit.
func TestE2E_TwoPhaseCommit_HappyPath(t *testing.T) {
	const (
		collection  = "tpc_e2e_happy"
		serviceName = "tpc-e2e-happy"
	)

	pgPool := testPgPool(t)
	store := pgstorage.NewStorage(pgPool)
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	resetTPCState(t, pgPool, collection, serviceName)

	now := time.Now().UTC().Truncate(time.Second)
	src := &controllableSource{
		items: []e2eTPCArticle{
			{ID: 1, Name: "Alpha", Score: 10},
			{ID: 2, Name: "Beta", Score: 20},
			{ID: 3, Name: "Gamma", Score: 30},
		},
		lastModified: now,
	}

	cfgA := dcfg.NewCollection[e2eTPCArticle](collection)
	cfgB := dcfg.NewCollection[e2eTPCArticle](collection)

	notifA := pgnotify.NewChannel(pgPool)
	defer notifA.Close()
	notifB := pgnotify.NewChannel(pgPool)
	defer notifB.Close()

	opts := manager.Options{
		PollInterval:             2 * time.Second,
		HeartbeatInterval:        500 * time.Millisecond,
		WaitConfirmationsTimeout: 10 * time.Second,
		PrepareTTL:               20 * time.Second,
		ServiceName:              serviceName,
		RequireUnanimousApply:    true,
	}

	mgrA := manager.New(store, notifA, pgregistry.NewRegistry(pgPool), opts, manager.WithLogger(testLogger(t)))
	manager.RegisterCollectionSource(mgrA, cfgA, src)

	mgrB := manager.New(store, notifB, pgregistry.NewRegistry(pgPool), opts, manager.WithLogger(testLogger(t)))
	manager.RegisterCollectionSource(mgrB, cfgB, src)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	ctxA, cancelA := context.WithCancel(ctx)
	defer cancelA()
	errA := make(chan error, 1)
	go func() { errA <- mgrA.Start(ctxA) }()

	// Give A a head start so registry has 1 instance, then start B.
	time.Sleep(300 * time.Millisecond)

	ctxB, cancelB := context.WithCancel(ctx)
	defer cancelB()
	errB := make(chan error, 1)
	go func() { errB <- mgrB.Start(ctxB) }()

	// Wait for both to converge.
	if !waitUntil(30*time.Second, func() bool {
		return cfgA.Count() == 3 && cfgB.Count() == 3 &&
			cfgA.Version().Equal(cfgB.Version())
	}) {
		t.Fatalf("did not converge: A=%d/%q B=%d/%q",
			cfgA.Count(), cfgA.Version(), cfgB.Count(), cfgB.Version())
	}

	// Publish a new version and confirm both replicas advance together.
	now2 := now.Add(time.Hour)
	src.update([]e2eTPCArticle{
		{ID: 1, Name: "Alpha", Score: 11},
		{ID: 2, Name: "Beta", Score: 22},
	}, now2)

	mgrA.SyncNow(ctx)

	if !waitUntil(30*time.Second, func() bool {
		return cfgA.Count() == 2 && cfgB.Count() == 2 &&
			cfgA.Version().Equal(cfgB.Version())
	}) {
		t.Fatalf("after update did not converge: A=%d/%q B=%d/%q",
			cfgA.Count(), cfgA.Version(), cfgB.Count(), cfgB.Version())
	}

	cancelA()
	cancelB()
	<-errA
	<-errB
}

// TestE2E_TwoPhaseCommit_AbortOnBrokenReplica: replica B uses a flakyStorage
// that fails GetSnapshot during the broken window — simulating a follower
// whose snapshot read fails during 2PC prepare. The leader must abort and
// neither replica may advance. After flaky behavior is disabled, both
// replicas converge on the new version.
//
// In 2PC, followers don't call their source.List() during prepare — they
// load the snapshot from storage. So a realistic "broken follower" is one
// whose storage read fails (corrupt snapshot, transient pg error, etc.).
func TestE2E_TwoPhaseCommit_AbortOnBrokenReplica(t *testing.T) {
	const (
		collection  = "tpc_e2e_abort"
		serviceName = "tpc-e2e-abort"
	)

	pgPool := testPgPool(t)
	storeReal := pgstorage.NewStorage(pgPool)
	if err := storeReal.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	resetTPCState(t, pgPool, collection, serviceName)

	// B reads through a flaky wrapper; both write to the same underlying pg.
	flakyB := &flakyStorage{Storage: storeReal}

	now := time.Now().UTC().Truncate(time.Second)
	src := &controllableSource{
		items:        []e2eTPCArticle{{ID: 1, Name: "v1", Score: 1}},
		lastModified: now,
	}

	cfgA := dcfg.NewCollection[e2eTPCArticle](collection)
	cfgB := dcfg.NewCollection[e2eTPCArticle](collection)

	notifA := pgnotify.NewChannel(pgPool)
	defer notifA.Close()
	notifB := pgnotify.NewChannel(pgPool)
	defer notifB.Close()

	opts := manager.Options{
		PollInterval:             2 * time.Second,
		HeartbeatInterval:        500 * time.Millisecond,
		WaitConfirmationsTimeout: 4 * time.Second,
		PrepareTTL:               10 * time.Second,
		ServiceName:              serviceName,
		RequireUnanimousApply:    true,
	}

	mgrA := manager.New(storeReal, notifA, pgregistry.NewRegistry(pgPool), opts, manager.WithLogger(testLogger(t)))
	manager.RegisterCollectionSource(mgrA, cfgA, src)

	mgrB := manager.New(flakyB, notifB, pgregistry.NewRegistry(pgPool), opts, manager.WithLogger(testLogger(t)))
	manager.RegisterCollectionSource(mgrB, cfgB, src)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	ctxA, cancelA := context.WithCancel(ctx)
	defer cancelA()
	errA := make(chan error, 1)
	go func() { errA <- mgrA.Start(ctxA) }()

	time.Sleep(300 * time.Millisecond)

	ctxB, cancelB := context.WithCancel(ctx)
	defer cancelB()
	errB := make(chan error, 1)
	go func() { errB <- mgrB.Start(ctxB) }()

	// Wait for v1 to converge.
	if !waitUntil(20*time.Second, func() bool {
		return cfgA.Count() == 1 && cfgB.Count() == 1 &&
			cfgA.Version().Equal(cfgB.Version())
	}) {
		t.Fatalf("v1 did not converge: A=%d/%q B=%d/%q",
			cfgA.Count(), cfgA.Version(), cfgB.Count(), cfgB.Version())
	}
	v1 := cfgA.Version()

	// Break replica B's storage read and pin B as follower so it can't take
	// leadership on the next poll cycle (which would bypass GetSnapshot).
	flakyB.failGetSnapshot.Store(true)
	flakyB.neverLeader.Store(true)
	now2 := now.Add(time.Hour)
	src.update([]e2eTPCArticle{
		{ID: 1, Name: "v2", Score: 1},
		{ID: 2, Name: "v2-extra", Score: 2},
	}, now2)

	mgrA.SyncNow(ctx)

	// Wait past the prepare timeout so the leader has aborted.
	time.Sleep(7 * time.Second)

	// Neither replica must have advanced past v1.
	if !cfgA.Version().Equal(v1) {
		t.Errorf("A advanced past v1 despite broken B: version=%q count=%d", cfgA.Version(), cfgA.Count())
	}
	if !cfgB.Version().Equal(v1) {
		t.Errorf("B advanced past v1 despite broken storage: version=%q count=%d", cfgB.Version(), cfgB.Count())
	}

	// Verify the leader marked the v2 snapshot as failed.
	failedSnap, err := storeReal.GetSnapshot(ctx, collection, dcfg.NewVersion(now2).String())
	if err != nil {
		t.Errorf("expected v2 snapshot in storage: %v", err)
	} else if failedSnap.Status != storage.StatusFailed {
		t.Errorf("v2 snapshot status = %q, want 'failed'", failedSnap.Status)
	}

	// Recover B and re-trigger. Bump the version forward so the leader actually re-syncs.
	flakyB.failGetSnapshot.Store(false)
	flakyB.neverLeader.Store(false)
	now3 := now2.Add(time.Hour)
	src.update([]e2eTPCArticle{
		{ID: 1, Name: "v3", Score: 1},
		{ID: 2, Name: "v3-extra", Score: 2},
	}, now3)
	mgrA.SyncNow(ctx)

	if !waitUntil(20*time.Second, func() bool {
		return cfgA.Count() == 2 && cfgB.Count() == 2 &&
			cfgA.Version().Equal(cfgB.Version()) && !cfgA.Version().Equal(v1)
	}) {
		t.Fatalf("did not converge after recovery: A=%d/%q B=%d/%q (v1=%q)",
			cfgA.Count(), cfgA.Version(), cfgB.Count(), cfgB.Version(), v1)
	}

	cancelA()
	cancelB()
	<-errA
	<-errB
}

// waitUntil polls predicate every 100ms until it returns true or timeout.
func waitUntil(timeout time.Duration, predicate func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if predicate() {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return predicate()
}

// silence unused import warnings if compiler ever strips them
var _ = notify.ActionPrepare
