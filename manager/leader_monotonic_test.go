package manager_test

import (
	"context"
	"testing"
	"time"

	"github.com/swchck/director/config"
	"github.com/swchck/director/directus"
	"github.com/swchck/director/manager"
	"github.com/swchck/director/notify"
)

// -- Fixture: a leader only moves a collection forward. max(date_updated) drops when
// the newest item is deleted, and announcing backwards splits the cluster.

// declineFixture is the sole replica of a service, so it always wins the
// advisory lock and every sync cycle runs the leader path.
type declineFixture struct {
	store    *mockStorage
	notif    *twoPCNotifier
	logger   *captureLogger
	metrics  *recordingMetrics
	mgr      *manager.Manager
	src      *validationSource
	articles *config.Collection[twoPCArticle]

	v1, v2 string
}

func newDeclineFixture(t *testing.T, strict bool, mgrOpts ...manager.ManagerOption) *declineFixture {
	t.Helper()

	t1 := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)

	store := newMockStorage()
	notif := newTwoPCNotifier()
	logger := &captureLogger{}
	metrics := &recordingMetrics{}
	articles := config.NewCollection[twoPCArticle]("articles")

	src := &validationSource{
		items:        []twoPCArticle{{ID: 1, Name: "one"}, {ID: 2, Name: "two"}},
		lastModified: t2,
	}

	opts := append([]manager.ManagerOption{
		manager.WithInstanceID("leader"),
		manager.WithLogger(logger),
		manager.WithMetrics(metrics),
	}, mgrOpts...)

	mgr := manager.New(store, notif, newTwoPCRegistry("leader"), manager.Options{
		PollInterval:             time.Hour,
		WSPollInterval:           time.Hour,
		HeartbeatInterval:        time.Hour,
		WSDebounce:               100 * time.Millisecond,
		WaitConfirmationsTimeout: 300 * time.Millisecond,
		PrepareTTL:               2 * time.Second,
		ServiceName:              "test-svc",
		RequireUnanimousApply:    strict,
	}, opts...)

	manager.RegisterCollectionSource(mgr, articles, src)

	return &declineFixture{
		store:    store,
		notif:    notif,
		logger:   logger,
		metrics:  metrics,
		mgr:      mgr,
		src:      src,
		articles: articles,
		v1:       config.NewVersion(t1).String(),
		v2:       config.NewVersion(t2).String(),
	}
}

// start runs the fixture's manager for the rest of the test.
func (f *declineFixture) start(t *testing.T) {
	t.Helper()
	startManager(t, f.mgr, f.logger)
}

// declineWarns counts the one-per-version warning the decline emits.
func (f *declineFixture) declineWarns() int {
	return f.logger.warnCount("declining to sync a source version behind the one held")
}

// declineDebugs counts the deduped repeats of the decline log line.
func (f *declineFixture) declineDebugs() int {
	return f.logger.debugCount("declining to sync a source version behind the one held (dedup)")
}

// published counts events of one action naming a version.
func (f *declineFixture) published(action, version string) int {
	n := 0
	for _, ev := range f.notif.publishedEvents() {
		if ev.Action == action && ev.Version == version {
			n++
		}
	}
	return n
}

func (f *declineFixture) lastSyncErr() string {
	for _, c := range f.mgr.Status().Configs {
		if c.Name == "articles" {
			return c.LastSyncErr
		}
	}
	return ""
}

// syncModes runs a scenario against both consistency modes: the guard belongs to both
// leader paths, and 2PC would otherwise abort rounds instead of never starting them.
var syncModes = []struct {
	name   string
	strict bool
}{
	{"eventually consistent", false},
	{"strict 2PC", true},
}

