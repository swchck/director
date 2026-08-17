package manager_test

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/swchck/director/config"
	"github.com/swchck/director/manager"
	"github.com/swchck/director/notify"
	"github.com/swchck/director/notify/notifytest"
	"github.com/swchck/director/registry"
	"github.com/swchck/director/storage"
)

// -- cluster doubles -------------------------------------------------------

// clusterHub is a shared notify transport for tests running more than one
// Manager. It fans out to the subscriptions that exist at publish time and never
// replays what a subscriber missed, as PostgreSQL LISTEN/NOTIFY does.
type clusterHub struct {
	mu     sync.Mutex
	subs   []chan notify.Event
	events []notify.Event
}

func newClusterHub() *clusterHub { return &clusterHub{} }

// client returns one replica's handle. A non-nil barrier holds that replica at its
// subscribe step until every replica sharing the barrier has reached it.
func (h *clusterHub) client(barrier *startBarrier) *hubClient {
	return &hubClient{hub: h, barrier: barrier}
}

func (h *clusterHub) publish(event notify.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.events = append(h.events, event)

	for _, ch := range h.subs {
		// Drop instead of block when the buffer is full, as notify/postgres does.
		select {
		case ch <- event:
		default:
		}
	}
}

func (h *clusterHub) subscribe() <-chan notify.Event {
	ch := make(chan notify.Event, 32)

	h.mu.Lock()
	h.subs = append(h.subs, ch)
	h.mu.Unlock()

	return ch
}

// count reports how many events with the given action were published.
func (h *clusterHub) count(action string) int {
	h.mu.Lock()
	defer h.mu.Unlock()

	n := 0
	for _, ev := range h.events {
		if ev.Action == action {
			n++
		}
	}

	return n
}

type hubClient struct {
	hub     *clusterHub
	barrier *startBarrier
}

func (c *hubClient) Publish(_ context.Context, event notify.Event) error {
	c.hub.publish(event)
	return nil
}

func (c *hubClient) Subscribe(ctx context.Context) (<-chan notify.Event, error) {
	// Registered before the barrier opens, so releasing it means every held replica
	// can hear from that instant on.
	ch := c.hub.subscribe()

	if err := c.barrier.wait(ctx); err != nil {
		return nil, err
	}

	return ch, nil
}

func (c *hubClient) Close() error { return nil }

// startBarrier releases the replicas holding it once all of them have arrived, so
// "started at once" is a property of the fixture and not a scheduling accident.
type startBarrier struct {
	mu      sync.Mutex
	size    int
	arrived int
	open    chan struct{}
}

func newStartBarrier(size int) *startBarrier {
	return &startBarrier{size: size, open: make(chan struct{})}
}

// wait blocks until every replica has arrived. A nil barrier never holds anyone.
func (b *startBarrier) wait(ctx context.Context) error {
	if b == nil {
		return nil
	}

	b.mu.Lock()
	b.arrived++
	if b.arrived == b.size {
		close(b.open)
	}
	b.mu.Unlock()

	select {
	case <-b.open:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// TestClusterHub_HonorsChannelContract keeps the double honest about broadcast
// and self-delivery; its no-replay behaviour is what these tests rely on.
func TestClusterHub_HonorsChannelContract(t *testing.T) {
	notifytest.RunContract(t, func(t *testing.T) (notify.Channel, notify.Channel) {
		hub := newClusterHub()
		return hub.client(nil), hub.client(nil)
	})
}

// clusterRegistry is a shared instance registry: an instance becomes visible to
// every peer's AliveInstances the moment it registers, as a heartbeating pod is.
type clusterRegistry struct {
	mu    sync.Mutex
	alive map[string]bool
}

func newClusterRegistry(seed ...string) *clusterRegistry {
	r := &clusterRegistry{alive: make(map[string]bool)}
	for _, id := range seed {
		r.alive[id] = true
	}

	return r
}

func (r *clusterRegistry) Register(_ context.Context, instanceID, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.alive[instanceID] = true

	return nil
}

func (r *clusterRegistry) Heartbeat(_ context.Context, instanceID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.alive[instanceID] {
		return registry.ErrInstanceNotFound
	}

	return nil
}

func (r *clusterRegistry) Deregister(_ context.Context, instanceID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.alive, instanceID)

	return nil
}

func (r *clusterRegistry) AliveCount(_ context.Context, _ string) (int, error) {
	return r.aliveCount(), nil
}

func (r *clusterRegistry) AliveInstances(_ context.Context, _ string) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]string, 0, len(r.alive))
	for id := range r.alive {
		out = append(out, id)
	}

	return out, nil
}

