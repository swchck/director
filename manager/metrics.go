package manager

import "time"

// Metrics receives sync-lifecycle telemetry; all methods must be safe for
// concurrent use. LeadershipMetrics and SourceMetrics are optional additions.
type Metrics interface {
	// SyncCompleted is called after a successful leader sync (poll or WS-triggered).
	SyncCompleted(collection string, duration time.Duration, itemCount int)

	// SyncFailed is called when a leader sync fails.
	SyncFailed(collection string, err error)

	// FollowerApplied is called when a follower successfully applies a snapshot.
	FollowerApplied(collection string)

	// FollowerFailed is called when a follower fails to apply a snapshot.
	FollowerFailed(collection string, err error)

	// CacheHit is called when a collection's cache entry is read on startup.
	CacheHit(collection string)

	// CacheMiss is called when a startup cache read misses or errors.
	CacheMiss(collection string)

	// StorageLoaded is called when a collection is loaded from storage on startup.
	StorageLoaded(collection string)

	// WSEventReceived is called when a WebSocket change event is received.
	WSEventReceived(collection string)

	// PreparePhaseStarted is called when the leader begins a 2PC prepare round.
	// Only emitted when Options.RequireUnanimousApply is enabled.
	PreparePhaseStarted(collection, roundID string)

	// PreparePhaseSucceeded is called when all target replicas successfully
	// staged a new version in a 2PC round.
	PreparePhaseSucceeded(collection, roundID string)

	// PreparePhaseFailed is called when a 2PC prepare phase fails. reason is
	// "prepare_failed" (a follower rejected) or "timeout".
	PreparePhaseFailed(collection, roundID, reason string)

	// FollowerPrepared is called when a follower successfully stages a
	// 2PC snapshot.
	FollowerPrepared(collection string)

	// FollowerPrepareFailed is called when a follower fails to stage a
	// 2PC snapshot.
	FollowerPrepareFailed(collection string, err error)

	// StagedDropped is called when a staged 2PC snapshot is discarded before commit.
	// reason is "ttl", "abort", or the reason the leader aborted the round.
	StagedDropped(collection, reason string)

	// ValidationFailed is called when a validator rejects a fetched or staged
	// value. Deduped per (collection, version).
	ValidationFailed(collection string)
}

// SourceMetrics is an optional interface a Metrics may also satisfy; the manager
// discards the call otherwise. See docs/sync-protocol.md "Forward-Only Leader".
type SourceMetrics interface {
	// SourceVersionRegressed is called when a leader cycle declines a source
	// version older than the one held. Not deduped: it is the alerting signal.
	SourceVersionRegressed(collection string)
}

// LeadershipMetrics is an optional interface a Metrics may also satisfy to
// observe advisory-lock transitions; the manager discards the calls otherwise.
type LeadershipMetrics interface {
	// LeaderAcquired is called when this instance acquires the advisory lock
	// for a sync cycle after not holding it on the previous one.
	LeaderAcquired(serviceName string)

	// LeaderLost is called when this instance fails to acquire the advisory
	// lock for a sync cycle after holding it on the previous one.
	LeaderLost(serviceName string)
}

// nopMetrics is the default no-op implementation.
type nopMetrics struct{}

var (
	_ Metrics           = nopMetrics{}
	_ LeadershipMetrics = nopMetrics{}
	_ SourceMetrics     = nopMetrics{}
)

func (nopMetrics) SyncCompleted(string, time.Duration, int)  {}
func (nopMetrics) SyncFailed(string, error)                  {}
func (nopMetrics) FollowerApplied(string)                    {}
func (nopMetrics) FollowerFailed(string, error)              {}
func (nopMetrics) CacheHit(string)                           {}
func (nopMetrics) CacheMiss(string)                          {}
func (nopMetrics) StorageLoaded(string)                      {}
func (nopMetrics) WSEventReceived(string)                    {}
func (nopMetrics) PreparePhaseStarted(string, string)        {}
func (nopMetrics) PreparePhaseSucceeded(string, string)      {}
func (nopMetrics) PreparePhaseFailed(string, string, string) {}
func (nopMetrics) FollowerPrepared(string)                   {}
func (nopMetrics) FollowerPrepareFailed(string, error)       {}
func (nopMetrics) StagedDropped(string, string)              {}
func (nopMetrics) ValidationFailed(string)                   {}
func (nopMetrics) LeaderAcquired(string)                     {}
func (nopMetrics) LeaderLost(string)                         {}
func (nopMetrics) SourceVersionRegressed(string)             {}

// NopMetrics returns a Metrics implementation that discards all telemetry.
func NopMetrics() Metrics {
	return nopMetrics{}
}
