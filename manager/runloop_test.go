package manager_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/swchck/director/config"
	"github.com/swchck/director/manager"
	"github.com/swchck/director/notify"
	"github.com/swchck/director/registry"
)

// runLoopSource is under test control: the reported version advances by step, an error
// makes the leader skip the collection, and gate holds the first check open.
type runLoopSource struct {
	mu       sync.Mutex
	items    []twoPCArticle
	modified time.Time
	step     time.Duration
	err      error
	gate     chan struct{}

	versionCalls atomic.Int32
}

func (s *runLoopSource) List(_ context.Context) ([]twoPCArticle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]twoPCArticle, len(s.items))
	copy(out, s.items)

	return out, nil
}

func (s *runLoopSource) LastModified(_ context.Context) (time.Time, error) {
	call := s.versionCalls.Add(1)

	s.mu.Lock()
	s.modified = s.modified.Add(s.step)
	ts, err, gate := s.modified, s.err, s.gate
	s.mu.Unlock()

	if gate != nil && call == 1 {
		<-gate
	}

	return ts, err
}

// -- Two-writer fixture: "alpha" is advanced by the leader path, "beta" by the follower
// path (its version check fails), so each collection has exactly one writer.

// epoch is the base timestamp both version sequences are derived from.
var epoch = time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

// driveIterations is how many rounds of each writer the invariant tests run.
const driveIterations = 40

type twoWriterFixture struct {
	store  *mockStorage
	notif  *mockNotifier
	logger *captureLogger
	mgr    *manager.Manager
	alpha  *config.Collection[twoPCArticle]
	beta   *config.Collection[twoPCArticle]
}

func newTwoWriterFixture(t *testing.T, heartbeat time.Duration) *twoWriterFixture {
	t.Helper()

	store := newMockStorage()
	notif := newMockNotifier()
	logger := &captureLogger{}

	alpha := config.NewCollection[twoPCArticle]("alpha")
	beta := config.NewCollection[twoPCArticle]("beta")

	alphaSrc := &runLoopSource{
		items:    []twoPCArticle{{ID: 1, Name: "alpha"}},
		modified: epoch,
		step:     time.Second,
	}
	betaSrc := &runLoopSource{err: fmt.Errorf("beta source unreachable")}

	mgr := manager.New(store, notif, newMockRegistry(), manager.Options{
		PollInterval:      time.Hour,
		HeartbeatInterval: heartbeat,
		// The apply log is never confirmed by a peer here; a short timeout keeps
		// each cycle to a few milliseconds instead of a 500ms poll tick.
		WaitConfirmationsTimeout: 5 * time.Millisecond,
		ServiceName:              "runloop-svc",
	},
		manager.WithInstanceID("inst-1"),
		manager.WithLogger(logger),
	)

	manager.RegisterCollectionSource(mgr, alpha, alphaSrc)
	manager.RegisterCollectionSource(mgr, beta, betaSrc)

	return &twoWriterFixture{
		store:  store,
		notif:  notif,
		logger: logger,
		mgr:    mgr,
		alpha:  alpha,
		beta:   beta,
	}
}

// start runs the manager until the test ends.
func (f *twoWriterFixture) start(t *testing.T) {
	t.Helper()

	startManager(t, f.mgr, f.logger)
}

// pushBeta stores a snapshot for beta at a version derived from n and delivers
// the matching sync event, which the run loop applies via the follower path.
func (f *twoWriterFixture) pushBeta(t *testing.T, n int) {
	t.Helper()

	version := config.NewVersion(epoch.Add(time.Duration(n) * time.Second)).String()

	content, err := json.Marshal([]twoPCArticle{{ID: n, Name: fmt.Sprintf("beta-%d", n)}})
	if err != nil {
		t.Fatalf("marshal beta content: %v", err)
	}
	if err := f.store.SaveSnapshot(context.Background(), "beta", version, content); err != nil {
		t.Fatalf("save beta snapshot: %v", err)
	}

	f.notif.subCh <- notify.Event{
		Action:     notify.ActionSync,
		Collection: "beta",
		Version:    version,
		InstanceID: "inst-2",
	}
}

