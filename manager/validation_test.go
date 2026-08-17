package manager_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/swchck/director/config"
	dlog "github.com/swchck/director/log"
	"github.com/swchck/director/manager"
	"github.com/swchck/director/notify"
)

// captureLogger counts log calls per formatted message — enough to assert dedup and to
// observe that the event loop reached a specific decision.
type captureLogger struct {
	mu     sync.Mutex
	warns  []string // formatted message + collection/version hint
	infos  []string
	debugs []string
}

func (l *captureLogger) Debug(msg string, fields ...dlog.Field) {
	line := formatLogLine(msg, fields)
	l.mu.Lock()
	l.debugs = append(l.debugs, line)
	l.mu.Unlock()
}
func (l *captureLogger) Info(msg string, _ ...dlog.Field) {
	l.mu.Lock()
	l.infos = append(l.infos, msg)
	l.mu.Unlock()
}
func (l *captureLogger) Warn(msg string, fields ...dlog.Field) {
	line := formatLogLine(msg, fields)
	l.mu.Lock()
	l.warns = append(l.warns, line)
	l.mu.Unlock()
}
func (l *captureLogger) Error(string, ...dlog.Field) {}

func formatLogLine(msg string, fields []dlog.Field) string {
	var b strings.Builder
	b.WriteString(msg)
	for _, f := range fields {
		b.WriteString(" ")
		b.WriteString(f.Key)
		b.WriteString("=")
		if s, ok := f.Value.(string); ok {
			b.WriteString(s)
		} else {
			fmt.Fprintf(&b, "%v", f.Value)
		}
	}
	return b.String()
}

func (l *captureLogger) warnCount(substr string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return countMatches(l.warns, substr)
}

// startedCount reports how many times the manager logged that it finished its
// startup sequence — the signal that the run loop is up.
func (l *captureLogger) startedCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return countMatches(l.infos, "manager: started")
}

func (l *captureLogger) debugCount(substr string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return countMatches(l.debugs, substr)
}

func countMatches(lines []string, substr string) int {
	n := 0
	for _, line := range lines {
		if substr == "" || strings.Contains(line, substr) {
			n++
		}
	}
	return n
}

// validationSource is a controllable collection source that lets the test
// drive items + lastModified so the manager observes "new versions".
type validationSource struct {
	mu           sync.Mutex
	items        []twoPCArticle
	lastModified time.Time
	listCalls    atomic.Int32
}

func (s *validationSource) List(_ context.Context) ([]twoPCArticle, error) {
	s.listCalls.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]twoPCArticle, len(s.items))
	copy(out, s.items)
	return out, nil
}

func (s *validationSource) LastModified(_ context.Context) (time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastModified, nil
}

func (s *validationSource) set(items []twoPCArticle, ts time.Time) {
	s.mu.Lock()
	s.items = items
	s.lastModified = ts
	s.mu.Unlock()
}

// TestValidator_RejectsAndStaysOnPreviousVersion: nothing swaps, and since the first
// version to arrive is invalid the collection stays empty (no last-known-good).
func TestValidator_RejectsAndStaysOnPreviousVersion(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	store := newMockStorage()
	notif := newMockNotifier()
	reg := newMockRegistry()
	src := &validationSource{
		items:        []twoPCArticle{{ID: 1, Name: "bad"}},
		lastModified: now,
	}

	logger := &captureLogger{}
	products := config.NewCollection[twoPCArticle]("products")

	mgr := manager.New(store, notif, reg, manager.Options{
		PollInterval:             time.Hour,
		WaitConfirmationsTimeout: 200 * time.Millisecond,
		ServiceName:              "svc",
	},
		manager.WithInstanceID("inst-1"),
		manager.WithLogger(logger),
	)

	manager.RegisterCollectionSource(mgr, products, src,
		manager.WithCollectionValidator(func(items []twoPCArticle) error {
			if items[0].Name == "bad" {
				return errors.New("name=bad rejected")
			}
			return nil
		}),
	)

	errCh := make(chan error, 1)
	go func() { errCh <- mgr.Start(ctx) }()

	waitFor(t, 2*time.Second, func() bool {
		return logger.warnCount("config update rejected") >= 1
	})

	if products.Count() != 0 {
		t.Errorf("collection swapped despite validator rejection: Count=%d", products.Count())
	}
	if !products.Version().IsZero() {
		t.Errorf("version advanced to %q despite rejection", products.Version())
	}

	// No snapshot should have been activated (leader didn't proceed past validator).
	if _, err := store.GetActiveSnapshot(ctx, "products"); err == nil {
		t.Error("active snapshot exists despite validator rejection")
	}

	cancel()
	<-errCh
}

