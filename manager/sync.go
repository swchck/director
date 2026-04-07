package manager

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/swchck/director/cache"
	"github.com/swchck/director/config"
	"github.com/swchck/director/directus"
	dlog "github.com/swchck/director/log"
	"github.com/swchck/director/notify"
	"github.com/swchck/director/storage"
)

// syncAll runs one sync cycle for all registered configs.
// If this instance holds the advisory lock, it acts as leader; otherwise follower.
func (m *Manager) syncAll(ctx context.Context) {
	release, err := m.storage.AcquireLock(ctx, m.opts.AdvisoryLockKey)
	if err != nil {
		if errors.Is(err, storage.ErrLockNotAcquired) {
			// Another instance is leader — nothing to do, follower reacts to notifications.
			return
		}

		m.logger.Error("manager: acquire lock failed", dlog.Err(err))
		return
	}
	defer release()

	for _, reg := range m.configs {
		if err := m.leaderSync(ctx, reg, false); err != nil {
			m.metrics.SyncFailed(reg.name(), err)
			m.logger.Error("manager: leader sync failed", dlog.Err(err), dlog.String("collection", reg.name()))
		}
	}
}

// leaderSync performs the full leader sync protocol for a single config.
// When force is true (e.g. triggered by WebSocket), the version check is skipped
// and zero timestamps fall back to time.Now().
func (m *Manager) leaderSync(ctx context.Context, reg registrable, force bool) error {
	collection := reg.name()
	syncStart := time.Now()

	// 1. Check version.
	updatedAt, err := reg.fetchVersion(ctx)
	if err != nil {
		return fmt.Errorf("fetch version: %w", err)
	}

	if force && updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}

	newVersion := config.NewVersion(updatedAt)
	currentVersion := reg.version()

	if !force && !currentVersion.IsZero() && newVersion.Equal(currentVersion) {
		m.logger.Debug("manager: no version change, skipping sync",
			dlog.String("collection", collection),
			dlog.String("version", currentVersion.String()),
		)
		return nil // no change
	}

	m.logger.Info("manager: version change detected",
		dlog.String("collection", collection),
		dlog.String("old_version", currentVersion.String()),
		dlog.String("new_version", newVersion.String()),
		dlog.Bool("forced", force),
	)

	// 2. Fetch all items and swap locally.
	content, err := reg.fetchAndSwap(ctx, newVersion)
	if err != nil {
		return fmt.Errorf("fetch and swap: %w", err)
	}

	m.logger.Debug("manager: fetched and swapped data",
		dlog.String("collection", collection),
		dlog.Int("content_bytes", len(content)),
		dlog.String("version", newVersion.String()),
	)

	// 3. Save snapshot to storage.
	if err := m.storage.SaveSnapshot(ctx, collection, newVersion.String(), content); err != nil {
		return fmt.Errorf("save snapshot: %w", err)
	}

	// 4. Write to cache if strategy requires it.
	m.cacheWrite(ctx, collection, newVersion.String(), content)

	// 5. Log own apply.
	if err := m.storage.LogApply(ctx, m.instanceID, collection, newVersion.String(), "applied"); err != nil {
		return fmt.Errorf("log apply: %w", err)
	}

	// 6. Notify other replicas.
	event := notify.Event{
		Action:     "sync",
		Collection: collection,
		Version:    newVersion.String(),
	}

	if err := m.notifier.Publish(ctx, event); err != nil {
		return fmt.Errorf("publish sync event: %w", err)
	}

	// 7. Wait for confirmations from other replicas.
	if err := m.waitConfirmations(ctx, collection, newVersion.String()); err != nil {
		m.logger.Warn("manager: confirmations timed out, activating anyway", dlog.Err(err), dlog.String("collection", collection))
	}

	// 8. Activate snapshot.
	if err := m.storage.ActivateSnapshot(ctx, collection, newVersion.String()); err != nil {
		return fmt.Errorf("activate snapshot: %w", err)
	}

	m.metrics.SyncCompleted(collection, time.Since(syncStart), len(content))

	m.logger.Info("manager: sync complete",
		dlog.String("collection", collection),
		dlog.String("version", newVersion.String()),
	)

	return nil
}