// drive runs both writers concurrently: SyncNow requests on one goroutine,
// notify events for beta on another.
func (f *twoWriterFixture) drive(t *testing.T) {
	t.Helper()

	ctx := context.Background()

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for range driveIterations {
			f.mgr.SyncNow(ctx)
			time.Sleep(time.Millisecond)
		}
	}()

	go func() {
		defer wg.Done()
		for n := 1; n <= driveIterations; n++ {
			f.pushBeta(t, n)
			time.Sleep(time.Millisecond)
		}
	}()

	wg.Wait()
}

// reentrancyDetector fails the moment two swaps overlap: hooks run synchronously inside
// Swap, so a second entry means two goroutines are mutating config state.
type reentrancyDetector struct {
	inside    atomic.Int32
	overlaps  atomic.Int32
	hookCalls atomic.Int32
}

func (d *reentrancyDetector) hook(_, _ []twoPCArticle) {
	d.hookCalls.Add(1)

	if d.inside.Add(1) != 1 {
		d.overlaps.Add(1)
	}

	// Hold the hook open to widen the window a concurrent writer would land in.
	time.Sleep(2 * time.Millisecond)

	d.inside.Add(-1)
}

// TestRunLoop_ConcurrentWritersNeverOverlap drives the leader path (SyncNow) and the
// follower path (notify) hard, asserting no two swaps are ever in flight together.
func TestRunLoop_ConcurrentWritersNeverOverlap(t *testing.T) {
	f := newTwoWriterFixture(t, 100*time.Millisecond)

	detector := &reentrancyDetector{}
	f.alpha.OnChange(detector.hook)
	f.beta.OnChange(detector.hook)

	f.start(t)
	f.drive(t)

	// Both writers must actually have run, or the assertion below proves nothing.
	waitFor(t, 5*time.Second, func() bool { return detector.hookCalls.Load() >= 20 })

	if got := detector.hookCalls.Load(); got < 20 {
		t.Fatalf("OnChange fired %d times, want at least 20 — the writers did not run", got)
	}
	if f.beta.Count() == 0 {
		t.Fatal("beta never applied a notify event — the follower path did not run")
	}
	if f.alpha.Version().IsZero() {
		t.Fatal("alpha never synced — the leader path did not run")
	}

	if got := detector.overlaps.Load(); got != 0 {
		t.Errorf("%d overlapping swaps out of %d — config state was mutated off the run loop",
			got, detector.hookCalls.Load())
	}
}

// aggregateRecorder is the consumer pattern the invariant exists for: an OnChange hook
// reading several collections and storing one derived object.
type aggregateRecorder struct {
	alpha *config.Collection[twoPCArticle]
	beta  *config.Collection[twoPCArticle]

	mu          sync.Mutex
	alphaHigh   config.Version
	betaHigh    config.Version
	assemblies  int
	regressions []string
}

// assemble reads both collections and stores the pair. work is how long it takes, so a
// run that starts first can finish last — the ordering that publishes staleness.
func (r *aggregateRecorder) assemble(work time.Duration) {
	alphaVer := r.alpha.Version()
	time.Sleep(work)
	betaVer := r.beta.Version()

	r.mu.Lock()
	defer r.mu.Unlock()

	r.assemblies++

	if r.alphaHigh.After(alphaVer) {
		r.regressions = append(r.regressions,
			fmt.Sprintf("alpha %s after %s", alphaVer.String(), r.alphaHigh.String()))
	} else {
		r.alphaHigh = alphaVer
	}

	if r.betaHigh.After(betaVer) {
		r.regressions = append(r.regressions,
			fmt.Sprintf("beta %s after %s", betaVer.String(), r.betaHigh.String()))
	} else {
		r.betaHigh = betaVer
	}
}

func (r *aggregateRecorder) snapshot() (int, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]string, len(r.regressions))
	copy(out, r.regressions)

	return r.assemblies, out
}

