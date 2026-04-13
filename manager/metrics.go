package manager

import "time"

// Metrics receives telemetry from the sync lifecycle.
// Implement this interface to export metrics to Prometheus, OpenTelemetry, StatsD, etc.
//
// All methods must be safe for concurrent use.
// A no-op implementation is used when no Metrics is configured.
//
// Example with Prometheus:
//
//	type promMetrics struct {
//	    syncTotal    *prometheus.CounterVec
//	    syncDuration *prometheus.HistogramVec
//	    syncErrors   *prometheus.CounterVec
//	    cacheHits    prometheus.Counter
//	    cacheMisses  prometheus.Counter
//	}
//
//	func (m *promMetrics) SyncCompleted(collection string, duration time.Duration, itemCount int) {
//	    m.syncTotal.WithLabelValues(collection).Inc()
//	    m.syncDuration.WithLabelValues(collection).Observe(duration.Seconds())
//	}
type Metrics interface {
	// SyncCompleted is called after a successful leader sync (poll or WS-triggered).
	SyncCompleted(collection string, duration time.Duration, itemCount int)

	// SyncFailed is called when a leader sync fails.
	SyncFailed(collection string, err error)

	// FollowerApplied is called when a follower successfully applies a snapshot.
	FollowerApplied(collection string)

	// FollowerFailed is called when a follower fails to apply a snapshot.
	FollowerFailed(collection string, err error)

	// CacheHit is called when a collection is loaded from cache on startup.
	CacheHit(collection string)

	// CacheMiss is called when a collection is not found in cache on startup.
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

	// PreparePhaseFailed is called when a 2PC round aborts — either because
	// a follower returned prepare_failed or because the prepare timeout
	// elapsed. reason is "prepare_failed" or "timeout".
	PreparePhaseFailed(collection, roundID, reason string)

	// FollowerPrepared is called when a follower successfully stages a
	// 2PC snapshot.
	FollowerPrepared(collection string)

	// FollowerPrepareFailed is called when a follower fails to stage a
	// 2PC snapshot.
	FollowerPrepareFailed(collection string, err error)

	// StagedDropped is called when a staged 2PC snapshot is discarded
	// before commit — via abort, TTL expiry, or explicit drop.
	StagedDropped(collection, reason string)
}

// nopMetrics is the default no-op implementation.
type nopMetrics struct{}

func (nopMetrics) SyncCompleted(string, time.Duration, int) {}
func (nopMetrics) SyncFailed(string, error)                 {}
func (nopMetrics) FollowerApplied(string)                   {}
func (nopMetrics) FollowerFailed(string, error)             {}
func (nopMetrics) CacheHit(string)                          {}
func (nopMetrics) CacheMiss(string)                         {}
func (nopMetrics) StorageLoaded(string)                     {}
func (nopMetrics) WSEventReceived(string)                   {}
func (nopMetrics) PreparePhaseStarted(string, string)       {}
func (nopMetrics) PreparePhaseSucceeded(string, string)     {}
func (nopMetrics) PreparePhaseFailed(string, string, string) {}
func (nopMetrics) FollowerPrepared(string)                  {}
func (nopMetrics) FollowerPrepareFailed(string, error)      {}
func (nopMetrics) StagedDropped(string, string)             {}

// NopMetrics returns a Metrics implementation that discards all telemetry.
func NopMetrics() Metrics {
	return nopMetrics{}
}
