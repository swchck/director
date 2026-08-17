package manager

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/swchck/director/cache"
	"github.com/swchck/director/directus"
	dlog "github.com/swchck/director/log"
	"github.com/swchck/director/notify"
	"github.com/swchck/director/registry"
	"github.com/swchck/director/storage"
)

// ErrNoConfigs is returned when Start is called with no registered configs.
var ErrNoConfigs = errors.New("manager: no configs registered")

// phase is the manager's lifecycle state, ordered so that callers can ask
// whether their work can still be served with a single comparison.
type phase int32

const (
	// phaseNew: constructed, Start not entered yet.
	phaseNew phase = iota

	// phaseRunning: Start entered — the startup sequence, then the run loop.
	phaseRunning

	// phaseLeaving: Stop called. The instance is on its way out, so the heartbeat
	// must not recreate its registry row.
	phaseLeaving

	// phaseStopped: Start returned. Nothing will serve a sync request any more.
	phaseStopped
)

// Manager orchestrates config synchronization: polling for changes, persisting
// snapshots, coordinating replicas, and optionally caching.
type Manager struct {
	storage  storage.Storage
	notifier notify.Channel
	registry registry.Registry
	logger   dlog.Logger

	cache         cache.Cache
	cacheStrategy cache.Strategy

	ws      *directus.WSClient
	metrics Metrics

	// leaderMetrics and sourceMetrics are metrics when it implements the matching
	// optional interface, else a no-op. Resolved in New so call sites need no check.
	leaderMetrics LeadershipMetrics
	sourceMetrics SourceMetrics

	configs    map[string]registrable
	instanceID string
	opts       Options

	mu             sync.Mutex
	cancel         context.CancelFunc
	deregisterOnce sync.Once

	// lifecycle holds the current phase. Read it through phase().
	lifecycle atomic.Int32

	// syncRequests carries SyncNow requests to the run loop. One slot, so bursts
	// coalesce and a request made before the loop exists is still served.
	syncRequests chan struct{}

	// isLeader records whether this instance held the advisory lock at the last
	// sync attempt. Best-effort, for Status().
	isLeader atomic.Bool

	// syncState tracks the most recent sync attempt per collection for
	// Status(). Keyed by collection name.
	syncStateMu sync.RWMutex
	syncState   map[string]syncStateEntry

	// schemaCheck gates the optional Directus drift check at Start. Off by
	// default; turned on with WithSchemaCheck.
	schemaCheck        bool
	schemaCheckEntries []schemaCheckEntry
}

// New creates a new Manager. storage, notifier and registry are required; cache
// (nil disables), logger and websocket are optional ManagerOptions.
func New(
	store storage.Storage,
	notifier notify.Channel,
	reg registry.Registry,
	opts Options,
	mgrOpts ...ManagerOption,
) *Manager {
	m := &Manager{
		storage:      store,
		notifier:     notifier,
		registry:     reg,
		logger:       dlog.Nop(),
		metrics:      NopMetrics(),
		configs:      make(map[string]registrable),
		syncState:    make(map[string]syncStateEntry),
		syncRequests: make(chan struct{}, 1),
		instanceID:   uuid.New().String(),
		opts:         opts.withDefaults(),
	}

	for _, opt := range mgrOpts {
		opt(m)
	}

	m.leaderMetrics = nopMetrics{}
	if lm, ok := m.metrics.(LeadershipMetrics); ok {
		m.leaderMetrics = lm
	}

	m.sourceMetrics = nopMetrics{}
	if sm, ok := m.metrics.(SourceMetrics); ok {
		m.sourceMetrics = sm
	}

	return m
}

// ManagerOption configures optional Manager dependencies.
type ManagerOption func(*Manager)

// WithLogger sets the manager logger.
func WithLogger(logger dlog.Logger) ManagerOption {
	return func(m *Manager) {
		m.logger = logger
	}
}

// WithCache enables the cache layer with the given strategy.
func WithCache(c cache.Cache, strategy cache.Strategy) ManagerOption {
	return func(m *Manager) {
		m.cache = c
		m.cacheStrategy = strategy
	}
}

// WithInstanceID overrides the auto-generated instance ID. Derive it per pod: a
// replica drops events stamped with its own ID, so sharers never exchange config.
func WithInstanceID(id string) ManagerOption {
	return func(m *Manager) {
		m.instanceID = id
	}
}

