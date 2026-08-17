package manager_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/swchck/director/cache"
	"github.com/swchck/director/config"
	"github.com/swchck/director/manager"
	"github.com/swchck/director/notify"
)

// -- Fixture: follower catch-up reconciles the in-memory version against the
// *active* snapshot each tick, so these tests move the active row underneath.

// catchUpFixture is a replica with v1 (1 item), v2 (2 items) and v3 (3 items) in
// storage, so the item count identifies which version is live.
type catchUpFixture struct {
	store    *mockStorage
	notif    *mockNotifier
	logger   *captureLogger
	metrics  *recordingMetrics
	mgr      *manager.Manager
	articles *config.Collection[twoPCArticle]

	v1, v2, v3 string
}

// catchUpOptions tunes the fixture. The defaults — lock held elsewhere, reconcile
// ticker off — leave a replica reacting only to events a test delivers by hand.
type catchUpOptions struct {
	// leader lets this replica win the advisory lock. The source is aligned with
	// v2 so its initial leader sync finds no version change and skips the fetch.
	leader bool

	// reconcileInterval drives the follower catch-up ticker.
	reconcileInterval time.Duration

	// cache, when set, is attached with cacheStrategy so a scenario can observe
	// what the replica leaves behind for the next pod to warm-start from.
	cache         *trackingCache
	cacheStrategy cache.Strategy

	// validator, when set, is installed as the collection's pre-apply validator.
	validator func([]twoPCArticle) error
}

func newCatchUpFixture(t *testing.T, opts catchUpOptions) *catchUpFixture {
	t.Helper()

	t1 := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)

	v1 := config.NewVersion(t1).String()
	v2 := config.NewVersion(t2).String()
	v3 := config.NewVersion(t3).String()

	items2 := []twoPCArticle{{ID: 1, Name: "one"}, {ID: 2, Name: "two"}}

	store := newMockStorage()
	seedSnapshot(t, store, v1, []twoPCArticle{{ID: 1, Name: "one"}})
	seedSnapshot(t, store, v2, items2)
	seedSnapshot(t, store, v3, []twoPCArticle{{ID: 1, Name: "one"}, {ID: 2, Name: "two"}, {ID: 3, Name: "three"}})

	store.lockHeld = !opts.leader

	if opts.reconcileInterval == 0 {
		opts.reconcileInterval = time.Hour
	}

	logger := &captureLogger{}
	metrics := &recordingMetrics{}
	notif := newMockNotifier()
	articles := config.NewCollection[twoPCArticle]("articles")

	mgrOpts := []manager.ManagerOption{
		manager.WithInstanceID("inst-1"),
		manager.WithLogger(logger),
		manager.WithMetrics(metrics),
	}
	if opts.cache != nil {
		mgrOpts = append(mgrOpts, manager.WithCache(opts.cache, opts.cacheStrategy))
	}

	mgr := manager.New(store, notif, newTwoPCRegistry("inst-1"), manager.Options{
		PollInterval:             time.Hour,
		HeartbeatInterval:        opts.reconcileInterval,
		WaitConfirmationsTimeout: time.Second,
		ServiceName:              "test-svc",
	}, mgrOpts...)

	var colOpts []manager.CollectionOption[twoPCArticle]
	if opts.validator != nil {
		colOpts = append(colOpts, manager.WithCollectionValidator(opts.validator))
	}

	// A leader must find the source already at v2, or its initial sync advances
	// the collection past the version the test just activated.
	manager.RegisterCollectionSource(mgr, articles, &twoPCSource{items: items2, lastModified: t2}, colOpts...)

	return &catchUpFixture{
		store:    store,
		notif:    notif,
		logger:   logger,
		metrics:  metrics,
		mgr:      mgr,
		articles: articles,
		v1:       v1,
		v2:       v2,
		v3:       v3,
	}
}

func seedSnapshot(t *testing.T, store *mockStorage, version string, items []twoPCArticle) {
	t.Helper()

	content, err := json.Marshal(items)
	if err != nil {
		t.Fatalf("marshal %s: %v", version, err)
	}
	if err := store.SaveSnapshot(context.Background(), "articles", version, content); err != nil {
		t.Fatalf("seed snapshot %s: %v", version, err)
	}
}

