package registry

import (
	"context"
	"errors"
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
}