// WithMetrics reports sync events, cache hits and follower operations to metrics,
// plus leadership and source regressions if it satisfies the optional interfaces.
func WithMetrics(metrics Metrics) ManagerOption {
	return func(m *Manager) {
		m.metrics = metrics
	}
}

// WithSchemaCheck warns at startup when a Go struct names a field Directus lacks —
// an admin rename loses data silently. Directus registrations only; never fatal.
func WithSchemaCheck() ManagerOption {
	return func(m *Manager) {
		m.schemaCheck = true
	}
}

// WithWebSocket enables real-time change detection: the leader syncs on Directus
// change events instead of waiting for a poll. Polling stays on at WSPollInterval.
func WithWebSocket(ws *directus.WSClient) ManagerOption {
	return func(m *Manager) {
		m.ws = ws
	}
}

// InstanceID returns this manager's unique instance identifier.
func (m *Manager) InstanceID() string {
	return m.instanceID
}

// phase reports the manager's current lifecycle phase.
func (m *Manager) phase() phase {
	return phase(m.lifecycle.Load())
}

// register adds a config to the manager. Must be called before Start.
// Panics if called after Start has been invoked.
func (m *Manager) register(reg registrable) {
	if m.phase() != phaseNew {
		panic("manager: register called after Start")
	}

	m.configs[reg.name()] = reg
}

// collectionNames returns the names of all registered collections.
func (m *Manager) collectionNames() []string {
	names := make([]string, 0, len(m.configs))
	for name := range m.configs {
		names = append(names, name)
	}

	return names
}

// hasEmptyConfigs returns true if any registered config has no data loaded
// (version is zero). Used to detect first-deploy bootstrap in manual mode.
func (m *Manager) hasEmptyConfigs() bool {
	for _, reg := range m.configs {
		if reg.version().IsZero() {
			return true
		}
	}

	return false
}

// Ready reports whether every registered config has a non-zero version. For a
// Kubernetes readiness probe: no traffic until config has actually been applied.
func (m *Manager) Ready() bool {
	return !m.hasEmptyConfigs()
}

// Start runs the startup sequence, then the run loop, blocking until ctx is
// cancelled or Stop is called. Sole writer of config state; docs/sync-protocol.md.
func (m *Manager) Start(ctx context.Context) error {
	if len(m.configs) == 0 {
		return ErrNoConfigs
	}

	m.lifecycle.Store(int32(phaseRunning))
	defer m.lifecycle.Store(int32(phaseStopped))

	ctx, cancel := context.WithCancel(ctx)

	m.mu.Lock()
	m.cancel = cancel
	m.mu.Unlock()

	defer cancel()

	m.warnOnInstanceIDCollision(ctx)

	if err := m.registry.Register(ctx, m.instanceID, m.opts.ServiceName); err != nil {
		return fmt.Errorf("manager: register instance: %w", err)
	}

	// Started here rather than with the run loop so it covers startup too. Its own
	// cancel: Start returns on paths that leave ctx alive (a failed resubscribe).
	heartbeatCtx, stopHeartbeat := context.WithCancel(ctx)
	heartbeatDone := make(chan struct{})

	go func() {
		defer close(heartbeatDone)
		m.heartbeatLoop(heartbeatCtx)
	}()

	// Deregister only once the heartbeat is gone, so no tick can bring the row
	// back after it is deleted.
	defer func() {
		stopHeartbeat()
		<-heartbeatDone
		m.deregister()
	}()

	m.runSchemaChecks(ctx)

	// Subscribe before loading or syncing: peers count this instance in their 2PC
	// target set from Register on, and the transports never replay a missed event.
	events, err := m.notifier.Subscribe(ctx)
	if err != nil {
		return fmt.Errorf("manager: subscribe: %w", err)
	}

	m.loadFromCache(ctx)
	m.loadFromStorage(ctx)

	switch {
	case !m.opts.ManualSyncOnly:
		m.syncAll(ctx)
	case m.hasEmptyConfigs():
		// First deploy in manual mode: neither cache nor storage had data, so
		// sync once or the service comes up with no config at all.
		m.logger.Info("manager: manual mode bootstrap — empty collections detected, running initial sync")
		m.syncAll(ctx)
	default:
		// Manual mode skips syncAll, so the version-skip cache repair inside it
		// never fires — without this every rolling-restart pod goes to storage.
		m.warmCacheIfMissing(ctx)
	}

	var wsEvents <-chan directus.ChangeEvent
	if m.ws != nil && !m.opts.ManualSyncOnly {
		collections := m.collectionNames()
		var wsErr error
		wsEvents, wsErr = m.ws.Subscribe(ctx, collections...)
		if wsErr != nil {
			m.logger.Warn("manager: websocket subscribe failed, polling only", dlog.Err(wsErr))
		} else {
			m.logger.Info("manager: websocket subscribed", dlog.Strings("collections", collections))
		}
	}

	m.logger.Info("manager: started",
		dlog.String("instance_id", m.instanceID),
		dlog.String("service", m.opts.ServiceName),
		dlog.Int("configs", len(m.configs)),
		dlog.Bool("websocket", wsEvents != nil),
		dlog.Bool("manual_sync", m.opts.ManualSyncOnly),
	)

	return m.run(ctx, events, wsEvents)
}