func (f *catchUpFixture) start(t *testing.T) {
	t.Helper()
	startManager(t, f.mgr, f.logger)
}

// refusals counts FollowerFailed calls reporting a refused backward catch-up.
func (f *catchUpFixture) refusals() int {
	return f.metrics.followerFailedCount("articles", manager.ErrActiveSnapshotBehind)
}

func (f *catchUpFixture) refusalWarns() int {
	return f.logger.warnCount("refusing to move to an older active version")
}

func (f *catchUpFixture) version() string {
	return f.articles.Version().String()
}

// activeVersion reports the version storage currently names active.
func (f *catchUpFixture) activeVersion(t *testing.T) string {
	t.Helper()

	snap, err := f.store.GetActiveSnapshot(context.Background(), "articles")
	if err != nil {
		t.Fatalf("get active snapshot: %v", err)
	}

	return snap.Version
}

func (f *catchUpFixture) appliedStatus(t *testing.T, version, status string) bool {
	t.Helper()

	ids, err := f.store.AppliedInstances(context.Background(), "articles", version, status)
	if err != nil {
		t.Fatalf("applied instances: %v", err)
	}

	return slices.Contains(ids, "inst-1")
}

// -- Monotonic catch-up ----------------------------------------------------

// TestFollowerCatchUp_RefusesOlderActiveVersion: every replica reconciles against the
// same active row, so one stale row would regress the whole cluster within a tick.
func TestFollowerCatchUp_RefusesOlderActiveVersion(t *testing.T) {
	f := newCatchUpFixture(t, catchUpOptions{reconcileInterval: 50 * time.Millisecond})
	f.store.forceActive("articles", f.v2)
	f.start(t)

	waitFor(t, 2*time.Second, func() bool { return f.version() == f.v2 })
	if got := f.version(); got != f.v2 {
		t.Fatalf("in-memory version = %q, want %q before the regression", got, f.v2)
	}

	// Storage falls behind the cluster: the active row now names the old version.
	f.store.forceActive("articles", f.v1)

	// Wait on the warn, which the refusal emits after the metric — waiting on the
	// metric can land between the two and read zero warns.
	waitFor(t, 2*time.Second, func() bool { return f.refusalWarns() > 0 })

	if got := f.version(); got != f.v2 {
		t.Errorf("in-memory version = %q, want %q — catch-up moved the replica backwards", got, f.v2)
	}
	if got := f.articles.Count(); got != 2 {
		t.Errorf("articles.Count() = %d, want 2 — an older payload was applied", got)
	}
	if got := f.refusals(); got < 1 {
		t.Errorf("FollowerFailed(ErrActiveSnapshotBehind) = %d, want at least 1 — the refusal is not alertable", got)
	}
	if got := f.refusalWarns(); got != 1 {
		t.Errorf("refusal warns = %d, want 1", got)
	}
	if got := f.logger.warnCount("local=" + f.v2 + " active=" + f.v1); got != 1 {
		t.Errorf("refusal warn naming local=%s active=%s fired %d times, want 1", f.v2, f.v1, got)
	}
	if got := f.metrics.followerAppliedCount("articles"); got != 0 {
		t.Errorf("FollowerApplied = %d, want 0 — nothing was applied", got)
	}
}

// TestFollowerCatchUp_RegressionWarnDedupedAndResetOnApply: the refusal repeats every
// tick, so the warn is deduped per version — and must reset on the next apply.
func TestFollowerCatchUp_RegressionWarnDedupedAndResetOnApply(t *testing.T) {
	f := newCatchUpFixture(t, catchUpOptions{reconcileInterval: 50 * time.Millisecond})
	f.store.forceActive("articles", f.v2)
	f.start(t)

	waitFor(t, 2*time.Second, func() bool { return f.version() == f.v2 })

	f.store.forceActive("articles", f.v1)

	// Several ticks must refuse before the dedup assertion means anything.
	waitFor(t, 3*time.Second, func() bool { return f.refusals() >= 3 })
	if got := f.refusals(); got < 3 {
		t.Fatalf("refusals = %d, want at least 3 ticks", got)
	}
	if got := f.refusalWarns(); got != 1 {
		t.Errorf("refusal warns = %d over %d refused ticks, want 1", got, f.refusals())
	}

	// The active row moves forward again: catch-up applies it and clears the
	// dedup state along with every other per-version failure record.
	f.store.forceActive("articles", f.v3)
	waitFor(t, 2*time.Second, func() bool { return f.version() == f.v3 })
	if got := f.version(); got != f.v3 {
		t.Fatalf("in-memory version = %q, want %q — forward catch-up did not run", got, f.v3)
	}

	// Same older version refused again — this is a new incident and must warn.
	f.store.forceActive("articles", f.v1)
	waitFor(t, 2*time.Second, func() bool { return f.refusalWarns() >= 2 })

	if got := f.refusalWarns(); got != 2 {
		t.Errorf("refusal warns = %d, want 2 — the dedup did not reset after a successful apply", got)
	}
	if got := f.version(); got != f.v3 {
		t.Errorf("in-memory version = %q, want %q", got, f.v3)
	}
}