// cacheWrite writes an entry to cache if the strategy requires it.
func (m *Manager) cacheWrite(ctx context.Context, collection, version string, content []byte) {
	if m.cache == nil || !m.cacheStrategy.WritesToCache() {
		return
	}

	cacheEntry := cache.Entry{
		Collection: collection,
		Version:    version,
		Content:    content,
	}

	if m.cacheStrategy.IsAsync() {
		go func() { //nolint:gosec // intentional: async write must outlive request context
			if cacheErr := m.cache.Set(context.Background(), cacheEntry); cacheErr != nil {
				m.logger.Error("manager: async cache write failed", dlog.Err(cacheErr), dlog.String("collection", collection))
			}
		}()
	} else {
		if cacheErr := m.cache.Set(ctx, cacheEntry); cacheErr != nil {
			m.logger.Warn("manager: cache write failed", dlog.Err(cacheErr), dlog.String("collection", collection))
		}
	}
}

// waitConfirmations polls the apply log until all alive replicas have confirmed
// or the timeout expires.
func (m *Manager) waitConfirmations(ctx context.Context, collection, version string) error {
	deadline := time.After(m.opts.WaitConfirmationsTimeout)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-deadline:
			return fmt.Errorf("timeout waiting for confirmations")

		case <-ticker.C:
			applied, err := m.storage.CountApplied(ctx, collection, version)
			if err != nil {
				return fmt.Errorf("count applied: %w", err)
			}

			alive, err := m.registry.AliveCount(ctx, m.opts.ServiceName)
			if err != nil {
				return fmt.Errorf("alive count: %w", err)
			}

			if applied >= alive {
				return nil
			}
		}
	}
}

// handleEvent processes an incoming notification as a follower.
func (m *Manager) handleEvent(ctx context.Context, event notify.Event) {
	m.logger.Debug("manager: received notify event",
		dlog.String("action", event.Action),
		dlog.String("collection", event.Collection),
		dlog.String("version", event.Version),
	)

	switch event.Action {
	case "sync":
		m.handleSyncEvent(ctx, event)
	case "rollback":
		m.handleRollbackEvent(ctx, event)
	default:
		m.logger.Warn("manager: unknown event action", dlog.String("action", event.Action))
	}
}

// handleSyncEvent loads a snapshot from storage and applies it locally.
func (m *Manager) handleSyncEvent(ctx context.Context, event notify.Event) {
	reg, ok := m.configs[event.Collection]
	if !ok {
		return
	}

	version, err := config.ParseVersion(event.Version)
	if err != nil {
		m.logger.Error("manager: parse version", dlog.Err(err), dlog.String("version", event.Version))
		return
	}

	// Skip if already at this version.
	if reg.version().Equal(version) {
		return
	}

	snap, err := m.storage.GetSnapshot(ctx, event.Collection, event.Version)
	if err != nil {
		m.metrics.FollowerFailed(event.Collection, err)
		m.logger.Error("manager: get snapshot for follower sync",
			dlog.Err(err),
			dlog.String("collection", event.Collection),
			dlog.String("version", event.Version),
		)
		m.logApplyStatus(ctx, event.Collection, event.Version, "error")
		return
	}

	if err := reg.swapFromBytes(version, snap.Content); err != nil {
		m.metrics.FollowerFailed(event.Collection, err)
		m.logger.Error("manager: swap from snapshot",
			dlog.Err(err),
			dlog.String("collection", event.Collection),
			dlog.String("version", event.Version),
		)
		m.logApplyStatus(ctx, event.Collection, event.Version, "error")
		return
	}

	m.logApplyStatus(ctx, event.Collection, event.Version, "applied")
	m.metrics.FollowerApplied(event.Collection)

	m.logger.Info("manager: follower applied sync",
		dlog.String("collection", event.Collection),
		dlog.String("version", event.Version),
	)
}

// handleRollbackEvent loads the current active snapshot and reverts to it.
func (m *Manager) handleRollbackEvent(ctx context.Context, event notify.Event) {
	reg, ok := m.configs[event.Collection]
	if !ok {
		return
	}

	snap, err := m.storage.GetActiveSnapshot(ctx, event.Collection)
	if err != nil {
		m.logger.Error("manager: get active snapshot for rollback",
			dlog.Err(err),
			dlog.String("collection", event.Collection),
		)
		return
	}

	version, err := config.ParseVersion(snap.Version)
	if err != nil {
		m.logger.Error("manager: parse rollback version", dlog.Err(err), dlog.String("version", snap.Version))
		return
	}

	if err := reg.swapFromBytes(version, snap.Content); err != nil {
		m.logger.Error("manager: rollback swap",
			dlog.Err(err),
			dlog.String("collection", event.Collection),
			dlog.String("version", snap.Version),
		)
		return
	}

	m.logger.Info("manager: rolled back to active snapshot",
		dlog.String("collection", event.Collection),
		dlog.String("version", snap.Version),
	)
}

