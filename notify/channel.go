package notify

import (
	"context"
	"errors"
)

// ErrClosed is returned when operating on a closed channel.
var ErrClosed = errors.New("notify: channel closed")

// Event represents a config sync notification sent between replicas.
type Event struct {
	Action     string `json:"action"` // "sync" or "rollback"
	Collection string `json:"collection"`
	Version    string `json:"version"`
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
