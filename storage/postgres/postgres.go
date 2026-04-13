// Package postgres implements storage.Storage using PostgreSQL via pgx.
package postgres

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/swchck/director/storage"
)

//go:embed migrations/001_init.sql
var MigrationSQL string

// Storage implements storage.Storage using PostgreSQL.
type Storage struct {
	pool *pgxpool.Pool
}

// NewStorage creates a new PostgreSQL-backed Storage.
func NewStorage(pool *pgxpool.Pool) *Storage {
	return &Storage{pool: pool}
}

// Migrate runs the DDL migration to create required tables and indexes.
func (s *Storage) Migrate(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, MigrationSQL); err != nil {
		return fmt.Errorf("storage/postgres: migrate: %w", err)
	}

	return nil
}

// SaveSnapshot inserts a new snapshot in pending state.
// If a snapshot with the same collection+version already exists, this is a no-op.
func (s *Storage) SaveSnapshot(ctx context.Context, collection, version string, content []byte) error {
	const query = `
		INSERT INTO director.config_snapshots (collection_name, version, content, status)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (collection_name, version) DO NOTHING`

	if _, err := s.pool.Exec(ctx, query, collection, version, content, storage.StatusPending); err != nil {
		return fmt.Errorf("storage/postgres: save snapshot %s/%s: %w", collection, version, err)
	}

	return nil
}

// ActivateSnapshot marks the given snapshot as active and demotes any previously
// active snapshot for the same collection to inactive.
func (s *Storage) ActivateSnapshot(ctx context.Context, collection, version string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("storage/postgres: activate snapshot begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is harmless

	const deactivate = `
		UPDATE director.config_snapshots
		SET status = $1
		WHERE collection_name = $2 AND status = $3`

	if _, err := tx.Exec(ctx, deactivate, storage.StatusInactive, collection, storage.StatusActive); err != nil {
		return fmt.Errorf("storage/postgres: deactivate old snapshot %s: %w", collection, err)
	}

	const activate = `
		UPDATE director.config_snapshots
		SET status = $1
		WHERE collection_name = $2 AND version = $3`

	tag, err := tx.Exec(ctx, activate, storage.StatusActive, collection, version)
	if err != nil {
		return fmt.Errorf("storage/postgres: activate snapshot %s/%s: %w", collection, version, err)
	}

	if tag.RowsAffected() == 0 {
		return fmt.Errorf("storage/postgres: activate snapshot %s/%s: %w", collection, version, storage.ErrSnapshotNotFound)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("storage/postgres: activate snapshot commit: %w", err)
	}

	return nil
}

// GetActiveSnapshot returns the currently active snapshot for a collection.
func (s *Storage) GetActiveSnapshot(ctx context.Context, collection string) (*storage.Snapshot, error) {
	const query = `
		SELECT collection_name, version, content, status, created_at
		FROM director.config_snapshots
		WHERE collection_name = $1 AND status = $2`

	snap := &storage.Snapshot{}
	err := s.pool.QueryRow(ctx, query, collection, storage.StatusActive).Scan(
		&snap.Collection, &snap.Version, &snap.Content, &snap.Status, &snap.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("storage/postgres: active snapshot %s: %w", collection, storage.ErrSnapshotNotFound)
		}

		return nil, fmt.Errorf("storage/postgres: active snapshot %s: %w", collection, err)
	}

	return snap, nil
}

// GetSnapshot returns a specific snapshot by collection and version.
func (s *Storage) GetSnapshot(ctx context.Context, collection, version string) (*storage.Snapshot, error) {
	const query = `
		SELECT collection_name, version, content, status, created_at
		FROM director.config_snapshots
		WHERE collection_name = $1 AND version = $2`

	snap := &storage.Snapshot{}
	err := s.pool.QueryRow(ctx, query, collection, version).Scan(
		&snap.Collection, &snap.Version, &snap.Content, &snap.Status, &snap.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("storage/postgres: snapshot %s/%s: %w", collection, version, storage.ErrSnapshotNotFound)
		}

		return nil, fmt.Errorf("storage/postgres: snapshot %s/%s: %w", collection, version, err)
	}

	return snap, nil
}

// FailSnapshot marks a snapshot as failed.
func (s *Storage) FailSnapshot(ctx context.Context, collection, version string) error {
	const query = `
		UPDATE director.config_snapshots
		SET status = $1
		WHERE collection_name = $2 AND version = $3`

	tag, err := s.pool.Exec(ctx, query, storage.StatusFailed, collection, version)
	if err != nil {
		return fmt.Errorf("storage/postgres: fail snapshot %s/%s: %w", collection, version, err)
	}

	if tag.RowsAffected() == 0 {
		return fmt.Errorf("storage/postgres: fail snapshot %s/%s: %w", collection, version, storage.ErrSnapshotNotFound)
	}

	return nil
}

