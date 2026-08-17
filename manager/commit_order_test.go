package manager_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/swchck/director/config"
	"github.com/swchck/director/manager"
	"github.com/swchck/director/notify"
	"github.com/swchck/director/storage"
)

// syncCompletedCount counts clean leader syncs; every fixture here registers
// exactly one collection, so there is nothing to filter by name.
func syncCompletedCount(m *recordingMetrics) int {
	return len(m.snapshot().syncCompleted)
}

// eventualFixture drives the default eventually-consistent leader path. With any peer
// present the confirmation wait runs its full timeout: only the leader logs "applied".
type eventualFixture struct {
	store    *mockStorage
	notif    *twoPCNotifier
	logger   *captureLogger
	metrics  *recordingMetrics
	mgr      *manager.Manager
	articles *config.Collection[twoPCArticle]
	version  string
}

func newEventualFixture(t *testing.T, peers ...string) *eventualFixture {
	t.Helper()

	now := time.Now().UTC().Truncate(time.Second)

	store := newMockStorage()
	reg := newTwoPCRegistry(append([]string{"leader"}, peers...)...)
	logger := &captureLogger{}
	metrics := &recordingMetrics{}
	notif := newTwoPCNotifier()

	articles := config.NewCollection[twoPCArticle]("articles")

	mgr := manager.New(store, notif, reg, manager.Options{
		PollInterval:             time.Hour,
		HeartbeatInterval:        time.Hour,
		WaitConfirmationsTimeout: 600 * time.Millisecond,
		ServiceName:              "test-svc",
	},
		manager.WithInstanceID("leader"),
		manager.WithLogger(logger),
		manager.WithMetrics(metrics),
	)

	manager.RegisterCollectionSource(mgr, articles, &twoPCSource{
		items:        []twoPCArticle{{ID: 1, Name: "Alpha"}},
		lastModified: now,
	})

	return &eventualFixture{
		store:    store,
		notif:    notif,
		logger:   logger,
		metrics:  metrics,
		mgr:      mgr,
		articles: articles,
		version:  config.NewVersion(now).String(),
	}
}

func (f *eventualFixture) lastSyncErr() string {
	for _, c := range f.mgr.Status().Configs {
		if c.Name == "articles" {
			return c.LastSyncErr
		}
	}
	return ""
}

// TestLeaderSync_ActivatesBeforeAnnouncing: followers reconcile against the *active*
// snapshot, so announcing first reverts every replica for the confirmation wait.
func TestLeaderSync_ActivatesBeforeAnnouncing(t *testing.T) {
	f := newEventualFixture(t, "follower-1")

	var mu sync.Mutex
	var activeAtAnnounce []string

	f.notif.onPublish = func(_ context.Context, ev notify.Event) {
		if ev.Action != notify.ActionSync {
			return
		}
		snap, err := f.store.GetActiveSnapshot(context.Background(), ev.Collection)
		active := ""
		if err == nil {
			active = snap.Version
		}
		mu.Lock()
		activeAtAnnounce = append(activeAtAnnounce, active)
		mu.Unlock()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- f.mgr.Start(ctx) }()

	waitFor(t, 3*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(activeAtAnnounce) == 1
	})

	mu.Lock()
	observed := slices.Clone(activeAtAnnounce)
	mu.Unlock()

	if len(observed) != 1 {
		t.Fatalf("sync events announced = %d, want 1", len(observed))
	}
	if observed[0] != f.version {
		t.Errorf("active version when the sync event was announced = %q, want %q", observed[0], f.version)
	}

	// The wait must not be a gate: it times out here, and the sync still finishes.
	waitFor(t, 3*time.Second, func() bool { return syncCompletedCount(f.metrics) == 1 })
	if got := syncCompletedCount(f.metrics); got != 1 {
		t.Errorf("SyncCompleted = %d, want 1 despite the unconfirmed peer", got)
	}

	cancel()
	<-errCh
}

