// Package postgres implements registry.Registry using PostgreSQL.
package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/swchck/director/registry"
)

const defaultStaleThreshold = 30 * time.Second

// Registry implements registry.Registry using the director.config_instances table.
type Registry struct {
	pool           *pgxpool.Pool
	staleThreshold time.Duration
}

// Option configures a Registry.
type Option func(*Registry)

// WithStaleThreshold sets the duration after which an instance without
// a heartbeat is considered dead. Default is 30 seconds.
func WithStaleThreshold(d time.Duration) Option {
	return func(r *Registry) {
		r.staleThreshold = d
	}
}

// NewRegistry creates a new PostgreSQL-backed Registry.
func NewRegistry(pool *pgxpool.Pool, opts ...Option) *Registry {
	r := &Registry{
		pool:           pool,
		staleThreshold: defaultStaleThreshold,
	}

	for _, opt := range opts {
		opt(r)
	}

	return r
}

// Register adds or re-registers an instance.
func (r *Registry) Register(ctx context.Context, instanceID, serviceName string) error {
	const query = `
		INSERT INTO director.config_instances (instance_id, service_name, last_heartbeat, started_at)
		VALUES ($1, $2, NOW(), NOW())
		ON CONFLICT (instance_id)
		DO UPDATE SET service_name = EXCLUDED.service_name,
		              last_heartbeat = NOW(),
		              started_at = NOW()`

	if _, err := r.pool.Exec(ctx, query, instanceID, serviceName); err != nil {
		return fmt.Errorf("registry/postgres: register %s: %w", instanceID, err)
	}

	return nil
}

// Heartbeat updates the last-seen timestamp.
func (r *Registry) Heartbeat(ctx context.Context, instanceID string) error {
	const query = `
		UPDATE director.config_instances
		SET last_heartbeat = NOW()
		WHERE instance_id = $1`

	tag, err := r.pool.Exec(ctx, query, instanceID)
	if err != nil {
		return fmt.Errorf("registry/postgres: heartbeat %s: %w", instanceID, err)
	}

	if tag.RowsAffected() == 0 {
		return fmt.Errorf("registry/postgres: heartbeat %s: %w", instanceID, registry.ErrInstanceNotFound)
	}

	return nil
}

// Deregister removes an instance from the registry.
func (r *Registry) Deregister(ctx context.Context, instanceID string) error {
	const query = `DELETE FROM director.config_instances WHERE instance_id = $1`

	if _, err := r.pool.Exec(ctx, query, instanceID); err != nil {
		return fmt.Errorf("registry/postgres: deregister %s: %w", instanceID, err)
	}

	return nil
}

// AliveCount returns the number of instances with a heartbeat newer than
// the stale threshold.
func (r *Registry) AliveCount(ctx context.Context, serviceName string) (int, error) {
	const query = `
		SELECT COUNT(*)
		FROM director.config_instances
		WHERE service_name = $1 AND last_heartbeat > NOW() - $2::interval`

	interval := fmt.Sprintf("%.0f seconds", r.staleThreshold.Seconds())

	var count int
	if err := r.pool.QueryRow(ctx, query, serviceName, interval).Scan(&count); err != nil {
		return 0, fmt.Errorf("registry/postgres: alive count %s: %w", serviceName, err)
	}

	return count, nil
}

// DeleteStaleInstances removes instance rows whose last_heartbeat is older
// than olderThan, regardless of service. Returns the number of rows deleted.
func (r *Registry) DeleteStaleInstances(ctx context.Context, olderThan time.Time) (int, error) {
	const query = `DELETE FROM director.config_instances WHERE last_heartbeat < $1`

	tag, err := r.pool.Exec(ctx, query, olderThan)
	if err != nil {
		return 0, fmt.Errorf("registry/postgres: delete stale instances: %w", err)
	}

	return int(tag.RowsAffected()), nil
}

// AliveInstances returns the instance IDs with a heartbeat newer than the
// stale threshold for the given service name.
func (r *Registry) AliveInstances(ctx context.Context, serviceName string) ([]string, error) {
	const query = `
		SELECT instance_id
		FROM director.config_instances
		WHERE service_name = $1 AND last_heartbeat > NOW() - $2::interval
		ORDER BY instance_id`

	interval := fmt.Sprintf("%.0f seconds", r.staleThreshold.Seconds())

	rows, err := r.pool.Query(ctx, query, serviceName, interval)
	if err != nil {
		return nil, fmt.Errorf("registry/postgres: alive instances %s: %w", serviceName, err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("registry/postgres: alive instances scan %s: %w", serviceName, err)
		}
		ids = append(ids, id)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("registry/postgres: alive instances iter %s: %w", serviceName, err)
	}

	return ids, nil
}
