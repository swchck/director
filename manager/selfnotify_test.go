package manager_test

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/swchck/director/config"
	"github.com/swchck/director/manager"
	"github.com/swchck/director/notify"
	"github.com/swchck/director/storage"
)

// -- Fixture ---------------------------------------------------------------

// selfNotifyFixture reproduces the topology that surfaced the self-delivery bug: leader
// plus two followers, strict consistency, webhook-driven sync, older snapshot active.
type selfNotifyFixture struct {
	store    *mockStorage
	notif    *twoPCNotifier
	logger   *captureLogger
	registry *twoPCRegistry
	mgr      *manager.Manager
	articles *config.Collection[twoPCArticle]

	oldVersion string
	newVersion string
}

func newSelfNotifyFixture(t *testing.T) *selfNotifyFixture {
	t.Helper()

	oldTime := time.Date(2026, 8, 4, 14, 40, 51, 393_000_000, time.UTC)
	newTime := time.Date(2026, 8, 11, 16, 17, 41, 879_000_000, time.UTC)

	oldVersion := config.NewVersion(oldTime).String()
	newVersion := config.NewVersion(newTime).String()

	store := newMockStorage()

	// Storage already holds an active snapshot with the OLD payload — the post-deploy
	// pod state, which keeps Start out of the manual-mode bootstrap branch.
	oldContent, err := json.Marshal([]twoPCArticle{{ID: 1, Name: "old"}})
	if err != nil {
		t.Fatalf("marshal old content: %v", err)
	}

	ctx := context.Background()
	if err := store.SaveSnapshot(ctx, "articles", oldVersion, oldContent); err != nil {
		t.Fatalf("seed old snapshot: %v", err)
	}
	if err := store.ActivateSnapshot(ctx, "articles", oldVersion); err != nil {
		t.Fatalf("activate old snapshot: %v", err)
	}

	registry := newTwoPCRegistry("leader", "follower-1", "follower-2")

	src := &twoPCSource{
		items:        []twoPCArticle{{ID: 1, Name: "new"}, {ID: 2, Name: "new-2"}},
		lastModified: newTime,
	}

	// Remote followers prepare successfully and synchronously, so the leader's
	// immediate prepare check passes and the round is not gated on a 500ms tick.
	notif := newTwoPCNotifier()
	notif.onPublish = func(ctx context.Context, ev notify.Event) {
		if ev.Action != notify.ActionPrepare {
			return
		}
		for _, id := range []string{"follower-1", "follower-2"} {
			if err := store.LogApply(ctx, id, ev.Collection, ev.Version, "prepared"); err != nil {
				t.Errorf("follower %s prepare: %v", id, err)
			}
		}
	}

	logger := &captureLogger{}
	articles := config.NewCollection[twoPCArticle]("articles")

	mgr := manager.New(store, notif, registry, manager.Options{
		PollInterval:             time.Hour,
		HeartbeatInterval:        150 * time.Millisecond,
		WaitConfirmationsTimeout: 2 * time.Second,
		PrepareTTL:               10 * time.Second,
		ServiceName:              "test-svc",
		RequireUnanimousApply:    true,
		ManualSyncOnly:           true,
	},
		manager.WithInstanceID("leader"),
		manager.WithLogger(logger),
	)

	manager.RegisterCollectionSource(mgr, articles, src)

	return &selfNotifyFixture{
		store:      store,
		notif:      notif,
		logger:     logger,
		registry:   registry,
		mgr:        mgr,
		articles:   articles,
		oldVersion: oldVersion,
		newVersion: newVersion,
	}
}

// waitSelfEventsHandled blocks until the loop has processed the leader's own commit.
// Events arrive in publish order, so the self-delivered prepare is handled too.
func (f *selfNotifyFixture) waitSelfEventsHandled(t *testing.T) {
	t.Helper()

	const dropped = "dropping self-published event action=" + notify.ActionCommit

	waitFor(t, 2*time.Second, func() bool { return f.logger.debugCount(dropped) > 0 })

	if f.logger.debugCount(dropped) == 0 {
		t.Fatal("leader never processed its own commit event within 2s")
	}
}