// TestFollowerCatchUp_ZeroLocalVersionLoadsActive: 0001-01-01, what a source with no
// LastModified produces, is not after a fresh replica's zero — yet must still load.
func TestFollowerCatchUp_ZeroLocalVersionLoadsActive(t *testing.T) {
	zeroVersion := config.NewVersion(time.Time{}).String()

	tests := []struct {
		name    string
		active  func(f *catchUpFixture) string
		want    func(f *catchUpFixture) string
		wantLen int
	}{
		{
			name:    "active names a real timestamp",
			active:  func(f *catchUpFixture) string { return f.v3 },
			want:    func(f *catchUpFixture) string { return f.v3 },
			wantLen: 3,
		},
		{
			name:    "active names the zero timestamp",
			active:  func(*catchUpFixture) string { return zeroVersion },
			want:    func(*catchUpFixture) string { return zeroVersion },
			wantLen: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newCatchUpFixture(t, catchUpOptions{reconcileInterval: 50 * time.Millisecond})
			seedSnapshot(t, f.store, zeroVersion, []twoPCArticle{{ID: 1, Name: "one"}})

			// No active snapshot at Start, so the replica comes up at version zero.
			f.start(t)

			if got := f.version(); got != "" {
				t.Fatalf("in-memory version = %q, want empty at start", got)
			}

			f.store.forceActive("articles", tc.active(f))

			want := tc.want(f)
			waitFor(t, 2*time.Second, func() bool { return f.version() == want })

			if got := f.version(); got != want {
				t.Errorf("in-memory version = %q, want %q — a replica holding nothing must load the active snapshot", got, want)
			}
			if got := f.articles.Count(); got != tc.wantLen {
				t.Errorf("articles.Count() = %d, want %d", got, tc.wantLen)
			}
			if got := f.refusals(); got != 0 {
				t.Errorf("refusals = %d, want 0", got)
			}
			if !f.mgr.Ready() {
				t.Error("manager is not Ready after loading the active snapshot")
			}
		})
	}
}

// TestFollowerCatchUp_ForwardStillApplies: the self-heal this loop exists for —
// repairing a replica that missed a notification — must keep working unchanged.
func TestFollowerCatchUp_ForwardStillApplies(t *testing.T) {
	f := newCatchUpFixture(t, catchUpOptions{reconcileInterval: 50 * time.Millisecond})
	f.store.forceActive("articles", f.v2)
	f.start(t)

	waitFor(t, 2*time.Second, func() bool { return f.version() == f.v2 })

	// The notification for v3 never arrives; catch-up is the repair path.
	f.store.forceActive("articles", f.v3)

	waitFor(t, 2*time.Second, func() bool { return f.version() == f.v3 })

	if got := f.version(); got != f.v3 {
		t.Fatalf("in-memory version = %q, want %q", got, f.v3)
	}
	if got := f.articles.Count(); got != 3 {
		t.Errorf("articles.Count() = %d, want 3", got)
	}
	if !f.appliedStatus(t, f.v3, "applied") {
		t.Error("apply log has no 'applied' row for the caught-up version")
	}
	if f.metrics.followerAppliedCount("articles") == 0 {
		t.Error("FollowerApplied never fired for a forward catch-up")
	}
	if got := f.refusals(); got != 0 {
		t.Errorf("refusals = %d, want 0 for a forward move", got)
	}
}