// Stop shuts the manager down, deregistering before the event loop stops so the
// instance leaves AliveInstances at once instead of haunting 2PC target sets.
func (m *Manager) Stop() {
	// Announce the intent before deleting the row: a heartbeat tick racing this
	// delete must not re-register the instance it just removed.
	m.lifecycle.CompareAndSwap(int32(phaseRunning), int32(phaseLeaving))

	m.deregister()

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cancel != nil {
		m.cancel()
	}
}

// deregister removes this instance's registry row, at most once per manager.
func (m *Manager) deregister() {
	m.deregisterOnce.Do(func() {
		if err := m.registry.Deregister(context.Background(), m.instanceID); err != nil {
			m.logger.Error("manager: deregister instance", dlog.Err(err))
		}
	})
}

// warnOnInstanceIDCollision reports an already-live instance ID. Warn only: a fast
// restart reusing a stable ID looks identical to a genuine collision.
func (m *Manager) warnOnInstanceIDCollision(ctx context.Context) {
	alive, err := m.registry.AliveInstances(ctx, m.opts.ServiceName)
	if err != nil {
		m.logger.Warn("manager: instance id collision check skipped", dlog.Err(err))
		return
	}

	if !slices.Contains(alive, m.instanceID) {
		return
	}

	m.logger.Warn("manager: instance id already live in the registry — "+
		"expected after a restart that reuses the id, otherwise two replicas share it "+
		"and will drop each other's notify events",
		dlog.String("instance_id", m.instanceID),
		dlog.String("service", m.opts.ServiceName),
	)
}

// SyncNow asks the run loop for a sync cycle and returns immediately, never
// blocking. Calls coalesce; one made before Start is served when the loop comes up,
// one during shutdown is dropped. ctx is unused — the cycle runs under Start's.
func (m *Manager) SyncNow(_ context.Context) {
	if m.phase() >= phaseLeaving {
		m.logger.Warn("manager: SyncNow after shutdown, request dropped")
		return
	}

	select {
	case m.syncRequests <- struct{}{}:
	default:
		m.logger.Debug("manager: sync cycle already pending, request coalesced")
	}
}

// heartbeatLoop keeps the registry entry fresh, off the run loop so a long cycle or
// a slow startup cannot let it go stale and drop us from peers' 2PC target sets.
func (m *Manager) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(m.opts.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-ticker.C:
			m.heartbeat(ctx)
		}
	}
}

// heartbeat refreshes this instance's registry row, re-registering when it is gone —
// Heartbeat is an UPDATE, so a GC'd row never returns and drops us from 2PC targets.
func (m *Manager) heartbeat(ctx context.Context) {
	err := m.registry.Heartbeat(ctx, m.instanceID)

	switch {
	case err == nil:
		return

	case !errors.Is(err, registry.ErrInstanceNotFound):
		m.logger.Error("manager: heartbeat failed", dlog.Err(err))

	case m.phase() >= phaseLeaving:
		// The row is gone because we deleted it on the way out; re-registering
		// would resurrect a phantom instance in every peer's target set.

	default:
		m.logger.Warn("manager: registry entry gone, re-registering",
			dlog.String("instance_id", m.instanceID),
			dlog.String("service", m.opts.ServiceName),
		)

		if err := m.registry.Register(ctx, m.instanceID, m.opts.ServiceName); err != nil {
			m.logger.Error("manager: re-register instance", dlog.Err(err))
		}
	}
}

// sweepExpiredStages discards staged 2PC values past their PrepareTTL. On the run
// loop, not a timer, which could delete one mid-commit; the cost is granularity.
func (m *Manager) sweepExpiredStages() {
	now := time.Now()

	for name, reg := range m.configs {
		for _, roundID := range reg.dropExpiredStages(now) {
			m.metrics.StagedDropped(name, "ttl")
			m.logger.Warn("manager: staged value dropped after TTL",
				dlog.String("collection", name),
				dlog.String("round_id", roundID),
			)
		}
	}
}