func (f *selfNotifyFixture) activeVersion(t *testing.T) string {
	t.Helper()

	snap, err := f.store.GetActiveSnapshot(context.Background(), "articles")
	if err != nil {
		t.Fatalf("get active snapshot: %v", err)
	}

	return snap.Version
}

// -- Event origin filtering ------------------------------------------------

// eventFilterFixture is a manager that can never win the advisory lock, so the
// only thing it ever reacts to is a notify event.
type eventFilterFixture struct {
	store    *mockStorage
	notif    *mockNotifier
	logger   *captureLogger
	metrics  *recordingMetrics
	mgr      *manager.Manager
	articles *config.Collection[twoPCArticle]
	version  string
}

func newEventFilterFixture(t *testing.T) *eventFilterFixture {
	t.Helper()

	store := newMockStorage()
	store.lockHeld = true // another instance is leader for the whole test

	version := config.NewVersion(time.Date(2026, 8, 11, 16, 17, 41, 0, time.UTC)).String()
	content, err := json.Marshal([]twoPCArticle{{ID: 1, Name: "one"}, {ID: 2, Name: "two"}})
	if err != nil {
		t.Fatalf("marshal content: %v", err)
	}
	if err := store.SaveSnapshot(context.Background(), "articles", version, content); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}

	notif := newMockNotifier()
	logger := &captureLogger{}
	metrics := &recordingMetrics{}
	articles := config.NewCollection[twoPCArticle]("articles")

	mgr := manager.New(store, notif, newTwoPCRegistry("inst-1"), manager.Options{
		PollInterval:             time.Hour,
		HeartbeatInterval:        time.Hour,
		WaitConfirmationsTimeout: time.Second,
		PrepareTTL:               10 * time.Second,
		ServiceName:              "test-svc",
		RequireUnanimousApply:    true,
	},
		manager.WithInstanceID("inst-1"),
		manager.WithLogger(logger),
		manager.WithMetrics(metrics),
	)

	manager.RegisterCollectionSource(mgr, articles, &twoPCSource{})

	return &eventFilterFixture{
		store:    store,
		notif:    notif,
		logger:   logger,
		metrics:  metrics,
		mgr:      mgr,
		articles: articles,
		version:  version,
	}
}

// start runs the manager until the test ends.
func (f *eventFilterFixture) start(t *testing.T) {
	t.Helper()

	startManager(t, f.mgr, f.logger)
}

func (f *eventFilterFixture) loggedStatus(t *testing.T, status string) bool {
	t.Helper()

	ids, err := f.store.AppliedInstances(context.Background(), "articles", f.version, status)
	if err != nil {
		t.Fatalf("applied instances: %v", err)
	}

	return slices.Contains(ids, "inst-1")
}

// eventFilterCase pins one action together with the observable side effect that
// proves the follower path ran.
type eventFilterCase struct {
	action string

	// prepare runs after the manager started, before the event is delivered.
	prepare func(t *testing.T, f *eventFilterFixture)

	// handled reports whether the follower path ran for this action.
	handled func(t *testing.T, f *eventFilterFixture) bool
}

func eventFilterCases() []eventFilterCase {
	activate := func(t *testing.T, f *eventFilterFixture) {
		t.Helper()
		if err := f.store.ActivateSnapshot(context.Background(), "articles", f.version); err != nil {
			t.Fatalf("activate snapshot: %v", err)
		}
	}

	return []eventFilterCase{
		{
			action:  notify.ActionSync,
			handled: func(t *testing.T, f *eventFilterFixture) bool { return f.loggedStatus(t, "applied") },
		},
		{
			action:  notify.ActionRollback,
			prepare: activate,
			handled: func(_ *testing.T, f *eventFilterFixture) bool { return f.articles.Count() == 2 },
		},
		{
			action:  notify.ActionPrepare,
			handled: func(t *testing.T, f *eventFilterFixture) bool { return f.loggedStatus(t, "prepared") },
		},
		{
			action:  notify.ActionCommit,
			handled: func(t *testing.T, f *eventFilterFixture) bool { return f.loggedStatus(t, "committed") },
		},
		{
			action: notify.ActionAbort,
			handled: func(_ *testing.T, f *eventFilterFixture) bool {
				return f.metrics.stagedDroppedCount("articles:abort") == 1
			},
		},
	}
}

