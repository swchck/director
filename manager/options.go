package manager

import "time"

const (
	defaultPollInterval             = 5 * time.Minute
	defaultWSPollInterval           = 15 * time.Minute
	defaultWSDebounce               = 2 * time.Second
	defaultHeartbeatInterval        = 10 * time.Second
	defaultWaitConfirmationsTimeout = 30 * time.Second
	defaultAdvisoryLockKey          = int64(987654321)
	defaultMaintenanceInterval      = 1 * time.Hour
)

// Options configures the Manager behavior.
type Options struct {
	// PollInterval is how often the manager checks the source for version
	// changes. Default: 5 minutes.
	PollInterval time.Duration

	// HeartbeatInterval is how often the instance heartbeats to the registry.
	// Default: 10 seconds.
	HeartbeatInterval time.Duration

	// WaitConfirmationsTimeout is how long the leader waits for confirmations:
	// observational by default, the 2PC abort deadline. Default: 30 seconds.
	WaitConfirmationsTimeout time.Duration

	// AdvisoryLockKey is the Postgres advisory lock key for leader election. All
	// instances of the same service must use the same key. Default: 987654321.
	AdvisoryLockKey int64

	// WSPollInterval overrides PollInterval when WebSocket is active, where
	// polling is only a safety net for missed events. Default: 15 minutes.
	WSPollInterval time.Duration

	// WSDebounce collapses a burst of WebSocket events into one sync; a further
	// event resets it. Default: 2 seconds. Negative syncs on every event.
	WSDebounce time.Duration

	// ServiceName identifies this service in the instance registry.
	// Required.
	ServiceName string

	// RequireUnanimousApply enables 2PC; one broken replica then blocks all updates.
	// Must match across a service's replicas. Default: false. docs/sync-protocol.md.
	RequireUnanimousApply bool

	// PrepareTTL bounds how long a follower holds a staged snapshot, swept on the
	// reconcile tick. Default: 2 × WaitConfirmationsTimeout.
	PrepareTTL time.Duration

	// SnapshotRetention is the minimum age a non-active snapshot must reach to be
	// deleted by the maintenance loop, with its apply-log rows. Default: 0 (off).
	SnapshotRetention time.Duration

	// InstanceRetention is the heartbeat staleness at which maintenance deletes a
	// registry row. Keep it far above the registry's own threshold. Default: 0 (off).
	InstanceRetention time.Duration

	// MaintenanceInterval is how often the leader applies SnapshotRetention and
	// InstanceRetention. Default: 1 hour; unused if both retentions are 0.
	MaintenanceInterval time.Duration

	// ManualSyncOnly syncs from the source only on SyncNow. Startup loads, heartbeat,
	// follower protocol, maintenance and catch-up all keep running. Default: false.
	ManualSyncOnly bool
}

func (o Options) withDefaults() Options {
	if o.PollInterval <= 0 {
		o.PollInterval = defaultPollInterval
	}

	if o.HeartbeatInterval <= 0 {
		o.HeartbeatInterval = defaultHeartbeatInterval
	}

	if o.WaitConfirmationsTimeout <= 0 {
		o.WaitConfirmationsTimeout = defaultWaitConfirmationsTimeout
	}

	if o.WSPollInterval <= 0 {
		o.WSPollInterval = defaultWSPollInterval
	}

	if o.WSDebounce < 0 {
		o.WSDebounce = 0
	} else if o.WSDebounce == 0 {
		o.WSDebounce = defaultWSDebounce
	}

	if o.AdvisoryLockKey == 0 {
		o.AdvisoryLockKey = defaultAdvisoryLockKey
	}

	if o.PrepareTTL <= 0 {
		o.PrepareTTL = 2 * o.WaitConfirmationsTimeout
	}

	if o.MaintenanceInterval <= 0 {
		o.MaintenanceInterval = defaultMaintenanceInterval
	}

	return o
}
