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

// NopMetrics returns a Metrics implementation that discards all telemetry.
func NopMetrics() Metrics {
	return nopMetrics{}
}