// TestRunLoop_CrossCollectionAggregateStaysConsistent: an aggregate assembled across
// collections in an OnChange hook never goes backwards, since swaps are serialised.
func TestRunLoop_CrossCollectionAggregateStaysConsistent(t *testing.T) {
	// No reconcile ticker: follower catch-up applying the active snapshot would
	// add swaps that say nothing about writer serialisation.
	f := newTwoWriterFixture(t, time.Hour)

	rec := &aggregateRecorder{alpha: f.alpha, beta: f.beta}
	f.alpha.OnChange(func(_, _ []twoPCArticle) { rec.assemble(time.Millisecond) })
	f.beta.OnChange(func(_, _ []twoPCArticle) { rec.assemble(5 * time.Millisecond) })

	f.start(t)
	f.drive(t)

	waitFor(t, 5*time.Second, func() bool {
		assemblies, _ := rec.snapshot()
		return assemblies >= 20
	})

	assemblies, regressions := rec.snapshot()
	if assemblies < 20 {
		t.Fatalf("aggregate assembled %d times, want at least 20", assemblies)
	}
	if len(regressions) > 0 {
		t.Errorf("aggregate observed %d stale assemblies out of %d, first: %s",
			len(regressions), assemblies, regressions[0])
	}
}

// -- Single-collection fixture: manual mode with an active snapshot already in storage,
// so startup loads without syncing and every cycle the test sees came from SyncNow.

type manualFixture struct {
	registry *mockRegistry
	logger   *captureLogger
	mgr      *manager.Manager
	src      *runLoopSource
}

func newManualFixture(t *testing.T, heartbeat time.Duration, gate chan struct{}) *manualFixture {
	t.Helper()

	version := config.NewVersion(epoch).String()

	store := newMockStorage()
	content, err := json.Marshal([]twoPCArticle{{ID: 1, Name: "seeded"}})
	if err != nil {
		t.Fatalf("marshal seed content: %v", err)
	}
	if err := store.SaveSnapshot(context.Background(), "items", version, content); err != nil {
		t.Fatalf("save seed snapshot: %v", err)
	}
	if err := store.ActivateSnapshot(context.Background(), "items", version); err != nil {
		t.Fatalf("activate seed snapshot: %v", err)
	}

	registry := newMockRegistry()
	logger := &captureLogger{}
	items := config.NewCollection[twoPCArticle]("items")

	// A constant version means every cycle stops at the version check, so cycle
	// count is exactly the number of version checks.
	src := &runLoopSource{
		items:    []twoPCArticle{{ID: 1, Name: "seeded"}},
		modified: epoch,
		gate:     gate,
	}

	mgr := manager.New(store, newMockNotifier(), registry, manager.Options{
		PollInterval:             time.Hour,
		HeartbeatInterval:        heartbeat,
		WaitConfirmationsTimeout: 5 * time.Millisecond,
		ServiceName:              "runloop-svc",
		ManualSyncOnly:           true,
	},
		manager.WithInstanceID("inst-1"),
		manager.WithLogger(logger),
	)

	manager.RegisterCollectionSource(mgr, items, src)

	return &manualFixture{
		registry: registry,
		logger:   logger,
		mgr:      mgr,
		src:      src,
	}
}

// start runs the manager until the test ends, returning startManager's stop.
func (f *manualFixture) start(t *testing.T) func() {
	t.Helper()

	return startManager(t, f.mgr, f.logger)
}

// settle blocks until sample stops changing, so a test can assert on a total
// once the run loop has finished everything that was queued.
func settle(t *testing.T, sample func() int32) {
	t.Helper()

	const quiet = 200 * time.Millisecond

	deadline := time.Now().Add(5 * time.Second)
	last := sample()

	for time.Now().Before(deadline) {
		time.Sleep(quiet)

		got := sample()
		if got == last {
			return
		}
		last = got
	}

	t.Fatalf("run loop never went quiet, last sample %d", last)
}

// syncNowMustNotBlock fails the test if SyncNow does not hand back control
// promptly, whatever state the manager is in.
func syncNowMustNotBlock(t *testing.T, mgr *manager.Manager) {
	t.Helper()

	const limit = time.Second

	done := make(chan struct{})
	go func() {
		defer close(done)
		mgr.SyncNow(context.Background())
	}()

	select {
	case <-done:
	case <-time.After(limit):
		t.Fatalf("SyncNow did not return within %s", limit)
	}
}

