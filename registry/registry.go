package registry

import (
	"context"
	"errors"
	"time"
)

// ErrInstanceNotFound is returned when an instance is not found in the registry.
var ErrInstanceNotFound = errors.New("registry: instance not found")

// Registry tracks live service instances for coordinating config sync across replicas.
//
// "Alive" means a heartbeat newer than an implementation-defined stale threshold
// (registry/postgres: 30s, see WithStaleThreshold). A replica that dies without
// deregistering therefore leaves the alive set within that window instead of
// blocking a 2PC round forever.
type Registry interface {
	// Register adds an instance to the registry.
	Register(ctx context.Context, instanceID, serviceName string) error

	// Heartbeat refreshes the last-seen timestamp of a registered instance.
	// It is an update, not an upsert: implementations return ErrInstanceNotFound
	// when the row is gone, leaving the choice to re-Register to the caller.
	Heartbeat(ctx context.Context, instanceID string) error

	// Deregister removes an instance from the registry.
	Deregister(ctx context.Context, instanceID string) error

	// AliveCount returns the number of alive instances of a service.
	AliveCount(ctx context.Context, serviceName string) (int, error)

	// AliveInstances returns the IDs of the alive instances of a service.
	// The 2PC protocol needs the IDs, not a count, to hold a target set stable
	// across a round.
	AliveInstances(ctx context.Context, serviceName string) ([]string, error)

	// DeleteStaleInstances removes instance rows last seen before olderThan,
	// regardless of service, and returns how many were deleted. It garbage-collects
	// replicas that crashed without deregistering, so olderThan must sit well above
	// the heartbeat interval — a transient delay must not prune a live instance.
	DeleteStaleInstances(ctx context.Context, olderThan time.Time) (int, error)
}