// LogApply records that an instance has applied (or failed to apply) a config version.
// Uses upsert so repeated calls are idempotent.
func (s *Storage) LogApply(ctx context.Context, instanceID, collection, version, status string) error {
	const query = `
		INSERT INTO director.config_apply_log (instance_id, collection_name, version, status)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (instance_id, collection_name, version)
		DO UPDATE SET status = EXCLUDED.status`

	if _, err := s.pool.Exec(ctx, query, instanceID, collection, version, status); err != nil {
		return fmt.Errorf("storage/postgres: log apply %s/%s/%s: %w", instanceID, collection, version, err)
	}

	return nil
}

// CountApplied returns the number of instances that successfully applied a version.
func (s *Storage) CountApplied(ctx context.Context, collection, version string) (int, error) {
	const query = `
		SELECT COUNT(*)
		FROM director.config_apply_log
		WHERE collection_name = $1 AND version = $2 AND status = 'applied'`

	var count int
	if err := s.pool.QueryRow(ctx, query, collection, version).Scan(&count); err != nil {
		return 0, fmt.Errorf("storage/postgres: count applied %s/%s: %w", collection, version, err)
	}

	return count, nil
}

// ResetApplyLog deletes all apply-log rows for (collection, version).
func (s *Storage) ResetApplyLog(ctx context.Context, collection, version string) error {
	const query = `DELETE FROM director.config_apply_log WHERE collection_name = $1 AND version = $2`
	if _, err := s.pool.Exec(ctx, query, collection, version); err != nil {
		return fmt.Errorf("storage/postgres: reset apply log %s/%s: %w", collection, version, err)
	}
	return nil
}

// AppliedInstances returns the instance IDs that logged the given status
// for (collection, version).
func (s *Storage) AppliedInstances(ctx context.Context, collection, version, status string) ([]string, error) {
	const query = `
		SELECT instance_id
		FROM director.config_apply_log
		WHERE collection_name = $1 AND version = $2 AND status = $3
		ORDER BY instance_id`

	rows, err := s.pool.Query(ctx, query, collection, version, status)
	if err != nil {
		return nil, fmt.Errorf("storage/postgres: applied instances %s/%s/%s: %w", collection, version, status, err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("storage/postgres: applied instances scan %s/%s: %w", collection, version, err)
		}
		ids = append(ids, id)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage/postgres: applied instances iter %s/%s: %w", collection, version, err)
	}

	return ids, nil
}

// DeleteOldSnapshots removes snapshots created before olderThan, except those
// in the 'active' status (which are preserved regardless of age so the
// cluster can always recover the current authoritative version).
//
// In the same transaction, apply-log rows for the deleted snapshot versions
// are removed. Returns the number of snapshots deleted.
func (s *Storage) DeleteOldSnapshots(ctx context.Context, olderThan time.Time) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("storage/postgres: delete old snapshots begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is harmless

	const deleteSnapshots = `
		DELETE FROM director.config_snapshots
		WHERE status != $1 AND created_at < $2
		RETURNING collection_name, version`

	rows, err := tx.Query(ctx, deleteSnapshots, storage.StatusActive, olderThan)
	if err != nil {
		return 0, fmt.Errorf("storage/postgres: delete old snapshots: %w", err)
	}

	type cv struct {
		collection string
		version    string
	}
	var deleted []cv
	for rows.Next() {
		var c cv
		if err := rows.Scan(&c.collection, &c.version); err != nil {
			rows.Close()
			return 0, fmt.Errorf("storage/postgres: delete old snapshots scan: %w", err)
		}
		deleted = append(deleted, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("storage/postgres: delete old snapshots iter: %w", err)
	}

	const deleteApplyLog = `
		DELETE FROM director.config_apply_log
		WHERE collection_name = $1 AND version = $2`
	for _, c := range deleted {
		if _, err := tx.Exec(ctx, deleteApplyLog, c.collection, c.version); err != nil {
			return 0, fmt.Errorf("storage/postgres: delete apply log %s/%s: %w", c.collection, c.version, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("storage/postgres: delete old snapshots commit: %w", err)
	}

	return len(deleted), nil
}

// AcquireLock attempts to acquire a Postgres session-level advisory lock.
// Returns a release function if acquired. The caller must call release when done.
// Returns storage.ErrLockNotAcquired if the lock is already held by another session.
func (s *Storage) AcquireLock(ctx context.Context, key int64) (func(), error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("storage/postgres: acquire conn for lock: %w", err)
	}

	var acquired bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&acquired); err != nil {
		conn.Release()
		return nil, fmt.Errorf("storage/postgres: try advisory lock %d: %w", key, err)
	}

	if !acquired {
		conn.Release()
		return nil, storage.ErrLockNotAcquired
	}

	release := func() {
		// Best-effort unlock; connection release handles cleanup regardless.
		_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", key)
		conn.Release()
	}

	return release, nil
}