// TestSyncNow_BurstCoalescesIntoOneCycle: a webhook fired per updated collection must
// collapse into the running cycle plus at most one more, not one cycle per call.
func TestSyncNow_BurstCoalescesIntoOneCycle(t *testing.T) {
	const callers = 50

	gate := make(chan struct{})
	f := newManualFixture(t, time.Hour, gate)
	f.start(t)

	ctx := context.Background()

	var wg sync.WaitGroup
	wg.Add(callers)
	for range callers {
		go func() {
			defer wg.Done()
			f.mgr.SyncNow(ctx)
		}()
	}
	wg.Wait()

	// The run loop is now inside the first cycle, held at the version check.
	waitFor(t, 5*time.Second, func() bool { return f.src.versionCalls.Load() >= 1 })
	if f.src.versionCalls.Load() == 0 {
		t.Fatal("no sync cycle ran for a burst of SyncNow calls")
	}

	close(gate)
	settle(t, func() int32 { return f.src.versionCalls.Load() })

	if got := f.src.versionCalls.Load(); got > 2 {
		t.Errorf("%d SyncNow calls produced %d sync cycles, want at most 2", callers, got)
	}
}

// TestHeartbeat_ContinuesDuringLongCycle: a cycle can outlast the registry's stale
// threshold, and peers drop a stale replica from their 2PC target set.
func TestHeartbeat_ContinuesDuringLongCycle(t *testing.T) {
	gate := make(chan struct{})
	f := newManualFixture(t, 20*time.Millisecond, gate)
	f.start(t)

	before := f.registry.heartbeatCount()

	f.mgr.SyncNow(context.Background())

	// Hold the cycle open on the run loop and watch the heartbeats arrive.
	waitFor(t, 5*time.Second, func() bool { return f.src.versionCalls.Load() >= 1 })
	if f.src.versionCalls.Load() == 0 {
		t.Fatal("SyncNow never reached the source")
	}

	waitFor(t, 5*time.Second, func() bool { return f.registry.heartbeatCount()-before >= 3 })

	got := f.registry.heartbeatCount() - before
	close(gate)

	if got < 3 {
		t.Errorf("%d heartbeats while a cycle occupied the run loop, want at least 3", got)
	}
}

// -- Startup fixture: nothing in storage, so Start runs a bootstrap sync that the gated
// source holds at its first version check. Until the gate opens there is no run loop.

// fastHeartbeat keeps the heartbeat goroutine ticking often enough for a test to
// count several ticks inside one startup sequence or one shutdown window.
const fastHeartbeat = 20 * time.Millisecond

type startupFixture struct {
	registry *mockRegistry
	logger   *captureLogger
	mgr      *manager.Manager
	src      *runLoopSource
}

func newStartupFixture(t *testing.T, gate chan struct{}) *startupFixture {
	t.Helper()

	registry := newMockRegistry()
	logger := &captureLogger{}
	items := config.NewCollection[twoPCArticle]("items")

	src := &runLoopSource{
		items:    []twoPCArticle{{ID: 1, Name: "seeded"}},
		modified: epoch,
		gate:     gate,
	}

	mgr := manager.New(newMockStorage(), newMockNotifier(), registry, manager.Options{
		PollInterval:             time.Hour,
		HeartbeatInterval:        fastHeartbeat,
		WaitConfirmationsTimeout: 5 * time.Millisecond,
		ServiceName:              "runloop-svc",
	},
		manager.WithInstanceID("inst-1"),
		manager.WithLogger(logger),
	)

	manager.RegisterCollectionSource(mgr, items, src)

	return &startupFixture{registry: registry, logger: logger, mgr: mgr, src: src}
}

// start runs the manager without waiting for the run loop — the gated source is
// holding it back — and cancels it when the test ends.
func (f *startupFixture) start(t *testing.T) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- f.mgr.Start(ctx) }()

	t.Cleanup(func() {
		cancel()
		<-errCh
	})
}

