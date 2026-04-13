package registry

import (
	"context"
	"errors"
	"time"
)

// ErrInstanceNotFound is returned when an instance is not found in the registry.
var ErrInstanceNotFound = errors.New("registry: instance not found")

// Registry tracks live service instances for coordinating config sync across replicas.
type Registry interface {
	// Register adds an instance to the registry.
	Register(ctx context.Context, instanceID, serviceName string) error

	// Heartbeat updates the last-seen timestamp for an instance.
	Heartbeat(ctx context.Context, instanceID string) error

	// Deregister removes an instance from the registry.
	Deregister(ctx context.Context, instanceID string) error

	// AliveCount returns the number of instances with a recent heartbeat
	// for the given service name.
	AliveCount(ctx context.Context, serviceName string) (int, error)

	// AliveInstances returns the instance IDs with a recent heartbeat for
	// the given service name. Used by the 2PC sync protocol to build a
	// stable target set for a sync round (unlike AliveCount, which only
	// returns a number).
	AliveInstances(ctx context.Context, serviceName string) ([]string, error)

	// DeleteStaleInstances removes instance rows whose last_heartbeat is
	// older than olderThan, regardless of service. Used by the manager's
	// periodic maintenance loop to garbage-collect dead replicas that
	// crashed without calling Deregister. Returns the number of rows
	// deleted. olderThan should be set well above the heartbeat interval
	// to avoid pruning live instances during transient delays.
	DeleteStaleInstances(ctx context.Context, olderThan time.Time) (int, error)
}