// TestLeaderSync_DeclinesSourceVersionBehindTheOneHeld: deleting the newest item lowers
// max(date_updated), so ordinary content editing reaches this, not just a fault.
func TestLeaderSync_DeclinesSourceVersionBehindTheOneHeld(t *testing.T) {
	for _, mode := range syncModes {
		t.Run(mode.name, func(t *testing.T) {
			f := newDeclineFixture(t, mode.strict)
			f.start(t)

			waitFor(t, 3*time.Second, func() bool { return f.articles.Count() == 2 })
			if got := f.articles.Version().String(); got != f.v2 {
				t.Fatalf("in-memory version = %q, want %q before the regression", got, f.v2)
			}

			// The newest item is deleted, so the reported maximum drops back.
			f.src.set([]twoPCArticle{{ID: 1, Name: "one"}}, time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC))

			// One cycle at a time: SyncNow coalesces, so a burst is not three cycles.
			// Each is awaited on its log line, which the decline emits after the metric.
			f.mgr.SyncNow(context.Background())
			waitFor(t, 3*time.Second, func() bool { return f.declineWarns() == 1 })

			for want := 1; want <= 2; want++ {
				f.mgr.SyncNow(context.Background())
				waitFor(t, 3*time.Second, func() bool { return f.declineDebugs() >= want })
			}

			if got := f.articles.Version().String(); got != f.v2 {
				t.Errorf("in-memory version = %q, want %q — the leader followed the source backwards", got, f.v2)
			}
			if got := f.articles.Count(); got != 2 {
				t.Errorf("articles.Count() = %d, want 2 — the older payload was applied", got)
			}
			if got := f.store.activeArticleVersions(); len(got) != 1 || got[0] != f.v2 {
				t.Errorf("active versions = %v, want exactly [%s]", got, f.v2)
			}
			if _, err := f.store.GetSnapshot(context.Background(), "articles", f.v1); err == nil {
				t.Error("a snapshot was persisted for the declined version")
			}
			for _, action := range []string{notify.ActionSync, notify.ActionPrepare, notify.ActionCommit} {
				if got := f.published(action, f.v1); got != 0 {
					t.Errorf("%s events naming the declined version = %d, want 0", action, got)
				}
			}

			// The metric fires per cycle; the warn is deduped per version.
			if got := f.metrics.sourceRegressedCount("articles"); got != 3 {
				t.Errorf("SourceVersionRegressed = %d over 3 declined cycles, want 3 — the alerting signal is deduped", got)
			}
			if got := f.declineWarns(); got != 1 {
				t.Errorf("decline warns = %d, want 1 — a source stuck on the older version repeats it every poll", got)
			}
			if got := f.logger.warnCount("local=" + f.v2 + " reported=" + f.v1); got != 1 {
				t.Errorf("decline warn naming local=%s reported=%s fired %d times, want 1", f.v2, f.v1, got)
			}

			// Declining is not a failure: nothing broke and nothing is retried.
			if got := len(f.metrics.snapshot().syncFailed); got != 0 {
				t.Errorf("SyncFailed = %d, want 0 — a declined cycle is not a failed sync", got)
			}
			if got := f.lastSyncErr(); got != "" {
				t.Errorf("LastSyncErr = %q, want empty", got)
			}
		})
	}
}

// TestLeaderSync_DeclineResetsAfterMovingForward: the dedup must not silence a second
// regression once the collection has moved on, or a recurrence goes unreported.
func TestLeaderSync_DeclineResetsAfterMovingForward(t *testing.T) {
	f := newDeclineFixture(t, false)
	f.start(t)

	waitFor(t, 3*time.Second, func() bool { return f.articles.Count() == 2 })

	t1 := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	f.src.set([]twoPCArticle{{ID: 1, Name: "one"}}, t1)
	f.mgr.SyncNow(context.Background())
	waitFor(t, 3*time.Second, func() bool { return f.declineWarns() == 1 })

	// The source is fixed and moves ahead again.
	t3 := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	f.src.set([]twoPCArticle{{ID: 1, Name: "one"}, {ID: 2, Name: "two"}, {ID: 3, Name: "three"}}, t3)
	f.mgr.SyncNow(context.Background())
	waitFor(t, 3*time.Second, func() bool { return f.articles.Count() == 3 })

	// Same regression again — a new incident, so it must warn again.
	f.src.set([]twoPCArticle{{ID: 1, Name: "one"}}, t1)
	f.mgr.SyncNow(context.Background())
	waitFor(t, 3*time.Second, func() bool { return f.declineWarns() >= 2 })

	if got := f.declineWarns(); got != 2 {
		t.Errorf("decline warns = %d, want 2 — the dedup did not reset after a successful apply", got)
	}
	if got := f.articles.Version().String(); got != config.NewVersion(t3).String() {
		t.Errorf("in-memory version = %q, want %q", got, config.NewVersion(t3).String())
	}
}