// TestHeartbeat_CoversStartupSequence: a strict-mode bootstrap can spend
// WaitConfirmationsTimeout per collection, longer than the registry's stale threshold.
func TestHeartbeat_CoversStartupSequence(t *testing.T) {
	gate := make(chan struct{})
	defer close(gate)

	f := newStartupFixture(t, gate)
	f.start(t)

	// Startup is now inside the bootstrap sync, held at the version check.
	waitFor(t, 5*time.Second, func() bool { return f.src.versionCalls.Load() >= 1 })
	if f.src.versionCalls.Load() == 0 {
		t.Fatal("startup never reached the bootstrap sync")
	}

	waitFor(t, 5*time.Second, func() bool { return f.registry.heartbeatCount() >= 3 })

	got := f.registry.heartbeatCount()

	// The gate is still shut, so every heartbeat above came from the startup
	// sequence rather than from the run loop.
	if started := f.logger.startedCount(); started > 0 {
		t.Fatalf("run loop came up during the test (%d), the gate did not hold startup", started)
	}
	if got < 3 {
		t.Errorf("%d heartbeats during the startup sequence, want at least 3", got)
	}
}

// -- Registry row repair ---------------------------------------------------

// vanishingRegistry reproduces what maintenance GC leaves behind: Heartbeat is an UPDATE
// matching nothing, reporting ErrInstanceNotFound exactly as registry/postgres does.
type vanishingRegistry struct {
	mu         sync.Mutex
	present    bool
	registers  int
	heartbeats int

	enteredOnce sync.Once
	entered     chan struct{} // closed on the first Heartbeat call
	release     chan struct{} // when non-nil, the first Heartbeat waits for it
}

func newVanishingRegistry(holdFirstHeartbeat bool) *vanishingRegistry {
	r := &vanishingRegistry{entered: make(chan struct{})}
	if holdFirstHeartbeat {
		r.release = make(chan struct{})
	}

	return r
}

func (r *vanishingRegistry) Register(_ context.Context, _, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.registers++
	r.present = true

	return nil
}

func (r *vanishingRegistry) Heartbeat(_ context.Context, _ string) error {
	r.mu.Lock()
	r.heartbeats++
	first := r.heartbeats == 1
	r.mu.Unlock()

	r.enteredOnce.Do(func() { close(r.entered) })

	if first && r.release != nil {
		<-r.release
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.present {
		return registry.ErrInstanceNotFound
	}

	return nil
}

func (r *vanishingRegistry) Deregister(_ context.Context, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.present = false

	return nil
}

func (r *vanishingRegistry) AliveCount(_ context.Context, _ string) (int, error) {
	ids, _ := r.AliveInstances(context.Background(), "")

	return len(ids), nil
}

func (r *vanishingRegistry) AliveInstances(_ context.Context, _ string) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.present {
		return nil, nil
	}

	return []string{"inst-1"}, nil
}

func (r *vanishingRegistry) DeleteStaleInstances(_ context.Context, _ time.Time) (int, error) {
	return 0, nil
}

// evict deletes the instance row, as the maintenance GC does.
func (r *vanishingRegistry) evict() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.present = false
}

func (r *vanishingRegistry) registerCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.registers
}

func (r *vanishingRegistry) alive() bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.present
}

func (r *vanishingRegistry) releaseHeartbeat() {
	close(r.release)
}

// newRepairManager builds a manager whose only job is to heartbeat against reg.
func newRepairManager(t *testing.T, reg *vanishingRegistry, logger *captureLogger) *manager.Manager {
	t.Helper()

	items := config.NewCollection[twoPCArticle]("items")

	mgr := manager.New(newMockStorage(), newMockNotifier(), reg, manager.Options{
		PollInterval:             time.Hour,
		HeartbeatInterval:        fastHeartbeat,
		WaitConfirmationsTimeout: 5 * time.Millisecond,
		ServiceName:              "runloop-svc",
		ManualSyncOnly:           true,
	},
		manager.WithInstanceID("inst-1"),
		manager.WithLogger(logger),
	)

	manager.RegisterCollectionSource(mgr, items, &runLoopSource{modified: epoch})

	return mgr
}