// waitDropped blocks until the event loop reported dropping a self-published
// event for this action.
func (f *eventFilterFixture) waitDropped(t *testing.T, action string) {
	t.Helper()

	dropped := "dropping self-published event action=" + action
	waitFor(t, 2*time.Second, func() bool { return f.logger.debugCount(dropped) > 0 })

	if f.logger.debugCount(dropped) == 0 {
		t.Fatalf("event loop never reported dropping the self-published %q event", action)
	}
}

// TestHandleEvent_OriginFiltering pins which events run the follower path, per action:
// never our own (transports echo them), always another replica's, always unstamped.
func TestHandleEvent_OriginFiltering(t *testing.T) {
	origins := []struct {
		name        string
		instanceID  string
		wantHandled bool
	}{
		{"own event dropped", "inst-1", false},
		{"event from another instance processed", "inst-2", true},
		{"unstamped event processed", "", true},
	}

	for _, origin := range origins {
		t.Run(origin.name, func(t *testing.T) {
			for _, tc := range eventFilterCases() {
				t.Run(tc.action, func(t *testing.T) {
					f := newEventFilterFixture(t)
					f.start(t)

					if tc.prepare != nil {
						tc.prepare(t, f)
					}

					f.notif.subCh <- notify.Event{
						Action:     tc.action,
						Collection: "articles",
						Version:    f.version,
						RoundID:    "round-1",
						InstanceID: origin.instanceID,
					}

					if !origin.wantHandled {
						f.waitDropped(t, tc.action)
						if tc.handled(t, f) {
							t.Errorf("follower path ran for a dropped %q event", tc.action)
						}
						return
					}

					waitFor(t, 2*time.Second, func() bool { return tc.handled(t, f) })
					if !tc.handled(t, f) {
						t.Errorf("%q event was not handled", tc.action)
					}
				})
			}
		})
	}
}

// -- Self-delivered events on the leader -----------------------------------

// TestTwoPC_SelfDeliveredCommitBreaksRound pins the production failure: the leader's own
// commit consumed the staged entry before leaderSync2PC reached it, failing commitStaged.
func TestTwoPC_SelfDeliveredCommitBreaksRound(t *testing.T) {
	f := newSelfNotifyFixture(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- f.mgr.Start(ctx) }()

	waitFor(t, 3*time.Second, f.mgr.Ready)
	if !f.mgr.Ready() {
		t.Fatal("manager never became ready")
	}

	// The webhook path: SyncNow asks the run loop for a cycle, and the loop then
	// consumes the notifications that cycle published.
	f.mgr.SyncNow(ctx)
	f.waitSelfEventsHandled(t)

	// In-memory state is correct — the data did reach the replica.
	if got := f.articles.Version().String(); got != f.newVersion {
		t.Errorf("in-memory version = %q, want %q", got, f.newVersion)
	}

	// Every replica applied, so the round must not be reported as failed.
	var lastErr string
	for _, c := range f.mgr.Status().Configs {
		if c.Name == "articles" {
			lastErr = c.LastSyncErr
		}
	}
	if lastErr != "" {
		t.Errorf("leader sync reported an error for a committed round: %s", lastErr)
	}

	// The decision must be durable: storage points at the new version.
	if got := f.activeVersion(t); got != f.newVersion {
		t.Errorf("active snapshot version = %q, want %q — snapshot was never activated", got, f.newVersion)
	}

	// Belt and braces: the snapshot row itself carries the active status.
	snap, err := f.store.GetSnapshot(context.Background(), "articles", f.newVersion)
	if err != nil {
		t.Fatalf("get new snapshot: %v", err)
	}
	if snap.Status != storage.StatusActive {
		t.Errorf("new snapshot status = %q, want %q", snap.Status, storage.StatusActive)
	}

	cancel()
	<-errCh
}

