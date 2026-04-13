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
	// PollInterval is how often the manager checks Directus for version changes.
	// Default: 5 minutes.
	PollInterval time.Duration

	// HeartbeatInterval is how often the instance heartbeats to the registry.
	// Default: 10 seconds.
	HeartbeatInterval time.Duration

	// WaitConfirmationsTimeout is how long the leader waits for all replicas
	// to confirm they applied a new version before rolling back.
	// Default: 30 seconds.
	WaitConfirmationsTimeout time.Duration

	// AdvisoryLockKey is the Postgres advisory lock key used for leader election.
	// All instances of the same service must use the same key.
	// Default: 987654321.
	AdvisoryLockKey int64

	// WSPollInterval overrides PollInterval when WebSocket is active.
	// Since WebSocket provides near-instant change detection, polling serves only
	// as a safety net for missed events. Default: 15 minutes.
	WSPollInterval time.Duration

	// WSDebounce is how long to wait after a WebSocket event before syncing.
	// If more events arrive during this window, the timer resets.
	// This prevents mass rebuilds when many items are created/updated in quick succession
	// (e.g. bulk import, batch operations).
	// Default: 2 seconds. Set to 0 to disable debouncing (sync immediately on every event).
	WSDebounce time.Duration

	// ServiceName identifies this service in the instance registry.
	// Required.
	ServiceName string

	// RequireUnanimousApply enables two-phase commit (2PC) for config sync.
	//
	// When true, a new version is committed only if every alive replica
	// (snapshotted at the start of the round) successfully stages it in the
	// prepare phase. If any replica reports prepare_failed or does not
	// respond within WaitConfirmationsTimeout, the round is aborted —
	// nobody swaps — and the leader retries on the next poll/WS cycle.
	//
	// This guarantees the cluster-wide invariant that all alive replicas
	// operate on the same config version (with a small skew window of a
	// few seconds during the commit phase), at the cost of availability:
	// a single chronically-broken replica blocks all config updates.
	//
	// All replicas of the same service must use the same value for this
	// option — mixed-mode clusters are not supported.
	//
	// Default: false (eventually-consistent behavior).
	RequireUnanimousApply bool

	// PrepareTTL bounds how long a follower holds a staged snapshot in
	// memory while waiting for commit/abort from the leader. After the
	// TTL expires, the staged snapshot is dropped; a subsequent commit
	// for the same round falls back to re-loading from storage.
	// Applies only when RequireUnanimousApply is true.
	// Default: 2 × WaitConfirmationsTimeout.
	PrepareTTL time.Duration

	// SnapshotRetention sets the minimum age a non-active snapshot must
	// reach before it is eligible for deletion by the periodic maintenance
	// loop. Active snapshots are never deleted, regardless of age.
	//
	// When the maintenance loop runs (only on the leader), all snapshots
	// older than this duration that are not 'active' are removed, along
	// with their apply-log rows.
	//
	// Default: 0 (disabled — snapshots are kept forever).
	SnapshotRetention time.Duration

	// InstanceRetention sets the heartbeat-staleness threshold above which
	// an instance row is deleted from the registry by the periodic
	// maintenance loop. This is for cleaning up replicas that crashed
	// without calling Deregister and would otherwise accumulate forever.
	//
	// Choose a value well above HeartbeatInterval and the registry's stale
	// threshold (e.g. 1 hour) to avoid deleting live instances during
	// transient delays — AliveCount/AliveInstances already filter by their
	// own (much shorter) staleness window for sync correctness.
	//
	// Default: 0 (disabled — dead instance rows are kept forever).
	InstanceRetention time.Duration

	// MaintenanceInterval is how often the periodic maintenance loop runs
	// to apply SnapshotRetention and InstanceRetention. Only the leader
	// (advisory-lock holder) actually performs the deletes.
	//
	// Default: 1 hour. Has no effect if both retentions are 0.
	MaintenanceInterval time.Duration
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