// TestHeartbeat_RestoresDeletedRegistryEntry: once GC has removed a live replica's row,
// plain heartbeats never bring it back and it stays out of every peer's target set.
func TestHeartbeat_RestoresDeletedRegistryEntry(t *testing.T) {
	reg := newVanishingRegistry(false)
	logger := &captureLogger{}
	mgr := newRepairManager(t, reg, logger)

	startManager(t, mgr, logger)

	waitFor(t, 5*time.Second, reg.alive)
	reg.evict()

	waitFor(t, 5*time.Second, reg.alive)

	if !reg.alive() {
		t.Error("instance never reappeared in the registry after its row was deleted")
	}
	if got := reg.registerCount(); got < 2 {
		t.Errorf("Register called %d time(s), want at least 2 — the missing row was not re-created", got)
	}
	if got := logger.warnCount("registry entry gone, re-registering"); got == 0 {
		t.Error("re-registration was not reported")
	}
}

// TestHeartbeat_DoesNotResurrectAfterStop: Stop deregisters on purpose, so a racing
// heartbeat must not re-create the row and leave a phantom in peers' target sets.
func TestHeartbeat_DoesNotResurrectAfterStop(t *testing.T) {
	reg := newVanishingRegistry(true)
	logger := &captureLogger{}
	mgr := newRepairManager(t, reg, logger)

	stop := startManager(t, mgr, logger)

	<-reg.entered // a heartbeat is in flight and held there
	registers := reg.registerCount()

	mgr.Stop()
	reg.releaseHeartbeat() // the held heartbeat now finds the row gone
	stop()                 // wait for Start, and the heartbeat goroutine, to finish

	if got := reg.registerCount(); got != registers {
		t.Errorf("Register called %d times, want %d — a deregistered instance was resurrected", got, registers)
	}
	if reg.alive() {
		t.Error("stopped instance is still live in the registry")
	}
}

// -- Instance ID uniqueness ------------------------------------------------

// TestStart_WarnsOnInstanceIDCollision: a replica drops events carrying its own ID, so
// sharers stop exchanging config. Startup is the one place the collision is visible.
func TestStart_WarnsOnInstanceIDCollision(t *testing.T) {
	cases := []struct {
		name     string
		alive    []string
		wantWarn int
	}{
		{name: "own id already live", alive: []string{"inst-1"}, wantWarn: 1},
		{name: "only other ids live", alive: []string{"inst-2", "inst-3"}, wantWarn: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := newMockRegistry()
			reg.instances = tc.alive

			logger := &captureLogger{}
			items := config.NewCollection[twoPCArticle]("items")

			mgr := manager.New(newMockStorage(), newMockNotifier(), reg, manager.Options{
				PollInterval:             time.Hour,
				HeartbeatInterval:        time.Hour,
				WaitConfirmationsTimeout: 5 * time.Millisecond,
				ServiceName:              "runloop-svc",
				ManualSyncOnly:           true,
			},
				manager.WithInstanceID("inst-1"),
				manager.WithLogger(logger),
			)
			manager.RegisterCollectionSource(mgr, items, &runLoopSource{modified: epoch})

			startManager(t, mgr, logger)

			if got := logger.warnCount("instance id already live in the registry"); got != tc.wantWarn {
				t.Errorf("collision warnings = %d, want %d", got, tc.wantWarn)
			}
		})
	}
}

// resubscribeFailNotifier hands out one subscription then refuses, driving the run loop
// into the resubscribe path, where it errors while the caller's context is still alive.
type resubscribeFailNotifier struct {
	mu   sync.Mutex
	subs int
	ch   chan notify.Event
}

func (n *resubscribeFailNotifier) Publish(_ context.Context, _ notify.Event) error { return nil }

func (n *resubscribeFailNotifier) Subscribe(_ context.Context) (<-chan notify.Event, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.subs++
	if n.subs > 1 {
		return nil, notify.ErrClosed
	}

	n.ch = make(chan notify.Event)

	return n.ch, nil
}

