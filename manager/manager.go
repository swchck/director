package manager

import (
	"context"
	"errors"
	"fmt"
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

// Manager orchestrates the lifecycle of config synchronization:
// polling Directus for changes, persisting snapshots, coordinating replicas,
// and optionally caching.
type Manager struct {
	storage  storage.Storage
	notifier notify.Channel
	registry registry.Registry
	logger   dlog.Logger

	cache         cache.Cache
	cacheStrategy cache.Strategy

	ws      *directus.WSClient
	metrics Metrics

	configs    map[string]registrable
	instanceID string
	opts       Options

	mu             sync.Mutex
	cancel         context.CancelFunc
	started        atomic.Bool
	deregisterOnce sync.Once

	// isLeader records whether this instance held the advisory lock at the
	// last sync attempt. Surfaced via Status() — best-effort, not a strong
	// guarantee that we are leader right now.
	isLeader atomic.Bool

	// syncState tracks the most recent sync attempt per collection for
	// Status(). Keyed by collection name.
	syncStateMu sync.RWMutex
	syncState   map[string]syncStateEntry
}

// New creates a new Manager.
//
// Required dependencies: storage, notifier, registry.
// Optional: cache (pass nil to disable), logger, websocket.
func New(
	store storage.Storage,
	notifier notify.Channel,
	reg registry.Registry,
	opts Options,
	mgrOpts ...ManagerOption,
) *Manager {
	m := &Manager{
		storage:    store,
		notifier:   notifier,
		registry:   reg,
		logger:     dlog.Nop(),
		metrics:    NopMetrics(),
		configs:    make(map[string]registrable),
		syncState:  make(map[string]syncStateEntry),
		instanceID: uuid.New().String(),
		opts:       opts.withDefaults(),
	}

	for _, opt := range mgrOpts {
		opt(m)
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

// WithInstanceID overrides the auto-generated instance ID.
func WithInstanceID(id string) ManagerOption {
	return func(m *Manager) {
		m.instanceID = id
	}
}

// WithMetrics enables observability by reporting sync events, cache hits,
// and follower operations to the provided Metrics implementation.
func WithMetrics(metrics Metrics) ManagerOption {
	return func(m *Manager) {
		m.metrics = metrics
	}
}

// WithWebSocket enables real-time change detection via Directus WebSocket.
// When configured, the leader subscribes to collection changes and triggers
// immediate syncs instead of waiting for the next poll cycle.
// Polling continues as a safety net at a longer interval (WSPollInterval).
func WithWebSocket(ws *directus.WSClient) ManagerOption {
	return func(m *Manager) {
		m.ws = ws
	}
}

// InstanceID returns this manager's unique instance identifier.
func (m *Manager) InstanceID() string {
	return m.instanceID
}

// register adds a config to the manager. Must be called before Start.
// Panics if called after Start has been invoked.
func (m *Manager) register(reg registrable) {
	if m.started.Load() {
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

// Ready reports whether all registered configs have been loaded
// (every config has a non-zero version). Intended for use in Kubernetes
// readiness probes so that the pod does not receive traffic until the
// manager has applied config from cache, storage, or the source.
func (m *Manager) Ready() bool {
	return !m.hasEmptyConfigs()
}

// Start begins the config sync lifecycle. It blocks until ctx is cancelled
// or Stop is called.
//
// Startup sequence:
//  1. Register instance in the registry
//  2. Load from cache (if enabled with ReadThrough/ReadWriteThrough)
//  3. Load from storage (active snapshots)
//  4. Perform initial sync from Directus
//  5. Subscribe to notifications and optionally WebSocket
//  6. Start poll loop, heartbeat, and notification/WS listener
func (m *Manager) Start(ctx context.Context) error {
	if len(m.configs) == 0 {
		return ErrNoConfigs
	}

	m.started.Store(true)

	ctx, cancel := context.WithCancel(ctx)

	m.mu.Lock()
	m.cancel = cancel
	m.mu.Unlock()

	defer cancel()

	// 1. Register in the instance registry.
	if err := m.registry.Register(ctx, m.instanceID, m.opts.ServiceName); err != nil {
		return fmt.Errorf("manager: register instance: %w", err)
	}

	defer m.deregisterOnce.Do(func() {
		if err := m.registry.Deregister(context.Background(), m.instanceID); err != nil {
			m.logger.Error("manager: deregister instance", dlog.Err(err))
		}
	})

	// 2. Load from cache for fast startup.
	m.loadFromCache(ctx)

	// 3. Load from storage (active snapshots).
	m.loadFromStorage(ctx)

	// 4. Initial sync.
	switch {
	case !m.opts.ManualSyncOnly:
		m.syncAll(ctx)
	case m.hasEmptyConfigs():
		// Manual mode bootstrap: if cache and storage had no data for some
		// collections (first deploy), sync once from the source so the
		// service starts with a valid config. Subsequent updates are manual.
		m.logger.Info("manager: manual mode bootstrap — empty collections detected, running initial sync")
		m.syncAll(ctx)
	default:
		// Manual mode with data already loaded from storage: syncAll is
		// skipped, so its version-skip cache repair never fires. Warm the
		// cache here so subsequent rolling-restart pods can start from cache
		// instead of going to storage every time.
		m.warmCacheIfMissing(ctx)
	}

	// 5. Subscribe to notifications.
	events, err := m.notifier.Subscribe(ctx)
	if err != nil {
		return fmt.Errorf("manager: subscribe: %w", err)
	}

	// 5b. Subscribe to Directus WebSocket (optional, skipped in manual mode).
	var wsEvents <-chan directus.ChangeEvent
	if m.ws != nil && !m.opts.ManualSyncOnly {
		collections := m.collectionNames()
		var wsErr error
		wsEvents, wsErr = m.ws.Subscribe(ctx, collections...)
		if wsErr != nil {
			m.logger.Warn("manager: websocket subscribe failed, polling only", dlog.Err(wsErr))
			// Non-fatal: continue with polling.
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

	// 6. Run event loops.
	return m.run(ctx, events, wsEvents)
}

// Stop signals the manager to shut down gracefully.
//
// It deregisters the instance from the registry before stopping the event
// loop. This removes the instance from AliveInstances immediately, preventing
// phantom instances in 2PC target sets during rolling deployments.
func (m *Manager) Stop() {
	// Deregister first so the instance disappears from AliveInstances
	// before the event loop stops. This is critical for 2PC mode:
	// without it, the dying instance stays in the target set for up to
	// staleThreshold (30s), causing prepare timeouts and aborted rounds.
	m.deregisterOnce.Do(func() {
		if err := m.registry.Deregister(context.Background(), m.instanceID); err != nil {
			m.logger.Error("manager: deregister on stop", dlog.Err(err))
		}
	})

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cancel != nil {
		m.cancel()
	}
}

// SyncNow triggers an immediate sync cycle outside the regular poll interval.
func (m *Manager) SyncNow(ctx context.Context) {
	m.syncAll(ctx)
}

// run is the main event loop: polls the source, sends heartbeats, handles
// notifications and optionally WebSocket change events with debouncing.
func (m *Manager) run(ctx context.Context, events <-chan notify.Event, wsEvents <-chan directus.ChangeEvent) error {
	// Poll ticker: in manual mode, create a stopped ticker so the select case
	// never fires but the variable is still valid for WS fallback reset.
	pollTicker := time.NewTicker(m.opts.PollInterval)
	defer pollTicker.Stop()

	if m.opts.ManualSyncOnly {
		pollTicker.Stop() // never fires
	} else if wsEvents != nil {
		// When WebSocket is active, use a longer poll interval as safety net.
		pollTicker.Reset(m.opts.WSPollInterval)
	}

	heartbeatTicker := time.NewTicker(m.opts.HeartbeatInterval)
	defer heartbeatTicker.Stop()

	// Maintenance ticker for periodic GC of old snapshots and stale instances.
	// The channel is left nil when both retentions are disabled so the select
	// case never fires — no wakeups, no work.
	var maintenanceCh <-chan time.Time
	if m.opts.SnapshotRetention > 0 || m.opts.InstanceRetention > 0 {
		t := time.NewTicker(m.opts.MaintenanceInterval)
		defer t.Stop()
		maintenanceCh = t.C
	}

	// Debounce: collect changed collections over a short window, then sync once.
	// This prevents mass rebuilds when many items are created/updated in quick succession.
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

		case <-heartbeatTicker.C:
			if err := m.registry.Heartbeat(ctx, m.instanceID); err != nil {
				m.logger.Error("manager: heartbeat failed", dlog.Err(err))
			}

			// In manual mode, skip automatic sync — only SyncNow triggers it.
			// Follower self-heal still runs so followers catch up to manually
			// triggered leader syncs.
			if m.opts.ManualSyncOnly {
				m.followerCatchUp(ctx)
			} else {
				// Attempt leader election on every heartbeat. This reduces the
				// leadership vacuum from PollInterval (5m) to HeartbeatInterval
				// (10s) during rolling deployments. The overhead is one
				// pg_try_advisory_lock call per pod per heartbeat (trivial).
				// If the lock is acquired, syncAll runs version checks and
				// skips the full fetch when versions match.
				if wasLeader := m.syncAll(ctx); !wasLeader {
					// Follower self-heal: compare local version with active
					// snapshot. If behind (e.g. missed a notification due to
					// connection drop or buffer overflow), fetch and apply.
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

			// No debounce — sync immediately.
			if m.opts.WSDebounce == 0 {
				m.handleWSChange(ctx, change)
				continue
			}

			// Debounce: accumulate collections and reset timer.
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
			// Debounce window expired — sync all accumulated collections.
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