func (r *clusterRegistry) DeleteStaleInstances(_ context.Context, _ time.Time) (int, error) {
	return 0, nil
}

func (r *clusterRegistry) aliveCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return len(r.alive)
}

// gatedStorage holds a replica at its first active-snapshot read — the load step
// that sits between Register and the subscribe in the startup sequence.
type gatedStorage struct {
	*mockStorage
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newGatedStorage(store *mockStorage) *gatedStorage {
	return &gatedStorage{
		mockStorage: store,
		entered:     make(chan struct{}),
		release:     make(chan struct{}),
	}
}

func (s *gatedStorage) GetActiveSnapshot(ctx context.Context, collection string) (*storage.Snapshot, error) {
	s.once.Do(func() {
		close(s.entered)
		<-s.release
	})

	return s.mockStorage.GetActiveSnapshot(ctx, collection)
}

// bootstrapSource counts version checks, so a test can prove the bootstrap retry
// stops once every config is loaded.
type bootstrapSource struct {
	items        []twoPCArticle
	lastModified time.Time
	versionCalls atomic.Int32
}

func (s *bootstrapSource) List(_ context.Context) ([]twoPCArticle, error) {
	out := make([]twoPCArticle, len(s.items))
	copy(out, s.items)

	return out, nil
}

func (s *bootstrapSource) LastModified(_ context.Context) (time.Time, error) {
	s.versionCalls.Add(1)

	return s.lastModified, nil
}

// -- fixture ---------------------------------------------------------------

// clusterReplica is one Manager wired to shared cluster infrastructure.
type clusterReplica struct {
	mgr      *manager.Manager
	articles *config.Collection[twoPCArticle]
	logger   *captureLogger
}

// strictManualOptions is the deployment shape the startup race needs: strict 2PC
// plus manual sync. WaitConfirmationsTimeout must exceed the 500ms prepare poll
// so a responsive follower can still be seen inside a round.
func strictManualOptions() manager.Options {
	return manager.Options{
		PollInterval:             time.Hour,
		HeartbeatInterval:        200 * time.Millisecond,
		WaitConfirmationsTimeout: time.Second,
		PrepareTTL:               2 * time.Second,
		ServiceName:              "test-svc",
		RequireUnanimousApply:    true,
		ManualSyncOnly:           true,
	}
}

func newClusterReplica(
	t *testing.T,
	id string,
	store storage.Storage,
	notif notify.Channel,
	reg registry.Registry,
	opts manager.Options,
	src *bootstrapSource,
) *clusterReplica {
	t.Helper()

	articles := config.NewCollection[twoPCArticle]("articles")
	logger := &captureLogger{}

	mgr := manager.New(store, notif, reg, opts,
		manager.WithInstanceID(id),
		manager.WithLogger(logger),
	)
	manager.RegisterCollectionSource(mgr, articles, src)

	return &clusterReplica{mgr: mgr, articles: articles, logger: logger}
}

// startDetached runs mgr until the test ends. Unlike startManager it does not wait
// for the run loop: these tests deliberately hold replicas mid-startup.
func startDetached(t *testing.T, mgr *manager.Manager) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	errCh := make(chan error, 1)
	go func() { errCh <- mgr.Start(ctx) }()

	t.Cleanup(func() {
		cancel()
		<-errCh
	})
}

// -- tests -----------------------------------------------------------------