// TestTwoPC_SelfDeliveredCommitCausesClusterRollback pins the second half: catch-up
// applies the active snapshot either way, so an un-activated round drags replicas back.
func TestTwoPC_SelfDeliveredCommitCausesClusterRollback(t *testing.T) {
	f := newSelfNotifyFixture(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- f.mgr.Start(ctx) }()

	waitFor(t, 3*time.Second, f.mgr.Ready)

	f.mgr.SyncNow(ctx)
	f.waitSelfEventsHandled(t)

	if got := f.articles.Version().String(); got != f.newVersion {
		t.Fatalf("in-memory version = %q, want %q", got, f.newVersion)
	}

	// Manual mode runs catch-up every tick regardless of leadership — the path that
	// dragged the cluster back. The lone-instance alive set just avoids waiting on peers.
	f.registry.setInstances("leader")

	// Now wait: does the replica stay on the new version, or roll back?
	rolledBack := false
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if f.articles.Version().String() == f.oldVersion {
			rolledBack = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if rolledBack {
		t.Errorf("followerCatchUp rolled the replica back from %q to %q", f.newVersion, f.oldVersion)
	}

	cancel()
	<-errCh
}

// TestTwoPC_SelfDeliveredPrepareDuplicatesWork documents the cost of the same root
// cause: the leader ran the follower staging path against its own prepare event.
func TestTwoPC_SelfDeliveredPrepareDuplicatesWork(t *testing.T) {
	f := newSelfNotifyFixture(t)

	var mu sync.Mutex
	redundantReads := 0

	f.store.onGetSnapshot = func(collection, version string) {
		if collection == "articles" && version == f.newVersion {
			mu.Lock()
			redundantReads++
			mu.Unlock()
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- f.mgr.Start(ctx) }()

	waitFor(t, 3*time.Second, f.mgr.Ready)

	f.mgr.SyncNow(ctx)
	f.waitSelfEventsHandled(t)

	mu.Lock()
	got := redundantReads
	mu.Unlock()

	if got > 0 {
		t.Errorf("leader re-read its own snapshot from storage %d time(s) while staging its own prepare event", got)
	}

	cancel()
	<-errCh
}

// TestTwoPC_LeaderLogsPreparedAndCommittedOnce: the leader used to run the follower path
// against its own broadcast, writing a second prepared and committed row per round.
func TestTwoPC_LeaderLogsPreparedAndCommittedOnce(t *testing.T) {
	f := newSelfNotifyFixture(t)

	var mu sync.Mutex
	writes := make(map[string]int)
	f.store.onLogApply = func(instanceID, _, version, status string) error {
		if instanceID != "leader" || version != f.newVersion {
			return nil
		}
		mu.Lock()
		writes[status]++
		mu.Unlock()
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- f.mgr.Start(ctx) }()

	waitFor(t, 3*time.Second, f.mgr.Ready)

	f.mgr.SyncNow(ctx)
	f.waitSelfEventsHandled(t)

	mu.Lock()
	prepared, committed := writes["prepared"], writes["committed"]
	mu.Unlock()

	if prepared != 1 {
		t.Errorf("leader wrote %d prepared rows for one round, want 1", prepared)
	}
	if committed != 1 {
		t.Errorf("leader wrote %d committed rows for one round, want 1", committed)
	}

	cancel()
	<-errCh
}

// -- Publisher stamp -------------------------------------------------------

// assertStamped checks that every published event carries the publisher's
// instance ID and that the round produced the expected publish sites.
func assertStamped(t *testing.T, events []notify.Event, instanceID string, wantActions ...string) {
	t.Helper()

	seen := make(map[string]bool, len(events))
	for _, ev := range events {
		if ev.InstanceID != instanceID {
			t.Errorf("%q event published with InstanceID %q, want %q", ev.Action, ev.InstanceID, instanceID)
		}
		seen[ev.Action] = true
	}

	for _, action := range wantActions {
		if !seen[action] {
			t.Errorf("no %q event published — publish site not covered by this case", action)
		}
	}
}

// TestManager_StampsInstanceIDOnPublishedEvents covers every publish site: prepare,
// commit, the fallback sync, abort of a failed round, and eventually-consistent sync.
func TestManager_StampsInstanceIDOnPublishedEvents(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	t.Run("2pc committed round", func(t *testing.T) {
		store := newMockStorage()
		notif := newTwoPCNotifier()
		notif.onPublish = func(ctx context.Context, ev notify.Event) {
			if ev.Action == notify.ActionPrepare {
				_ = store.LogApply(ctx, "follower-1", ev.Collection, ev.Version, "prepared")
			}
		}

		mgr, articles := build2PCManager(t, store, notif, newTwoPCRegistry("leader", "follower-1"),
			&twoPCSource{items: []twoPCArticle{{ID: 1, Name: "Alpha"}}, lastModified: now})

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		errCh := make(chan error, 1)
		go func() { errCh <- mgr.Start(ctx) }()

		waitFor(t, 3*time.Second, func() bool { return articles.Count() == 1 })
		waitFor(t, 3*time.Second, func() bool {
			for _, ev := range notif.publishedEvents() {
				if ev.Action == notify.ActionSync {
					return true
				}
			}
			return false
		})

		assertStamped(t, notif.publishedEvents(), "leader",
			notify.ActionPrepare, notify.ActionCommit, notify.ActionSync)

		cancel()
		<-errCh
	})

	t.Run("2pc aborted round", func(t *testing.T) {
		store := newMockStorage()
		notif := newTwoPCNotifier()
		notif.onPublish = func(ctx context.Context, ev notify.Event) {
			if ev.Action == notify.ActionPrepare {
				_ = store.LogApply(ctx, "follower-1", ev.Collection, ev.Version, "prepare_failed")
			}
		}

		mgr, _ := build2PCManager(t, store, notif, newTwoPCRegistry("leader", "follower-1"),
			&twoPCSource{items: []twoPCArticle{{ID: 1, Name: "Alpha"}}, lastModified: now})

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		errCh := make(chan error, 1)
		go func() { errCh <- mgr.Start(ctx) }()

		waitFor(t, 3*time.Second, func() bool {
			for _, ev := range notif.publishedEvents() {
				if ev.Action == notify.ActionAbort {
					return true
				}
			}
			return false
		})

		assertStamped(t, notif.publishedEvents(), "leader", notify.ActionPrepare, notify.ActionAbort)

		cancel()
		<-errCh
	})

	t.Run("eventually consistent sync", func(t *testing.T) {
		store := newMockStorage()
		notif := newMockNotifier()
		articles := config.NewCollection[twoPCArticle]("articles")

		mgr := manager.New(store, notif, newMockRegistry(), manager.Options{
			PollInterval:             time.Hour,
			WaitConfirmationsTimeout: 500 * time.Millisecond,
			ServiceName:              "test-svc",
		},
			manager.WithInstanceID("leader"),
		)
		manager.RegisterCollectionSource(mgr, articles,
			&twoPCSource{items: []twoPCArticle{{ID: 1, Name: "Alpha"}}, lastModified: now})

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		errCh := make(chan error, 1)
		go func() { errCh <- mgr.Start(ctx) }()

		waitFor(t, 3*time.Second, func() bool { return len(notif.publishedEvents()) > 0 })

		assertStamped(t, notif.publishedEvents(), "leader", notify.ActionSync)

		cancel()
		<-errCh
	})
}