// TestLeaderSync_ZeroTimestampSourceStillSyncs: a source with no LastModified is
// supported — the first cycle syncs, later ones skip as unchanged, none declines.
func TestLeaderSync_ZeroTimestampSourceStillSyncs(t *testing.T) {
	zeroVersion := config.NewVersion(time.Time{}).String()

	for _, mode := range syncModes {
		t.Run(mode.name, func(t *testing.T) {
			f := newDeclineFixture(t, mode.strict)
			f.src.set([]twoPCArticle{{ID: 1, Name: "one"}}, time.Time{})
			f.start(t)

			waitFor(t, 3*time.Second, func() bool { return f.articles.Count() == 1 })

			if got := f.articles.Version().String(); got != zeroVersion {
				t.Errorf("in-memory version = %q, want %q — the first sync never happened", got, zeroVersion)
			}
			if got := f.store.activeArticleVersions(); len(got) != 1 || got[0] != zeroVersion {
				t.Errorf("active versions = %v, want exactly [%s]", got, zeroVersion)
			}
			if !f.mgr.Ready() {
				t.Error("manager is not Ready — a source without LastModified can never load")
			}

			// Further cycles are no-ops, not regressions.
			for range 3 {
				f.mgr.SyncNow(context.Background())
			}
			time.Sleep(200 * time.Millisecond)

			if got := f.metrics.sourceRegressedCount("articles"); got != 0 {
				t.Errorf("SourceVersionRegressed = %d, want 0 — an unchanged zero version is not a regression", got)
			}
			if got := f.declineWarns(); got != 0 {
				t.Errorf("decline warns = %d, want 0", got)
			}
			if got := f.src.listCalls.Load(); got != 1 {
				t.Errorf("source List calls = %d, want 1 — the version check stopped skipping", got)
			}
		})
	}
}

// TestLeaderSync_DeclineWithoutSourceMetrics: SourceMetrics is optional, so a Metrics
// written before it existed must decline without panicking and still get the log line.
func TestLeaderSync_DeclineWithoutSourceMetrics(t *testing.T) {
	core := &coreOnlyMetrics{}
	if _, ok := manager.Metrics(core).(manager.SourceMetrics); ok {
		t.Fatal("coreOnlyMetrics implements SourceMetrics; the fallback path is no longer covered")
	}

	f := newDeclineFixture(t, false, manager.WithMetrics(core))
	f.start(t)

	waitFor(t, 3*time.Second, func() bool { return f.articles.Count() == 2 })

	f.src.set([]twoPCArticle{{ID: 1, Name: "one"}}, time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC))
	f.mgr.SyncNow(context.Background())

	waitFor(t, 3*time.Second, func() bool { return f.declineWarns() == 1 })

	if got := f.declineWarns(); got != 1 {
		t.Errorf("decline warns = %d, want 1", got)
	}
	if got := f.articles.Version().String(); got != f.v2 {
		t.Errorf("in-memory version = %q, want %q", got, f.v2)
	}
}

// TestSyncOneForced_MintsForwardVersionOverABackwardSource: a WS event says something
// changed, not what, so a forced sync must propagate under a version it can move onto.
func TestSyncOneForced_MintsForwardVersionOverABackwardSource(t *testing.T) {
	sendEvent := make(chan struct{})

	wsSrv := newWSBehaviorServer(t, func(uid string, write func(map[string]any)) {
		<-sendEvent
		write(map[string]any{
			"type":  "subscription",
			"uid":   uid,
			"event": "delete",
			"data":  []map[string]any{{"id": 2}},
			"keys":  []string{"2"},
		})
	})
	defer wsSrv.srv.Close()

	wsClient := directus.NewWSClient(wsSrv.srv.URL, "token")
	defer wsClient.Close()

	f := newDeclineFixture(t, false, manager.WithWebSocket(wsClient))
	f.start(t)

	waitFor(t, 3*time.Second, func() bool { return f.articles.Count() == 2 })

	select {
	case <-wsSrv.uidCh:
	case <-time.After(3 * time.Second):
		t.Fatal("WS subscription was never received by the test server")
	}

	// The newest item is deleted: fewer items, and a lower reported timestamp.
	f.src.set([]twoPCArticle{{ID: 1, Name: "one"}}, time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC))
	close(sendEvent)

	waitFor(t, 5*time.Second, func() bool { return f.articles.Count() == 1 })

	if got := f.articles.Count(); got != 1 {
		t.Fatalf("articles.Count() = %d, want 1 — the deletion never propagated", got)
	}

	minted := f.articles.Version()
	if minted.String() == f.v1 {
		t.Fatal("the forced sync announced the source's own older version")
	}
	if !minted.After(config.NewVersion(time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC))) {
		t.Errorf("minted version = %q, want a version after %q", minted.String(), f.v2)
	}
	if got := f.store.activeArticleVersions(); len(got) != 1 || got[0] != minted.String() {
		t.Errorf("active versions = %v, want exactly [%s]", got, minted.String())
	}
	if got := f.published(notify.ActionSync, minted.String()); got != 1 {
		t.Errorf("sync events naming the minted version = %d, want 1 — followers never hear about the deletion", got)
	}
	if got := f.metrics.sourceRegressedCount("articles"); got != 0 {
		t.Errorf("SourceVersionRegressed = %d, want 0 — a forced cycle mints forward instead of declining", got)
	}
}