func (n *resubscribeFailNotifier) Close() error { return nil }

func (n *resubscribeFailNotifier) subscribed() bool {
	n.mu.Lock()
	defer n.mu.Unlock()

	return n.subs == 1
}

func (n *resubscribeFailNotifier) dropSubscription() {
	n.mu.Lock()
	defer n.mu.Unlock()

	close(n.ch)
}

// TestRunLoop_ShutdownOnResubscribeFailure: a failed resubscribe ends the loop while the
// caller's context is still alive, so shutdown cannot wait on that context.
func TestRunLoop_ShutdownOnResubscribeFailure(t *testing.T) {
	notif := &resubscribeFailNotifier{}
	items := config.NewCollection[twoPCArticle]("items")

	mgr := manager.New(newMockStorage(), notif, newMockRegistry(), manager.Options{
		PollInterval:             time.Hour,
		HeartbeatInterval:        time.Hour,
		WaitConfirmationsTimeout: 5 * time.Millisecond,
		ServiceName:              "runloop-svc",
	})
	manager.RegisterCollectionSource(mgr, items, &runLoopSource{modified: epoch})

	// t.Context() stays alive until cleanup, so the run loop must end on its own.
	errCh := make(chan error, 1)
	go func() { errCh <- mgr.Start(t.Context()) }()

	waitFor(t, 5*time.Second, notif.subscribed)
	if !notif.subscribed() {
		t.Fatal("manager never subscribed")
	}
	notif.dropSubscription()

	select {
	case err := <-errCh:
		if err == nil {
			t.Error("Start returned nil, want the resubscribe error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after the resubscribe failed")
	}
}

// TestSyncNow_Edges: the manager must survive the calls a webhook handler can
// make around the lifecycle — no panic, no deadlock, no unbounded block.
func TestSyncNow_Edges(t *testing.T) {
	ctx := context.Background()

	// A webhook-driven service serves HTTP before Start is entered, and manual mode
	// with an active snapshot runs no startup sync — dropping the request loses it.
	t.Run("before Start is served when the run loop comes up", func(t *testing.T) {
		f := newManualFixture(t, time.Hour, nil)

		syncNowMustNotBlock(t, f.mgr)

		if got := f.src.versionCalls.Load(); got != 0 {
			t.Errorf("source version-checked %d times before Start — the cycle ran off the run loop", got)
		}

		f.start(t)

		waitFor(t, 5*time.Second, func() bool { return f.src.versionCalls.Load() >= 1 })

		if got := f.src.versionCalls.Load(); got == 0 {
			t.Error("a SyncNow that arrived before Start never reached the source — the request was lost")
		}
		if got := f.logger.warnCount("request dropped"); got != 0 {
			t.Errorf("%d SyncNow requests reported as dropped, want 0 — the manager was still going to run", got)
		}
	})

	t.Run("after Stop is dropped", func(t *testing.T) {
		f := newManualFixture(t, time.Hour, nil)
		stop := f.start(t)

		f.mgr.Stop()
		stop() // wait for Start to return

		before := f.src.versionCalls.Load()

		for range 3 {
			syncNowMustNotBlock(t, f.mgr)
		}

		if got := f.src.versionCalls.Load(); got != before {
			t.Errorf("source version-checked %d times after Stop, want %d", got, before)
		}
		if got := f.logger.warnCount("SyncNow after shutdown"); got != 3 {
			t.Errorf("%d post-Stop SyncNow calls reported as dropped, want 3", got)
		}
	})

	t.Run("during an in-flight cycle returns immediately", func(t *testing.T) {
		gate := make(chan struct{})
		f := newManualFixture(t, time.Hour, gate)
		f.start(t)
		defer close(gate)

		f.mgr.SyncNow(ctx)
		waitFor(t, 5*time.Second, func() bool { return f.src.versionCalls.Load() >= 1 })
		if f.src.versionCalls.Load() == 0 {
			t.Fatal("SyncNow never reached the source")
		}

		for range 5 {
			syncNowMustNotBlock(t, f.mgr)
		}
	})
}