// TestBootstrap_StrictManual_SimultaneousStart_ConvergesWithoutTrigger is the
// first-deploy race: replicas count towards a 2PC target set from Register on, and
// a replica held before its subscribe can neither hear a prepare nor be allowed to
// lead a round that needs one. The cluster must come up with no SyncNow.
func TestBootstrap_StrictManual_SimultaneousStart_ConvergesWithoutTrigger(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	store := newMockStorage()
	hub := newClusterHub()
	reg := newClusterRegistry()

	// Both replicas reach their subscribe step before either continues, so each one
	// is registered — a peer's 2PC target — while still unable to hear a prepare.
	barrier := newStartBarrier(2)

	// loadFromStorage sits between Register and the bootstrap round in either order,
	// so holding it until both replicas are registered is what "started at once"
	// means for the target set. It costs nothing once both are in.
	store.onGetActiveSnapshot = func(string) error {
		deadline := time.Now().Add(5 * time.Second)
		for reg.aliveCount() < 2 && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}

		return nil
	}

	items := []twoPCArticle{{ID: 1, Name: "Alpha"}}

	a := newClusterReplica(t, "replica-a", store, hub.client(barrier), reg, strictManualOptions(),
		&bootstrapSource{items: items, lastModified: now})
	b := newClusterReplica(t, "replica-b", store, hub.client(barrier), reg, strictManualOptions(),
		&bootstrapSource{items: items, lastModified: now})

	startDetached(t, a.mgr)
	startDetached(t, b.mgr)

	// Nothing external happens from here on.
	waitFor(t, 10*time.Second, func() bool { return a.mgr.Ready() && b.mgr.Ready() })

	if !a.mgr.Ready() || !b.mgr.Ready() {
		t.Fatalf("cluster never came up without a manual trigger: "+
			"a.Ready=%v (%d items), b.Ready=%v (%d items), aborts=%d, commits=%d",
			a.mgr.Ready(), a.articles.Count(), b.mgr.Ready(), b.articles.Count(),
			hub.count(notify.ActionAbort), hub.count(notify.ActionCommit))
	}
	if a.articles.Count() != 1 || b.articles.Count() != 1 {
		t.Errorf("items after startup: a=%d b=%d, want 1 each", a.articles.Count(), b.articles.Count())
	}
	if n := hub.count(notify.ActionAbort); n != 0 {
		t.Errorf("bootstrap round aborted (%d abort events): a replica led a round "+
			"whose target could not hear it yet", n)
	}
}

// TestBootstrap_AbortedRound_RecoversOnReconcileWithoutTrigger: the bootstrap round
// is one-shot, so an abort caused by a peer that could not answer yet must not leave
// the replica unloaded for good once that peer is gone.
func TestBootstrap_AbortedRound_RecoversOnReconcileWithoutTrigger(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	store := newMockStorage()
	hub := newClusterHub()
	// A peer that is registered and heartbeating but cannot answer a prepare — the
	// state of every replica between its Register and its subscribe.
	reg := newClusterRegistry("starting-peer")

	solo := newClusterReplica(t, "solo", store, hub.client(nil), reg, strictManualOptions(),
		&bootstrapSource{items: []twoPCArticle{{ID: 1, Name: "Alpha"}}, lastModified: now})

	startDetached(t, solo.mgr)

	waitFor(t, 10*time.Second, func() bool { return hub.count(notify.ActionAbort) > 0 })

	if hub.count(notify.ActionAbort) == 0 {
		t.Fatal("the bootstrap round never aborted: the fixture reproduced nothing")
	}
	if solo.logger.warnCount("reason=timeout") == 0 {
		t.Fatal("the round aborted for some reason other than a target that never prepared")
	}
	if solo.mgr.Ready() {
		t.Fatal("replica reports ready after an aborted bootstrap round")
	}

	// The peer finishes starting, or its heartbeat goes stale: the condition that
	// aborted the bootstrap is gone, and nothing external happens from here on.
	if err := reg.Deregister(context.Background(), "starting-peer"); err != nil {
		t.Fatalf("Deregister: %v", err)
	}

	waitFor(t, 10*time.Second, func() bool { return solo.mgr.Ready() })

	if !solo.mgr.Ready() {
		t.Fatalf("replica never retried its bootstrap: Ready=false, items=%d, aborts=%d, commits=%d",
			solo.articles.Count(), hub.count(notify.ActionAbort), hub.count(notify.ActionCommit))
	}
	if solo.articles.Count() != 1 {
		t.Errorf("items after recovery = %d, want 1", solo.articles.Count())
	}
}