// TestFollowerCatchUp_RefusalKeepsValidatorDedup: sharing one dedup slot would let the
// per-tick refusal clear the validator's record and re-report the same bad version.
func TestFollowerCatchUp_RefusalKeepsValidatorDedup(t *testing.T) {
	badVersion := config.NewVersion(time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)).String()

	f := newCatchUpFixture(t, catchUpOptions{
		reconcileInterval: 50 * time.Millisecond,
		validator: func(items []twoPCArticle) error {
			for _, it := range items {
				if it.Name == "bad" {
					return errors.New("name=bad rejected")
				}
			}
			return nil
		},
	})
	seedSnapshot(t, f.store, badVersion, []twoPCArticle{{ID: 9, Name: "bad"}})

	// Count writes per status: the mock upserts, so a repeated rejection leaves
	// no trace in the apply log itself.
	var rejectedWrites atomic.Int32
	f.store.onLogApply = func(_, _, version, status string) error {
		if version == badVersion && status == "validation_failed" {
			rejectedWrites.Add(1)
		}
		return nil
	}

	f.store.forceActive("articles", f.v2)
	f.start(t)

	waitFor(t, 2*time.Second, func() bool { return f.version() == f.v2 })

	// Storage falls behind, so every tick from here on refuses.
	f.store.forceActive("articles", f.v1)
	waitFor(t, 2*time.Second, func() bool { return f.refusals() >= 1 })

	badEvent := notify.Event{
		Action:     notify.ActionSync,
		Collection: "articles",
		Version:    badVersion,
		InstanceID: "inst-2",
	}

	f.notif.subCh <- badEvent
	waitFor(t, 2*time.Second, func() bool { return rejectedWrites.Load() >= 1 })

	// Let several refusals land between the two rejections of the same version.
	refusalsBefore := f.refusals()
	waitFor(t, 2*time.Second, func() bool { return f.refusals() >= refusalsBefore+2 })

	f.notif.subCh <- badEvent
	waitFor(t, 2*time.Second, func() bool { return rejectedWrites.Load() >= 2 })

	if got := rejectedWrites.Load(); got < 2 {
		t.Fatalf("rejected apply-log writes = %d, want the bad version rejected twice", got)
	}
	if got := f.refusals(); got <= refusalsBefore {
		t.Fatalf("refusals = %d, want more than the %d seen before the rejection", got, refusalsBefore)
	}
	if got := len(f.metrics.snapshot().validationFailed); got != 1 {
		t.Errorf("ValidationFailed = %d for one bad version, want 1 — the refusal cleared the validator's dedup", got)
	}
	if got := f.logger.warnCount("config update rejected"); got != 1 {
		t.Errorf("rejection warns = %d, want 1", got)
	}
	// The refusal keeps its own record: still one warn per active version.
	if got := f.refusalWarns(); got != 1 {
		t.Errorf("refusal warns = %d, want 1 — the validator rejection cleared the refusal's dedup", got)
	}
}

// TestHandleSyncEvent_RefusesOlderAnnouncedVersion: the same guard covers the
// announcement path — the one that splits the cluster if a regression ever escapes.
func TestHandleSyncEvent_RefusesOlderAnnouncedVersion(t *testing.T) {
	f := newCatchUpFixture(t, catchUpOptions{reconcileInterval: time.Hour})
	f.store.forceActive("articles", f.v2)
	f.start(t)

	waitFor(t, 2*time.Second, func() bool { return f.version() == f.v2 })
	if got := f.version(); got != f.v2 {
		t.Fatalf("in-memory version = %q, want %q before the announcement", got, f.v2)
	}

	f.notif.subCh <- notify.Event{
		Action:     notify.ActionSync,
		Collection: "articles",
		Version:    f.v1,
		InstanceID: "inst-2",
	}

	waitFor(t, 2*time.Second, func() bool { return f.refusalWarns() > 0 })

	if got := f.version(); got != f.v2 {
		t.Errorf("in-memory version = %q, want %q — the replica followed an older announcement", got, f.v2)
	}
	if got := f.articles.Count(); got != 2 {
		t.Errorf("articles.Count() = %d, want 2 — the older payload was applied", got)
	}
	if got := f.logger.warnCount("path=sync_event"); got != 1 {
		t.Errorf("refusal warns naming the announcement path = %d, want 1", got)
	}
	if got := f.refusals(); got < 1 {
		t.Errorf("FollowerFailed(ErrActiveSnapshotBehind) = %d, want at least 1 — the refusal is not alertable", got)
	}
	if got := f.metrics.followerAppliedCount("articles"); got != 0 {
		t.Errorf("FollowerApplied = %d, want 0 — nothing was applied", got)
	}
	if f.appliedStatus(t, f.v1, "applied") {
		t.Error("apply log claims the refused version was applied")
	}
}