// TestValidator_DedupesLogPerVersion: trigger several syncs of the same bad
// version and verify only ONE "config update rejected" warn is emitted.
func TestValidator_DedupesLogPerVersion(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	store := newMockStorage()
	notif := newMockNotifier()
	reg := newMockRegistry()
	src := &validationSource{
		items:        []twoPCArticle{{ID: 1, Name: "bad"}},
		lastModified: now,
	}

	logger := &captureLogger{}
	products := config.NewCollection[twoPCArticle]("products")

	mgr := manager.New(store, notif, reg, manager.Options{
		PollInterval:             time.Hour,
		WaitConfirmationsTimeout: 200 * time.Millisecond,
		ServiceName:              "svc",
	},
		manager.WithInstanceID("inst-1"),
		manager.WithLogger(logger),
	)

	manager.RegisterCollectionSource(mgr, products, src,
		manager.WithCollectionValidator(func(items []twoPCArticle) error {
			if items[0].Name == "bad" {
				return errors.New("rejected")
			}
			return nil
		}),
	)

	errCh := make(chan error, 1)
	go func() { errCh <- mgr.Start(ctx) }()

	waitFor(t, 2*time.Second, func() bool {
		return logger.warnCount("config update rejected") >= 1
	})

	// Drive several extra sync attempts at the SAME version.
	for range 5 {
		mgr.SyncNow(ctx)
	}
	time.Sleep(200 * time.Millisecond)

	if got := logger.warnCount("config update rejected"); got != 1 {
		t.Errorf("validation warn fired %d times for the same version, want 1", got)
	}
	if logger.warnCount("leader sync failed") != 0 {
		t.Error("generic leader-sync error log fired despite validation-failure dedup")
	}

	cancel()
	<-errCh
}