// TestBootstrap_SlowLoadingReplica_AnswersPrepareOfFirstRound: a replica still
// loading when the leader's round starts is already a target, so it must be able
// to answer that round instead of costing it an abort.
func TestBootstrap_SlowLoadingReplica_AnswersPrepareOfFirstRound(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	store := newMockStorage()
	hub := newClusterHub()
	reg := newClusterRegistry()
	slowStore := newGatedStorage(store)

	opts := strictManualOptions()
	opts.WaitConfirmationsTimeout = 2 * time.Second // room for several prepare polls
	opts.HeartbeatInterval = 2 * time.Second        // keep bootstrap retries out of the window

	items := []twoPCArticle{{ID: 1, Name: "Alpha"}}

	slow := newClusterReplica(t, "slow", slowStore, hub.client(nil), reg, opts,
		&bootstrapSource{items: items, lastModified: now})
	leader := newClusterReplica(t, "leader", store, hub.client(nil), reg, opts,
		&bootstrapSource{items: items, lastModified: now})

	startDetached(t, slow.mgr)

	select {
	case <-slowStore.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the slow replica never reached its load step")
	}

	startDetached(t, leader.mgr)

	waitFor(t, 5*time.Second, func() bool { return hub.count(notify.ActionPrepare) > 0 })
	if hub.count(notify.ActionPrepare) == 0 {
		t.Fatal("the leader never started a round")
	}

	// The slow replica finishes starting while the round is still open.
	close(slowStore.release)

	waitFor(t, 10*time.Second, func() bool { return leader.mgr.Ready() && slow.mgr.Ready() })

	if !leader.mgr.Ready() || !slow.mgr.Ready() {
		t.Fatalf("cluster did not converge: leader.Ready=%v (%d items), slow.Ready=%v (%d items)",
			leader.mgr.Ready(), leader.articles.Count(), slow.mgr.Ready(), slow.articles.Count())
	}
	if n := hub.count(notify.ActionAbort); n != 0 {
		t.Errorf("round aborted (%d abort events): a replica was a target before it could hear a prepare", n)
	}
}

// TestBootstrap_ManualMode_EmptySourceCountsAsLoaded: a source that legitimately
// returns nothing still reaches a loaded state, and the retry stops there rather
// than polling forever.
func TestBootstrap_ManualMode_EmptySourceCountsAsLoaded(t *testing.T) {
	tests := []struct {
		name         string
		lastModified time.Time
	}{
		{"no items, source reports a timestamp", time.Now().UTC().Truncate(time.Second)},
		{"no items, source reports no timestamp", time.Time{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newMockStorage()
			hub := newClusterHub()
			reg := newClusterRegistry()
			src := &bootstrapSource{lastModified: tc.lastModified}

			opts := strictManualOptions()
			opts.HeartbeatInterval = 50 * time.Millisecond

			solo := newClusterReplica(t, "solo", store, hub.client(nil), reg, opts, src)
			startDetached(t, solo.mgr)

			waitFor(t, 5*time.Second, func() bool { return solo.mgr.Ready() })

			if !solo.mgr.Ready() {
				t.Fatalf("an empty source never counted as loaded: version=%q", solo.articles.Version())
			}
			if solo.articles.Count() != 0 {
				t.Errorf("items = %d, want 0", solo.articles.Count())
			}

			settled := src.versionCalls.Load()
			time.Sleep(10 * opts.HeartbeatInterval)

			if got := src.versionCalls.Load(); got != settled {
				t.Errorf("source polled %d more times after the config was loaded — "+
					"manual mode must not keep pulling once everything is loaded", got-settled)
			}
		})
	}
}