// TestLeaderSync_ActivationFailureRetriedOnNextCycle: a failed activation must leave
// the version un-advanced, or the version check skips every later cycle.
func TestLeaderSync_ActivationFailureRetriedOnNextCycle(t *testing.T) {
	f := newEventualFixture(t)

	var failing atomic.Bool
	var attempts atomic.Int32
	failing.Store(true)

	f.store.onActivateSnapshot = func(string, string) error {
		attempts.Add(1)
		if failing.Load() {
			return errors.New("connection reset by peer")
		}
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- f.mgr.Start(ctx) }()

	waitFor(t, 3*time.Second, func() bool { return attempts.Load() >= 1 && f.lastSyncErr() != "" })

	if got := attempts.Load(); got < 1 {
		t.Fatalf("activation attempts = %d, want at least 1", got)
	}
	if f.lastSyncErr() == "" {
		t.Fatal("LastSyncErr is empty, want the failed activation reported")
	}
	if f.articles.Count() != 0 {
		t.Errorf("leader applied %d items over an un-activated snapshot", f.articles.Count())
	}
	if !f.articles.Version().IsZero() {
		t.Errorf("in-memory version advanced to %q ahead of the active snapshot", f.articles.Version())
	}
	if _, err := f.store.GetActiveSnapshot(ctx, "articles"); !errors.Is(err, storage.ErrSnapshotNotFound) {
		t.Errorf("GetActiveSnapshot err = %v, want %v", err, storage.ErrSnapshotNotFound)
	}

	// Storage recovers: a later cycle must retry the same version, not skip it.
	// LastSyncErr is stamped when the cycle returns, so wait for it too.
	failing.Store(false)
	waitFor(t, 5*time.Second, func() bool {
		f.mgr.SyncNow(ctx)
		return f.articles.Count() == 1 && f.lastSyncErr() == ""
	})

	if f.articles.Count() != 1 {
		t.Fatalf("articles.Count() = %d, want 1 — the retry never applied", f.articles.Count())
	}
	if got := f.store.activeArticleVersions(); len(got) != 1 || got[0] != f.version {
		t.Errorf("active versions = %v, want exactly [%s]", got, f.version)
	}
	if got := f.lastSyncErr(); got != "" {
		t.Errorf("LastSyncErr = %q, want empty after the successful retry", got)
	}

	cancel()
	<-errCh
}

// TestLeaderSync_ApplyLogFailureDoesNotFailTheSync: by then the snapshot is active, the
// value is live and the event is out, so a lost diagnostic row is not a failure.
func TestLeaderSync_ApplyLogFailureDoesNotFailTheSync(t *testing.T) {
	f := newEventualFixture(t, "follower-1")

	var attempts atomic.Int32
	f.store.onLogApply = func(_, _, _, status string) error {
		if status != "applied" {
			return nil
		}
		attempts.Add(1)
		return errors.New("connection reset by peer")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- f.mgr.Start(ctx) }()

	// The confirmation wait is the last thing the cycle does and it times out
	// here, since only the leader ever confirms.
	waitFor(t, 5*time.Second, func() bool { return f.logger.warnCount("confirmations timed out") == 1 })

	if got := attempts.Load(); got < 1 {
		t.Fatalf("apply-log writes attempted = %d, want at least 1", got)
	}
	if got := syncCompletedCount(f.metrics); got != 1 {
		t.Errorf("SyncCompleted = %d, want 1 — a sync every replica is serving was reported as failed", got)
	}
	if got := len(f.metrics.snapshot().syncFailed); got != 0 {
		t.Errorf("SyncFailed = %d, want 0", got)
	}
	if got := f.lastSyncErr(); got != "" {
		t.Errorf("LastSyncErr = %q, want empty", got)
	}
	if got := f.logger.warnCount("confirmations timed out"); got != 1 {
		t.Errorf("confirmation-wait warns = %d, want 1 — the wait was skipped", got)
	}
	if f.articles.Count() != 1 {
		t.Errorf("articles.Count() = %d, want 1", f.articles.Count())
	}
	if got := f.store.activeArticleVersions(); len(got) != 1 || got[0] != f.version {
		t.Errorf("active versions = %v, want exactly [%s]", got, f.version)
	}

	cancel()
	<-errCh
}