// -- Operator rollback -----------------------------------------------------

// TestHandleRollbackEvent_MovesReplicaBackwards: the operator's only way back, so it
// must work visibly on every role, lock holder included, stamped or not.
func TestHandleRollbackEvent_MovesReplicaBackwards(t *testing.T) {
	roles := []struct {
		name   string
		leader bool
	}{
		{"follower", false},
		{"current leader", true},
	}

	origins := []struct {
		name       string
		instanceID string
	}{
		{"unstamped operator event", ""},
		{"event from another replica", "inst-2"},
	}

	for _, role := range roles {
		for _, origin := range origins {
			t.Run(role.name+"/"+origin.name, func(t *testing.T) {
				f := newCatchUpFixture(t, catchUpOptions{leader: role.leader})
				f.store.forceActive("articles", f.v2)
				f.start(t)

				waitFor(t, 2*time.Second, func() bool { return f.version() == f.v2 })
				if got := f.version(); got != f.v2 {
					t.Fatalf("in-memory version = %q, want %q before the rollback", got, f.v2)
				}
				if got := f.mgr.Status().IsLeader; got != role.leader {
					t.Fatalf("Status().IsLeader = %v, want %v", got, role.leader)
				}

				// The operator procedure: activate the target snapshot, then
				// announce the rollback.
				f.store.forceActive("articles", f.v1)
				f.notif.subCh <- notify.Event{
					Action:     notify.ActionRollback,
					Collection: "articles",
					Version:    f.v1,
					InstanceID: origin.instanceID,
				}

				waitFor(t, 2*time.Second, func() bool { return f.version() == f.v1 })

				if got := f.version(); got != f.v1 {
					t.Errorf("in-memory version = %q, want %q — the rollback did not apply", got, f.v1)
				}
				if got := f.articles.Count(); got != 1 {
					t.Errorf("articles.Count() = %d, want 1", got)
				}
				if got := f.logger.warnCount("operator rollback applied"); got != 1 {
					t.Errorf("rollback warns = %d, want 1 — the guard bypass must be visible", got)
				}
				if got := f.logger.warnCount("from=" + f.v2 + " to=" + f.v1 + " backwards=true"); got != 1 {
					t.Errorf("rollback warn naming from=%s to=%s backwards=true fired %d times, want 1", f.v2, f.v1, got)
				}
				if !f.appliedStatus(t, f.v1, "rolled_back") {
					t.Error("apply log has no 'rolled_back' row — the operator cannot verify the rollback landed")
				}
				if got := f.refusals(); got != 0 {
					t.Errorf("refusals = %d, want 0 — the rollback path must not consult the catch-up guard", got)
				}
			})
		}
	}
}

// TestHandleRollbackEvent_CatchUpKeepsRolledBackVersion: after a rollback the active
// row and memory agree, so reconcile must not read the older version as a regression.
func TestHandleRollbackEvent_CatchUpKeepsRolledBackVersion(t *testing.T) {
	f := newCatchUpFixture(t, catchUpOptions{reconcileInterval: 50 * time.Millisecond})
	f.store.forceActive("articles", f.v2)
	f.start(t)

	waitFor(t, 2*time.Second, func() bool { return f.version() == f.v2 })

	f.store.forceActive("articles", f.v1)
	f.notif.subCh <- notify.Event{
		Action:     notify.ActionRollback,
		Collection: "articles",
		Version:    f.v1,
	}

	waitFor(t, 2*time.Second, func() bool { return f.version() == f.v1 })
	if got := f.version(); got != f.v1 {
		t.Fatalf("in-memory version = %q, want %q", got, f.v1)
	}

	// Let several reconcile ticks pass: the rolled-back version must stick.
	time.Sleep(300 * time.Millisecond)

	if got := f.version(); got != f.v1 {
		t.Errorf("in-memory version = %q, want %q — catch-up moved off the rolled-back version", got, f.v1)
	}
	if got := f.refusals(); got != 0 {
		t.Errorf("refusals = %d, want 0 — local and active agree", got)
	}
}

