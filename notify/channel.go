package notify

import (
	"context"
	"errors"
)

// ErrClosed is returned when operating on a closed channel.
var ErrClosed = errors.New("notify: channel closed")

// Event action constants published on the notify channel.
//
// The first two are used by the default eventually-consistent sync protocol.
// The last three are used by the two-phase commit protocol enabled via
// manager.Options.RequireUnanimousApply.
const (
	ActionSync     = "sync"     // eventually-consistent: followers apply a new version
	ActionRollback = "rollback" // eventually-consistent: followers revert to last active
	ActionPrepare  = "prepare"  // 2PC phase 1: followers stage a new version
	ActionCommit   = "commit"   // 2PC phase 2: followers swap staged version live
	ActionAbort    = "abort"    // 2PC: followers discard staged version
)

// Event represents a config sync notification sent between replicas.
//
// RoundID is populated only for 2PC events (prepare/commit/abort) so that
// followers can correlate commit/abort back to the matching prepare.
type Event struct {
	Action     string `json:"action"`
	Collection string `json:"collection"`
	Version    string `json:"version"`
	RoundID    string `json:"round_id,omitempty"`
}

// Channel is the cross-replica notification interface.
// Implementations deliver events to all listening instances.
type Channel interface {
	// Publish sends an event to all subscribers.
	Publish(ctx context.Context, event Event) error

	// Subscribe returns a channel that receives events.
	// The returned channel is closed when the context is cancelled or Close is called.
	Subscribe(ctx context.Context) (<-chan Event, error)

	// Close shuts down the channel and releases resources.
	Close() error
}