// TestAbortWarn_RepeatsWhileNothingHasLoaded: per-version dedup is right for "this
// version is bad" and wrong for "I have never come up", which must stay visible.
func TestAbortWarn_RepeatsWhileNothingHasLoaded(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	store := newMockStorage()
	hub := newClusterHub()
	reg := newClusterRegistry("ghost") // registered, never answers a prepare

	opts := strictManualOptions()
	opts.HeartbeatInterval = 5 * time.Second // no bootstrap retry inside the window

	solo := newClusterReplica(t, "solo", store, hub.client(nil), reg, opts,
		&bootstrapSource{items: []twoPCArticle{{ID: 1, Name: "Alpha"}}, lastModified: now})

	startDetached(t, solo.mgr)

	waitFor(t, 10*time.Second, func() bool { return solo.logger.startedCount() > 0 })

	if got := solo.logger.warnCount("2PC aborting round"); got != 1 {
		t.Fatalf("abort warnings after the bootstrap round = %d, want 1", got)
	}

	solo.mgr.SyncNow(context.Background())

	waitFor(t, 10*time.Second, func() bool { return hub.count(notify.ActionAbort) == 2 })

	if got := hub.count(notify.ActionAbort); got != 2 {
		t.Fatalf("aborted rounds = %d, want 2", got)
	}
	if got := solo.logger.warnCount("2PC aborting round"); got != 2 {
		t.Errorf("abort warnings = %d, want 2: a replica that has never loaded must keep reporting", got)
	}
}

// TestAbortWarn_DedupsPerVersionOnceLoaded: with a version live, a source version
// that keeps aborting is reported once — the dedup that keeps a bad version quiet.
func TestAbortWarn_DedupsPerVersionOnceLoaded(t *testing.T) {
	ctx := context.Background()
	loaded := time.Now().UTC().Truncate(time.Second)

	store := newMockStorage()
	hub := newClusterHub()
	reg := newClusterRegistry("ghost")

	// An active snapshot means this replica starts up loaded, so the abort below is
	// about the new version only.
	content, err := json.Marshal([]twoPCArticle{{ID: 1, Name: "Live"}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	liveVersion := config.NewVersion(loaded).String()
	if err := store.SaveSnapshot(ctx, "articles", liveVersion, content); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	if err := store.ActivateSnapshot(ctx, "articles", liveVersion); err != nil {
		t.Fatalf("ActivateSnapshot: %v", err)
	}

	opts := strictManualOptions()
	opts.HeartbeatInterval = 5 * time.Second

	solo := newClusterReplica(t, "solo", store, hub.client(nil), reg, opts,
		&bootstrapSource{items: []twoPCArticle{{ID: 2, Name: "Next"}}, lastModified: loaded.Add(time.Hour)})

	startDetached(t, solo.mgr)

	waitFor(t, 10*time.Second, func() bool { return solo.logger.startedCount() > 0 })

	if !solo.mgr.Ready() {
		t.Fatal("replica did not start up loaded from the active snapshot")
	}

	solo.mgr.SyncNow(ctx)
	waitFor(t, 10*time.Second, func() bool { return hub.count(notify.ActionAbort) == 1 })

	solo.mgr.SyncNow(ctx)
	waitFor(t, 10*time.Second, func() bool { return hub.count(notify.ActionAbort) == 2 })

	if got := hub.count(notify.ActionAbort); got != 2 {
		t.Fatalf("aborted rounds = %d, want 2", got)
	}
	if got := solo.logger.warnCount("2PC aborting round"); got != 1 {
		t.Errorf("abort warnings = %d, want 1: the same bad version must be reported once", got)
	}
	if got := solo.logger.debugCount("2PC aborting round (dedup)"); got != 1 {
		t.Errorf("deduped abort log lines = %d, want 1", got)
	}
}