// TestHandleRollbackEvent_RepairsCacheEntry: nothing else repairs the cache while the
// procedure holds the lock, so each replica rewrites its own from the active snapshot.
func TestHandleRollbackEvent_RepairsCacheEntry(t *testing.T) {
	tests := []struct {
		name     string
		strategy cache.Strategy
		wantVer  func(f *catchUpFixture) string
	}{
		{
			name:     "write-enabled strategy is repaired",
			strategy: cache.ReadWriteThrough,
			wantVer:  func(f *catchUpFixture) string { return f.v1 },
		},
		{
			name:     "read-only strategy is left alone",
			strategy: cache.ReadThrough,
			wantVer:  func(f *catchUpFixture) string { return f.v2 },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tcache := newTrackingCache()
			f := newCatchUpFixture(t, catchUpOptions{cache: tcache, cacheStrategy: tc.strategy})

			f.store.forceActive("articles", f.v2)
			// The leader wrote the cache after activating v2.
			seedCacheVersion(t, tcache, f.v2, []twoPCArticle{{ID: 1, Name: "one"}, {ID: 2, Name: "two"}})
			f.start(t)

			waitFor(t, 2*time.Second, func() bool { return f.version() == f.v2 })

			f.store.forceActive("articles", f.v1)
			f.notif.subCh <- notify.Event{
				Action:     notify.ActionRollback,
				Collection: "articles",
				Version:    f.v1,
			}

			waitFor(t, 2*time.Second, func() bool { return f.version() == f.v1 })
			if got := f.version(); got != f.v1 {
				t.Fatalf("in-memory version = %q, want %q", got, f.v1)
			}
			waitFor(t, 2*time.Second, func() bool {
				e := tcache.articlesEntry()
				return e != nil && e.Version == tc.wantVer(f)
			})

			entry := tcache.articlesEntry()
			if entry == nil {
				t.Fatal("cache entry disappeared")
			}
			if got := entry.Version; got != tc.wantVer(f) {
				t.Errorf("cache version = %q, want %q — a pod warm-starting from it would load the wrong payload",
					got, tc.wantVer(f))
			}
			if tc.strategy == cache.ReadWriteThrough {
				var cached []twoPCArticle
				if err := json.Unmarshal(entry.Content, &cached); err != nil {
					t.Fatalf("unmarshal cached content: %v", err)
				}
				if len(cached) != 1 {
					t.Errorf("cached items = %d, want 1 — the entry names the rolled-back version but holds other content", len(cached))
				}
			}
		})
	}
}

// TestHandleRollbackEvent_LeaderReadvancesWhileSourceIsAhead: a rollback is not durable
// on its own — the next leader cycle rolls forward again while the source is ahead.
func TestHandleRollbackEvent_LeaderReadvancesWhileSourceIsAhead(t *testing.T) {
	f := newCatchUpFixture(t, catchUpOptions{leader: true})
	f.store.forceActive("articles", f.v2)
	f.start(t)

	waitFor(t, 2*time.Second, func() bool { return f.version() == f.v2 })

	f.store.forceActive("articles", f.v1)
	f.notif.subCh <- notify.Event{
		Action:     notify.ActionRollback,
		Collection: "articles",
		Version:    f.v1,
	}

	waitFor(t, 2*time.Second, func() bool { return f.version() == f.v1 })
	if got := f.version(); got != f.v1 {
		t.Fatalf("in-memory version = %q, want %q", got, f.v1)
	}

	// The source still reports v2, so the next leader cycle re-advances.
	f.mgr.SyncNow(context.Background())

	waitFor(t, 3*time.Second, func() bool { return f.version() == f.v2 })

	if got := f.version(); got != f.v2 {
		t.Errorf("in-memory version = %q, want %q — the leader must re-advance while the source is ahead", got, f.v2)
	}
	if got := f.activeVersion(t); got != f.v2 {
		t.Errorf("active version = %q, want %q — the leader re-activated nothing", got, f.v2)
	}
}
