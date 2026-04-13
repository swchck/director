package storage

import (
	"context"
	"errors"
	"time"
)

// Sentinel errors for storage operations.
var (
	ErrSnapshotNotFound = errors.New("storage: snapshot not found")
	ErrLockNotAcquired  = errors.New("storage: lock not acquired")
)

// Status represents the lifecycle state of a config snapshot.
type Status string

const (
	StatusPending  Status = "pending"
	StatusActive   Status = "active"
	StatusInactive Status = "inactive"
	StatusFailed   Status = "failed"
)

// Snapshot represents a stored config snapshot.
type Snapshot struct {
	Collection string
	Version    string
	Content    []byte
	Status     Status
	CreatedAt  time.Time
}

// Storage defines the persistence interface for config snapshots,
// apply logging, and leader election via advisory locks.
type Storage interface {
	// SaveSnapshot persists a new snapshot in pending state.
	SaveSnapshot(ctx context.Context, collection, version string, content []byte) error

	// ActivateSnapshot marks a snapshot as active and demotes any previous active
	// snapshot for the same collection to inactive.
	ActivateSnapshot(ctx context.Context, collection, version string) error

	// GetActiveSnapshot returns the currently active snapshot for a collection.
	// Returns ErrSnapshotNotFound if no active snapshot exists.
	GetActiveSnapshot(ctx context.Context, collection string) (*Snapshot, error)

	// GetSnapshot returns a specific snapshot by collection and version.
	// Returns ErrSnapshotNotFound if it does not exist.
	GetSnapshot(ctx context.Context, collection, version string) (*Snapshot, error)

	// FailSnapshot marks a snapshot as failed.
	FailSnapshot(ctx context.Context, collection, version string) error

	// LogApply records that an instance has applied (or failed to apply) a version.
	LogApply(ctx context.Context, instanceID, collection, version, status string) error

	// CountApplied returns the number of instances that successfully applied a version.
	CountApplied(ctx context.Context, collection, version string) (int, error)

	// AppliedInstances returns the instance IDs that logged the given status
	// for (collection, version). Used by the 2PC protocol to track which
	// specific instances have prepared/committed a version.
	AppliedInstances(ctx context.Context, collection, version, status string) ([]string, error)

	// ResetApplyLog deletes all apply-log rows for (collection, version).
	// Called by the 2PC leader at the start of each round so that stale
	// statuses from a prior aborted round do not leak into the new round's
	// quorum check. The advisory lock serializes leaders, so this is safe.
	ResetApplyLog(ctx context.Context, collection, version string) error

	// AcquireLock attempts to acquire a distributed lock.
	// If acquired, returns a release function. The caller must call release when done.
	// Returns ErrLockNotAcquired if the lock is already held.
	AcquireLock(ctx context.Context, key int64) (release func(), err error)

	// DeleteOldSnapshots removes snapshots created before olderThan, except
	// snapshots in the 'active' status (which are preserved regardless of age
	// so the cluster can always recover the current authoritative version).
	// Apply-log rows for the deleted snapshot versions are removed as well.
	// Returns the number of snapshots deleted.
	DeleteOldSnapshots(ctx context.Context, olderThan time.Time) (int, error)
}