// run is the main event loop: poll ticks, SyncNow requests, notify events and
// debounced WS events. One goroutine because they all mutate config state.
func (m *Manager) run(ctx context.Context, events <-chan notify.Event, wsEvents <-chan directus.ChangeEvent) error {
	// Created then stopped in manual mode, so the case never fires while the
	// variable stays valid for the deferred Stop and any later Reset.
	pollTicker := time.NewTicker(m.opts.PollInterval)
	defer pollTicker.Stop()

	if m.opts.ManualSyncOnly {
		pollTicker.Stop()
	} else if wsEvents != nil {
		// WebSocket detects changes, so polling only has to be a safety net.
		pollTicker.Reset(m.opts.WSPollInterval)
	}

	// Reconcile at HeartbeatInterval, not PollInterval: retrying leader election
	// that often keeps the leadership vacuum short, for one lock try per tick.
	reconcileTicker := time.NewTicker(m.opts.HeartbeatInterval)
	defer reconcileTicker.Stop()

	// Left nil when both retentions are disabled, so the select case never fires
	// — no wakeups, no work.
	var maintenanceCh <-chan time.Time
	if m.opts.SnapshotRetention > 0 || m.opts.InstanceRetention > 0 {
		t := time.NewTicker(m.opts.MaintenanceInterval)
		defer t.Stop()
		maintenanceCh = t.C
	}

	// Collecting WS events over a short window keeps a bulk edit from rebuilding
	// the collection once per changed item.
	pendingCollections := make(map[string]bool)
	var debounceTimer *time.Timer
	var debounceCh <-chan time.Time // nil until first WS event

	for {
		select {
		case <-ctx.Done():
			m.logger.Info("manager: shutting down")
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			return ctx.Err()

		case <-pollTicker.C:
			m.syncAll(ctx)

		case <-m.syncRequests:
			m.syncAll(ctx)

		case <-reconcileTicker.C:
			m.sweepExpiredStages()

			// Catch-up runs in manual mode too, so followers still converge on a
			// manually triggered leader sync.
			if m.opts.ManualSyncOnly {
				// Manual mode means "pull no changes on a schedule", not "never come
				// up at all": retry the bootstrap until every config has a version.
				if m.hasEmptyConfigs() {
					m.syncAll(ctx)
				}

				m.followerCatchUp(ctx)
			} else {
				if wasLeader := m.syncAll(ctx); !wasLeader {
					// Self-heal for a notification lost to a dropped connection
					// or an overflowing buffer.
					m.followerCatchUp(ctx)
				}
			}

		case <-maintenanceCh:
			m.runMaintenance(ctx)

		case event, ok := <-events:
			if !ok {
				m.logger.Warn("manager: notification channel closed, resubscribing")

				var err error
				events, err = m.notifier.Subscribe(ctx)
				if err != nil {
					m.logger.Error("manager: resubscribe failed", dlog.Err(err))
					return fmt.Errorf("manager: resubscribe: %w", err)
				}

				continue
			}

			m.handleEvent(ctx, event)

		case change, ok := <-wsEvents:
			if !ok {
				m.logger.Warn("manager: websocket channel closed, falling back to polling")
				wsEvents = nil
				pollTicker.Reset(m.opts.PollInterval)
				continue
			}

			if change.Collection == "" {
				continue
			}

			if m.opts.WSDebounce == 0 {
				m.handleWSChange(ctx, change)
				continue
			}

			pendingCollections[change.Collection] = true

			if debounceTimer == nil {
				debounceTimer = time.NewTimer(m.opts.WSDebounce)
				debounceCh = debounceTimer.C
			} else {
				debounceTimer.Reset(m.opts.WSDebounce)
			}

			m.logger.Debug("manager: ws event queued (debouncing)",
				dlog.String("collection", change.Collection),
				dlog.String("action", change.Action),
				dlog.Int("pending", len(pendingCollections)),
			)

		case <-debounceCh:
			m.logger.Debug("manager: debounce fired, syncing collections",
				dlog.Int("count", len(pendingCollections)),
			)

			for col := range pendingCollections {
				if reg, ok := m.configs[col]; ok {
					m.syncOneForced(ctx, reg)
				}
			}

			pendingCollections = make(map[string]bool)
			debounceTimer = nil
			debounceCh = nil
		}
	}
}