// loadFromCache attempts to load configs from cache on startup.
// Returns true if at least one config was loaded.
func (m *Manager) loadFromCache(ctx context.Context) bool {
	if m.cache == nil || !m.cacheStrategy.ReadsFromCache() {
		return false
	}

	loaded := false

	for _, reg := range m.configs {
		entry, err := m.cache.Get(ctx, reg.name())
		if err != nil {
			m.metrics.CacheMiss(reg.name())
			if !errors.Is(err, cache.ErrCacheMiss) {
				m.logger.Warn("manager: cache read failed", dlog.Err(err), dlog.String("collection", reg.name()))
			}
			continue
		}
		m.metrics.CacheHit(reg.name())

		version, err := config.ParseVersion(entry.Version)
		if err != nil {
			m.logger.Warn("manager: parse cached version", dlog.Err(err), dlog.String("collection", reg.name()))
			continue
		}

		if err := reg.swapFromBytes(version, entry.Content); err != nil {
			m.logger.Warn("manager: swap from cache", dlog.Err(err), dlog.String("collection", reg.name()))
			continue
		}

		m.logger.Info("manager: loaded from cache",
			dlog.String("collection", reg.name()),
			dlog.String("version", entry.Version),
		)

		loaded = true
	}

	return loaded
}

// loadFromStorage attempts to load configs from active snapshots in storage.
func (m *Manager) loadFromStorage(ctx context.Context) {
	for _, reg := range m.configs {
		snap, err := m.storage.GetActiveSnapshot(ctx, reg.name())
		if err != nil {
			if !errors.Is(err, storage.ErrSnapshotNotFound) {
				m.logger.Warn("manager: load from storage", dlog.Err(err), dlog.String("collection", reg.name()))
			}
			continue
		}

		version, err := config.ParseVersion(snap.Version)
		if err != nil {
			m.logger.Warn("manager: parse stored version", dlog.Err(err), dlog.String("collection", reg.name()))
			continue
		}

		// Skip if already loaded (e.g. from cache) with a newer version.
		if !reg.version().IsZero() && !version.After(reg.version()) {
			continue
		}

		if err := reg.swapFromBytes(version, snap.Content); err != nil {
			m.logger.Warn("manager: swap from storage", dlog.Err(err), dlog.String("collection", reg.name()))
			continue
		}

		m.metrics.StorageLoaded(reg.name())

		m.logger.Info("manager: loaded from storage",
			dlog.String("collection", reg.name()),
			dlog.String("version", snap.Version),
		)
	}
}

// handleWSChange processes a Directus WebSocket change event.
// Only the leader fetches from Directus; followers receive data via the notify channel.
func (m *Manager) handleWSChange(ctx context.Context, change directus.ChangeEvent) {
	m.metrics.WSEventReceived(change.Collection)

	m.logger.Debug("manager: websocket change received",
		dlog.String("collection", change.Collection),
		dlog.String("action", change.Action),
	)

	reg, ok := m.configs[change.Collection]
	if !ok {
		return
	}

	m.syncOneForced(ctx, reg)
}

// syncOneForced runs the leader sync protocol skipping the version check.
// Used by the WS handler: the WebSocket already told us something changed,
// so we skip the version comparison and always do a full fetch.
func (m *Manager) syncOneForced(ctx context.Context, reg registrable) {
	release, err := m.storage.AcquireLock(ctx, m.opts.AdvisoryLockKey)
	if err != nil {
		if errors.Is(err, storage.ErrLockNotAcquired) {
			return
		}

		m.logger.Error("manager: acquire lock for ws sync failed", dlog.Err(err))
		return
	}
	defer release()

	if err := m.leaderSync(ctx, reg, true); err != nil {
		m.metrics.SyncFailed(reg.name(), err)
		m.logger.Error("manager: ws-triggered sync failed", dlog.Err(err), dlog.String("collection", reg.name()))
	}
}

func (m *Manager) logApplyStatus(ctx context.Context, collection, version, status string) {
	if err := m.storage.LogApply(ctx, m.instanceID, collection, version, status); err != nil {
		m.logger.Error("manager: log apply failed",
			dlog.Err(err),
			dlog.String("collection", collection),
			dlog.String("version", version),
			dlog.String("status", status),
		)
	}
}