// TestLeaderSync_OnChangeHookFailureStillCompletes: activation precedes the local swap,
// so a panicking consumer hook can no longer strand the cluster on the old version.
func TestLeaderSync_OnChangeHookFailureStillCompletes(t *testing.T) {
	f := newEventualFixture(t)
	f.articles.OnChange(func([]twoPCArticle, []twoPCArticle) {
		panic("consumer hook blew up")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- f.mgr.Start(ctx) }()

	waitFor(t, 3*time.Second, func() bool { return syncCompletedCount(f.metrics) == 1 })

	if got := syncCompletedCount(f.metrics); got != 1 {
		t.Fatalf("SyncCompleted = %d, want 1 despite the panicking hook", got)
	}
	if f.articles.Count() != 1 {
		t.Errorf("articles.Count() = %d, want 1", f.articles.Count())
	}
	if got := f.store.activeArticleVersions(); len(got) != 1 || got[0] != f.version {
		t.Errorf("active versions = %v, want exactly [%s]", got, f.version)
	}

	applied, err := f.store.AppliedInstances(ctx, "articles", f.version, "applied")
	if err != nil {
		t.Fatalf("AppliedInstances: %v", err)
	}
	if !slices.Contains(applied, "leader") {
		t.Errorf("applied instances = %v, want to contain %q", applied, "leader")
	}
	if got := f.lastSyncErr(); got != "" {
		t.Errorf("LastSyncErr = %q, want empty", got)
	}

	cancel()
	<-errCh
}

// commitOrderFixture drives a 2PC round with one remote follower that prepares
// synchronously, so every round reaches commit without waiting on a prepare tick.
type commitOrderFixture struct {
	store    *mockStorage
	notif    *twoPCNotifier
	logger   *captureLogger
	metrics  *recordingMetrics
	mgr      *manager.Manager
	articles *config.Collection[twoPCArticle]
	version  string
}

func newCommitOrderFixture(t *testing.T, prepareTTL time.Duration) *commitOrderFixture {
	t.Helper()

	now := time.Now().UTC().Truncate(time.Second)

	store := newMockStorage()
	reg := newTwoPCRegistry("leader", "follower-1")
	logger := &captureLogger{}
	metrics := &recordingMetrics{}

	notif := newTwoPCNotifier()
	notif.onPublish = func(ctx context.Context, ev notify.Event) {
		if ev.Action != notify.ActionPrepare {
			return
		}
		if err := store.LogApply(ctx, "follower-1", ev.Collection, ev.Version, "prepared"); err != nil {
			t.Errorf("follower prepare: %v", err)
		}
	}

	articles := config.NewCollection[twoPCArticle]("articles")

	mgr := manager.New(store, notif, reg, manager.Options{
		PollInterval:             time.Hour,
		HeartbeatInterval:        time.Hour,
		WaitConfirmationsTimeout: time.Second,
		PrepareTTL:               prepareTTL,
		ServiceName:              "test-svc",
		RequireUnanimousApply:    true,
	},
		manager.WithInstanceID("leader"),
		manager.WithLogger(logger),
		manager.WithMetrics(metrics),
	)

	manager.RegisterCollectionSource(mgr, articles, &twoPCSource{
		items:        []twoPCArticle{{ID: 1, Name: "Alpha"}},
		lastModified: now,
	})

	return &commitOrderFixture{
		store:    store,
		notif:    notif,
		logger:   logger,
		metrics:  metrics,
		mgr:      mgr,
		articles: articles,
		version:  config.NewVersion(now).String(),
	}
}

// seedActiveSnapshot makes storage hold an active snapshot at the given time, so
// the manager starts up already loaded instead of on a first deploy.
func (f *commitOrderFixture) seedActiveSnapshot(t *testing.T, at time.Time) {
	t.Helper()

	ctx := context.Background()
	version := config.NewVersion(at).String()

	if err := f.store.SaveSnapshot(ctx, "articles", version, []byte(`[{"id":9,"name":"Seeded"}]`)); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	// forceActive, not ActivateSnapshot: a test that installs onActivateSnapshot must
	// still be able to seed.
	f.store.forceActive("articles", version)
}

func (f *commitOrderFixture) countPublished(action string) int {
	n := 0
	for _, ev := range f.notif.publishedEvents() {
		if ev.Action == action {
			n++
		}
	}
	return n
}

func (f *commitOrderFixture) lastSyncErr() string {
	for _, c := range f.mgr.Status().Configs {
		if c.Name == "articles" {
			return c.LastSyncErr
		}
	}
	return ""
}

// TestTwoPC_ActivationFailureAbortsRound: activation is the commit decision, so a
// failure there aborts the round even though every replica prepared.
func TestTwoPC_ActivationFailureAbortsRound(t *testing.T) {
	f := newCommitOrderFixture(t, 3*time.Second)
	f.store.onActivateSnapshot = func(string, string) error {
		return errors.New("connection reset")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- f.mgr.Start(ctx) }()

	waitFor(t, 3*time.Second, func() bool {
		return f.countPublished(notify.ActionAbort) == 1
	})

	if got := f.countPublished(notify.ActionAbort); got != 1 {
		t.Fatalf("abort events published = %d, want 1", got)
	}
	if got := f.countPublished(notify.ActionCommit); got != 0 {
		t.Errorf("commit events published = %d, want 0 — the decision was never made", got)
	}

	snap, err := f.store.GetSnapshot(ctx, "articles", f.version)
	if err != nil {
		t.Fatalf("GetSnapshot: %v", err)
	}
	if snap.Status != storage.StatusFailed {
		t.Errorf("snapshot status = %q, want %q", snap.Status, storage.StatusFailed)
	}

	if _, err := f.store.GetActiveSnapshot(ctx, "articles"); !errors.Is(err, storage.ErrSnapshotNotFound) {
		t.Errorf("GetActiveSnapshot err = %v, want %v", err, storage.ErrSnapshotNotFound)
	}

	if !f.articles.Version().IsZero() {
		t.Errorf("in-memory version advanced to %q despite abort", f.articles.Version())
	}
	if f.articles.Count() != 0 {
		t.Errorf("leader applied %d items despite abort", f.articles.Count())
	}

	if got := f.metrics.stagedDroppedCount("articles:activate_failed"); got != 1 {
		t.Errorf("StagedDropped(activate_failed) = %d, want 1", got)
	}
	if got := f.logger.warnCount("reason=activate_failed"); got != 1 {
		t.Errorf("abort warn count = %d, want 1", got)
	}

	cancel()
	<-errCh
}

// TestTwoPC_ActivationFailureWarnDeduped: repeated rounds on one version must warn
// once, so a persistently failing activation cannot spam a line per poll cycle.
func TestTwoPC_ActivationFailureWarnDeduped(t *testing.T) {
	f := newCommitOrderFixture(t, 3*time.Second)
	f.store.onActivateSnapshot = func(string, string) error {
		return errors.New("connection reset")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// An active older snapshot makes this a running replica: dedup is per version,
	// and a replica that has never loaded keeps reporting instead.
	f.seedActiveSnapshot(t, time.Now().UTC().Truncate(time.Second).Add(-time.Hour))

	errCh := make(chan error, 1)
	go func() { errCh <- f.mgr.Start(ctx) }()

	// Retry until several rounds have aborted on the same version. SyncNow coalesces
	// into the running cycle, so drive it off the metric, not a fixed call count.
	waitFor(t, 5*time.Second, func() bool {
		f.mgr.SyncNow(ctx)
		return f.metrics.stagedDroppedCount("articles:activate_failed") >= 3
	})

	if got := f.metrics.stagedDroppedCount("articles:activate_failed"); got < 3 {
		t.Fatalf("StagedDropped(activate_failed) = %d, want at least 3 rounds", got)
	}
	if got := f.logger.warnCount("2PC aborting round"); got != 1 {
		t.Errorf("abort warn fired %d times for the same version, want 1", got)
	}

	cancel()
	<-errCh
}

// TestTwoPC_CommitOutlivingPrepareTTLStillApplies: staged values expire on the run
// loop, so nothing can drop the leader's value while that loop is mid-round with it.
func TestTwoPC_CommitOutlivingPrepareTTLStillApplies(t *testing.T) {
	f := newCommitOrderFixture(t, 20*time.Millisecond)

	// Outlive the staged TTL between the commit broadcast and the local apply.
	f.notif.onPublish = func(ctx context.Context, ev notify.Event) {
		switch ev.Action {
		case notify.ActionPrepare:
			if err := f.store.LogApply(ctx, "follower-1", ev.Collection, ev.Version, "prepared"); err != nil {
				t.Errorf("follower prepare: %v", err)
			}
		case notify.ActionCommit:
			time.Sleep(300 * time.Millisecond)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- f.mgr.Start(ctx) }()

	waitFor(t, 3*time.Second, func() bool { return f.countPublished(notify.ActionSync) == 1 })

	if got := f.articles.Version().String(); got != f.version {
		t.Errorf("in-memory version = %q, want %q — the staged value was dropped mid-round", got, f.version)
	}
	if got := f.articles.Count(); got != 1 {
		t.Errorf("articles.Count() = %d, want 1", got)
	}

	active, err := f.store.GetActiveSnapshot(ctx, "articles")
	if err != nil {
		t.Fatalf("GetActiveSnapshot: %v", err)
	}
	if active.Version != f.version {
		t.Errorf("active version = %q, want %q", active.Version, f.version)
	}

	committed, err := f.store.AppliedInstances(ctx, "articles", f.version, "committed")
	if err != nil {
		t.Fatalf("AppliedInstances: %v", err)
	}
	if !slices.Contains(committed, "leader") {
		t.Errorf("committed instances = %v, want to contain %q", committed, "leader")
	}

	if got := f.metrics.stagedDroppedCount("articles:ttl"); got != 0 {
		t.Errorf("StagedDropped(ttl) = %d, want 0 — the sweep cannot run mid-round", got)
	}
	if got := f.countPublished(notify.ActionAbort); got != 0 {
		t.Errorf("abort events = %d, want 0 — a durable decision is never aborted", got)
	}
	if got := f.lastSyncErr(); got != "" {
		t.Errorf("LastSyncErr = %q, want empty — the round committed everywhere", got)
	}
	if got := syncCompletedCount(f.metrics); got != 1 {
		t.Errorf("SyncCompleted = %d, want 1", got)
	}

	cancel()
	<-errCh
}

// TestTwoPC_OnChangeHookFailureStillCommits: config.Swap publishes before running
// hooks, so a panicking hook leaves the value live and the round genuinely committed.
func TestTwoPC_OnChangeHookFailureStillCommits(t *testing.T) {
	f := newCommitOrderFixture(t, 3*time.Second)
	f.articles.OnChange(func([]twoPCArticle, []twoPCArticle) {
		panic("consumer hook blew up")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- f.mgr.Start(ctx) }()

	waitFor(t, 3*time.Second, func() bool { return f.articles.Count() == 1 })

	if f.articles.Count() != 1 {
		t.Fatalf("articles.Count() = %d, want 1 — the value must be live despite the hook", f.articles.Count())
	}

	committed, err := f.store.AppliedInstances(ctx, "articles", f.version, "committed")
	if err != nil {
		t.Fatalf("AppliedInstances: %v", err)
	}
	if !slices.Contains(committed, "leader") {
		t.Errorf("committed instances = %v, want to contain %q", committed, "leader")
	}

	if got := f.logger.warnCount("on-change hook failed after the value was applied"); got != 1 {
		t.Errorf("hook failure warn count = %d, want 1", got)
	}
	if got := f.lastSyncErr(); got != "" {
		t.Errorf("LastSyncErr = %q, want empty — the round committed", got)
	}

	cancel()
	<-errCh
}

// TestTwoPC_Singleton_OnChangeHookFailureStillCommits: Singleton.Swap publishes before
// hooks too, so the singleton path must classify a panicking hook as applied.
func TestTwoPC_Singleton_OnChangeHookFailureStillCommits(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	version := config.NewVersion(now).String()

	store := newMockStorage()
	logger := &captureLogger{}

	notif := newTwoPCNotifier()
	notif.onPublish = func(ctx context.Context, ev notify.Event) {
		if ev.Action != notify.ActionPrepare {
			return
		}
		if err := store.LogApply(ctx, "follower-1", ev.Collection, ev.Version, "prepared"); err != nil {
			t.Errorf("follower prepare: %v", err)
		}
	}

	profile := config.NewSingleton[twoPCProfile]("profile")
	profile.OnChange(func(*twoPCProfile, *twoPCProfile) {
		panic("consumer hook blew up")
	})

	mgr := manager.New(store, notif, newTwoPCRegistry("leader", "follower-1"), manager.Options{
		PollInterval:             time.Hour,
		HeartbeatInterval:        time.Hour,
		WaitConfirmationsTimeout: time.Second,
		PrepareTTL:               3 * time.Second,
		ServiceName:              "test-svc",
		RequireUnanimousApply:    true,
	},
		manager.WithInstanceID("leader"),
		manager.WithLogger(logger),
	)

	manager.RegisterSingletonSource(mgr, profile, &twoPCSingletonSource{
		value:        twoPCProfile{Title: "Hello", Build: 42},
		lastModified: now,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- mgr.Start(ctx) }()

	// LastSyncAt is stamped once the round returns, so it is the last observable
	// of the whole cycle — the committed row and the warn are already written.
	profileStatus := func() manager.ConfigStatus {
		for _, c := range mgr.Status().Configs {
			if c.Name == "profile" {
				return c
			}
		}
		return manager.ConfigStatus{}
	}
	waitFor(t, 3*time.Second, func() bool { return !profileStatus().LastSyncAt.IsZero() })

	v, ok := profile.Get()
	if !ok || v.Build != 42 {
		t.Fatalf("profile = %+v (ok=%v), want {Hello 42} — the value must be live despite the hook", v, ok)
	}

	committed, err := store.AppliedInstances(ctx, "profile", version, "committed")
	if err != nil {
		t.Fatalf("AppliedInstances: %v", err)
	}
	if !slices.Contains(committed, "leader") {
		t.Errorf("committed instances = %v, want to contain %q", committed, "leader")
	}

	if got := logger.warnCount("on-change hook failed after the value was applied"); got != 1 {
		t.Errorf("hook failure warn count = %d, want 1", got)
	}
	if got := profileStatus().LastSyncErr; got != "" {
		t.Errorf("LastSyncErr = %q, want empty — the round committed", got)
	}

	cancel()
	<-errCh
}

// TestTwoPC_AmbiguousActivationKeepsActiveSnapshot: an activation can commit
// server-side yet error, and demoting the only active snapshot strands a restart.
func TestTwoPC_AmbiguousActivationKeepsActiveSnapshot(t *testing.T) {
	f := newCommitOrderFixture(t, 3*time.Second)
	f.store.onActivateSnapshot = func(collection, version string) error {
		f.store.forceActive(collection, version)
		return errors.New("connection reset by peer")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- f.mgr.Start(ctx) }()

	waitFor(t, 3*time.Second, func() bool {
		return f.countPublished(notify.ActionAbort) == 1
	})

	if got := f.countPublished(notify.ActionAbort); got != 1 {
		t.Fatalf("abort events published = %d, want 1", got)
	}

	if got := f.store.activeArticleVersions(); len(got) != 1 || got[0] != f.version {
		t.Errorf("active versions = %v, want exactly [%s]", got, f.version)
	}

	snap, err := f.store.GetSnapshot(ctx, "articles", f.version)
	if err != nil {
		t.Fatalf("GetSnapshot: %v", err)
	}
	if snap.Status == storage.StatusFailed {
		t.Error("snapshot marked failed while it is the active one")
	}

	if got := f.logger.warnCount("leaving the snapshot in place"); got != 1 {
		t.Errorf("ambiguous activation warn count = %d, want 1", got)
	}

	cancel()
	<-errCh
}

// TestTwoPC_UnreadableActiveVersionLeavesSnapshotUnfailed: with storage unreachable the
// leader cannot tell whether its activation landed, so it must not fail the snapshot.
func TestTwoPC_UnreadableActiveVersionLeavesSnapshotUnfailed(t *testing.T) {
	f := newCommitOrderFixture(t, 3*time.Second)

	var activationTried atomic.Bool
	f.store.onActivateSnapshot = func(string, string) error {
		activationTried.Store(true)
		return errors.New("connection reset by peer")
	}
	f.store.onGetActiveSnapshot = func(string) error {
		if activationTried.Load() {
			return errors.New("connection refused")
		}
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- f.mgr.Start(ctx) }()

	waitFor(t, 3*time.Second, func() bool {
		return f.countPublished(notify.ActionAbort) == 1
	})

	if got := f.countPublished(notify.ActionAbort); got != 1 {
		t.Fatalf("abort events published = %d, want 1", got)
	}

	snap, err := f.store.GetSnapshot(ctx, "articles", f.version)
	if err != nil {
		t.Fatalf("GetSnapshot: %v", err)
	}
	if snap.Status != storage.StatusPending {
		t.Errorf("snapshot status = %q, want %q — the activation outcome is unknown", snap.Status, storage.StatusPending)
	}

	if got := f.logger.warnCount("active version unreadable"); got != 1 {
		t.Errorf("unreadable active version warn count = %d, want 1", got)
	}

	cancel()
	<-errCh
}