// TestValidator_ClearsAfterSuccess: bad → good → bad-again should produce
// two separate warn logs (the dedup state resets on the successful apply).
func TestValidator_ClearsAfterSuccess(t *testing.T) {
	t0 := time.Now().UTC().Truncate(time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	store := newMockStorage()
	notif := newMockNotifier()
	reg := newMockRegistry()
	src := &validationSource{
		items:        []twoPCArticle{{ID: 1, Name: "bad"}},
		lastModified: t0,
	}

	logger := &captureLogger{}
	products := config.NewCollection[twoPCArticle]("products")

	mgr := manager.New(store, notif, reg, manager.Options{
		PollInterval:             time.Hour,
		WaitConfirmationsTimeout: 200 * time.Millisecond,
		ServiceName:              "svc",
	},
		manager.WithInstanceID("inst-1"),
		manager.WithLogger(logger),
	)

	manager.RegisterCollectionSource(mgr, products, src,
		manager.WithCollectionValidator(func(items []twoPCArticle) error {
			if items[0].Name == "bad" {
				return errors.New("rejected")
			}
			return nil
		}),
	)

	errCh := make(chan error, 1)
	go func() { errCh <- mgr.Start(ctx) }()

	waitFor(t, 2*time.Second, func() bool {
		return logger.warnCount("config update rejected") == 1
	})

	// Fix: new version with valid data.
	src.set([]twoPCArticle{{ID: 1, Name: "good"}}, t0.Add(time.Minute))
	mgr.SyncNow(ctx)

	waitFor(t, 2*time.Second, func() bool {
		return products.Count() == 1
	})
	if products.Count() != 1 {
		t.Fatalf("good version did not apply: Count=%d", products.Count())
	}

	// Break again with a NEW version (different timestamp).
	src.set([]twoPCArticle{{ID: 2, Name: "bad"}}, t0.Add(2*time.Minute))
	mgr.SyncNow(ctx)

	waitFor(t, 2*time.Second, func() bool {
		return logger.warnCount("config update rejected") == 2
	})

	if got := logger.warnCount("config update rejected"); got != 2 {
		t.Errorf("after success+new-bad: warn count=%d, want 2", got)
	}

	cancel()
	<-errCh
}

// TestValidator_2PCLeaderRejects: the leader's own validator fails on a fresh fetch —
// no snapshot, no prepare, no version advance; the round is skipped after one warn.
func TestValidator_2PCLeaderRejects(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	store := newMockStorage()
	twoPCReg := newTwoPCRegistry("leader") // leader-only, no followers
	notif := newTwoPCNotifier()
	src := &twoPCSource{
		items:        []twoPCArticle{{ID: 1, Name: "bad"}},
		lastModified: now,
	}

	logger := &captureLogger{}
	articles := config.NewCollection[twoPCArticle]("articles")

	mgr := manager.New(store, notif, twoPCReg, manager.Options{
		PollInterval:             time.Hour,
		WaitConfirmationsTimeout: 500 * time.Millisecond,
		PrepareTTL:               time.Second,
		ServiceName:              "svc",
		RequireUnanimousApply:    true,
	},
		manager.WithInstanceID("leader"),
		manager.WithLogger(logger),
	)

	manager.RegisterCollectionSource(mgr, articles, src,
		manager.WithCollectionValidator(func(items []twoPCArticle) error {
			if items[0].Name == "bad" {
				return errors.New("rejected")
			}
			return nil
		}),
	)

	errCh := make(chan error, 1)
	go func() { errCh <- mgr.Start(ctx) }()

	waitFor(t, 2*time.Second, func() bool {
		return logger.warnCount("config update rejected") >= 1
	})

	if articles.Count() != 0 {
		t.Errorf("leader committed despite validator rejection: Count=%d", articles.Count())
	}

	// Verify no prepare/commit event was published (leader rejected before publish).
	for _, ev := range notif.publishedEvents() {
		if ev.Action == notify.ActionPrepare || ev.Action == notify.ActionCommit {
			t.Errorf("unexpected published event %q after validator rejection", ev.Action)
		}
	}

	if _, err := store.GetSnapshot(ctx, "articles", config.NewVersion(now).String()); err == nil {
		t.Error("snapshot saved despite leader-side validator rejection")
	}

	cancel()
	<-errCh
}

// TestValidator_2PCLeaderAbortDeduped: a follower's prepare_failed aborts the round,
// and further syncs on the same version must produce only ONE abort warn.
func TestValidator_2PCLeaderAbortDeduped(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	store := newMockStorage()
	twoPCReg := newTwoPCRegistry("leader", "f1")
	notif := newTwoPCNotifier()
	notif.onPublish = func(ctx context.Context, ev notify.Event) {
		if ev.Action != notify.ActionPrepare {
			return
		}
		// Simulate follower validator rejecting.
		go func() {
			_ = store.LogApply(ctx, "f1", ev.Collection, ev.Version, "prepare_failed")
		}()
	}

	src := &twoPCSource{
		items:        []twoPCArticle{{ID: 1, Name: "Alpha"}},
		lastModified: now,
	}

	// An active older snapshot makes this a running replica: dedup is per version,
	// and a replica that has never loaded keeps reporting instead.
	seeded := config.NewVersion(now.Add(-time.Hour)).String()
	if err := store.SaveSnapshot(ctx, "articles", seeded, []byte(`[{"id":9,"name":"Seeded"}]`)); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	store.forceActive("articles", seeded)

	logger := &captureLogger{}
	articles := config.NewCollection[twoPCArticle]("articles")

	mgr := manager.New(store, notif, twoPCReg, manager.Options{
		PollInterval:             time.Hour,
		WaitConfirmationsTimeout: 500 * time.Millisecond,
		PrepareTTL:               time.Second,
		ServiceName:              "svc",
		RequireUnanimousApply:    true,
	},
		manager.WithInstanceID("leader"),
		manager.WithLogger(logger),
	)

	manager.RegisterCollectionSource(mgr, articles, src)

	errCh := make(chan error, 1)
	go func() { errCh <- mgr.Start(ctx) }()

	abortsPublished := func() int {
		n := 0
		for _, ev := range notif.publishedEvents() {
			if ev.Action == notify.ActionAbort {
				n++
			}
		}
		return n
	}

	// Drive further rounds on the same version off an observable, not a sleep:
	// SyncNow coalesces into the cycle already running.
	waitFor(t, 4*time.Second, func() bool {
		mgr.SyncNow(ctx)
		return abortsPublished() >= 3
	})

	if got := abortsPublished(); got < 3 {
		t.Fatalf("aborted rounds = %d, want at least 3 on the same version", got)
	}
	if got := logger.warnCount("2PC aborting round"); got != 1 {
		t.Errorf("2PC abort warn fired %d times for the same version, want 1", got)
	}

	cancel()
	<-errCh
}

// TestValidator_DiscardsInvalidSnapshotOnStartup: an active snapshot a tightened
// validator now rejects must not load; the config stays empty until a good sync.
func TestValidator_DiscardsInvalidSnapshotOnStartup(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	store := newMockStorage()
	notif := newMockNotifier()
	reg := newMockRegistry()

	// Pre-seed an invalid active snapshot in storage.
	preVersion := config.NewVersion(now.Add(-time.Hour)).String()
	bad, _ := json.Marshal([]twoPCArticle{{ID: 1, Name: "stale"}})
	if err := store.SaveSnapshot(ctx, "products", preVersion, bad); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	if err := store.ActivateSnapshot(ctx, "products", preVersion); err != nil {
		t.Fatalf("ActivateSnapshot: %v", err)
	}

	src := &validationSource{
		items:        []twoPCArticle{{ID: 1, Name: "stale"}},
		lastModified: now,
	}

	logger := &captureLogger{}
	products := config.NewCollection[twoPCArticle]("products")

	mgr := manager.New(store, notif, reg, manager.Options{
		PollInterval:             time.Hour,
		WaitConfirmationsTimeout: 200 * time.Millisecond,
		ServiceName:              "svc",
	},
		manager.WithInstanceID("inst-1"),
		manager.WithLogger(logger),
	)

	manager.RegisterCollectionSource(mgr, products, src,
		manager.WithCollectionValidator(func(items []twoPCArticle) error {
			for _, it := range items {
				if it.Name == "stale" {
					return errors.New("stale not allowed")
				}
			}
			return nil
		}),
	)

	errCh := make(chan error, 1)
	go func() { errCh <- mgr.Start(ctx) }()

	// Initial sync also fails (same source data) — there is no last-known-good.
	waitFor(t, 2*time.Second, func() bool {
		return logger.warnCount("config update rejected") >= 1
	})

	if products.Count() != 0 {
		t.Errorf("loaded invalid snapshot from storage: Count=%d", products.Count())
	}
	if !products.Version().IsZero() {
		t.Errorf("version set to %q from invalid stored snapshot", products.Version())
	}

	cancel()
	<-errCh
}

// TestValidator_SingletonRejects: same flow for singleton — validator runs
// and a rejected value is not applied.
func TestValidator_SingletonRejects(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	store := newMockStorage()
	notif := newMockNotifier()
	reg := newMockRegistry()
	src := &twoPCSingletonSource{
		value:        twoPCProfile{Title: "bad"},
		lastModified: now,
	}

	logger := &captureLogger{}
	settings := config.NewSingleton[twoPCProfile]("settings")

	mgr := manager.New(store, notif, reg, manager.Options{
		PollInterval:             time.Hour,
		WaitConfirmationsTimeout: 200 * time.Millisecond,
		ServiceName:              "svc",
	},
		manager.WithInstanceID("inst-1"),
		manager.WithLogger(logger),
	)

	manager.RegisterSingletonSource(mgr, settings, src,
		manager.WithSingletonValidator(func(s *twoPCProfile) error {
			if s.Title == "bad" {
				return errors.New("bad title")
			}
			return nil
		}),
	)

	errCh := make(chan error, 1)
	go func() { errCh <- mgr.Start(ctx) }()

	waitFor(t, 2*time.Second, func() bool {
		return logger.warnCount("config update rejected") >= 1
	})

	if !settings.Version().IsZero() {
		t.Errorf("singleton version advanced despite validator rejection: %q", settings.Version())
	}

	cancel()
	<-errCh
}
