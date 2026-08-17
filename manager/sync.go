package manager

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/swchck/director/cache"
	"github.com/swchck/director/config"
	"github.com/swchck/director/directus"
	dlog "github.com/swchck/director/log"
	"github.com/swchck/director/notify"
	"github.com/swchck/director/storage"
)

// errPrepareFailed signals that at least one follower reported prepare_failed
// during the 2PC prepare phase, so the round must abort.
var errPrepareFailed = errors.New("manager: prepare phase failed")

// ErrActiveSnapshotBehind reports that a follower refused an active snapshot naming
// an older version than it holds. Surfaced via Metrics.FollowerFailed, so alertable.
var ErrActiveSnapshotBehind = errors.New("manager: active snapshot is behind the local version")

// Dedup kinds for the per-version report slots on a registration. Distinct
// values because the conditions are independent and must not silence each other.
const (
	activeRegressionKind = "active_regression"
	sourceRegressionKind = "source_regression"
)

// Names for the code path that refused a backward move, logged as "path".
const (
	pathCatchUp   = "catch_up"
	pathSyncEvent = "sync_event"
)

// rollbackApplyStatus is written to the apply log by every replica that applies
// an operator rollback, so the rollback can be verified with one query.
const rollbackApplyStatus = "rolled_back"

// syncAll runs one sync cycle for all registered configs, as leader if this instance
// holds the advisory lock. Reports whether it acted as leader.
func (m *Manager) syncAll(ctx context.Context) bool {
	prevLeader := m.isLeader.Load()

	release, err := m.storage.AcquireLock(ctx, m.opts.AdvisoryLockKey)
	if err != nil {
		if errors.Is(err, storage.ErrLockNotAcquired) {
			// Another instance is leader; this one reacts to its notifications.
			m.isLeader.Store(false)
			if prevLeader {
				m.leaderMetrics.LeaderLost(m.opts.ServiceName)
			}
			return false
		}

		m.logger.Error("manager: acquire lock failed", dlog.Err(err))
		m.isLeader.Store(false)
		if prevLeader {
			m.leaderMetrics.LeaderLost(m.opts.ServiceName)
		}
		return false
	}
	m.isLeader.Store(true)
	if !prevLeader {
		m.leaderMetrics.LeaderAcquired(m.opts.ServiceName)
	}
	defer release()

	for _, reg := range m.configs {
		err := m.leaderSync(ctx, reg, false)
		m.recordSync(reg.name(), err)
		if err != nil {
			m.metrics.SyncFailed(reg.name(), err)
			if errors.Is(err, ErrValidationFailed) {
				// fetchAndStage already deduped and warn-logged this; suppress
				// the generic error so a chronically-bad version cannot spam.
				continue
			}
			m.logger.Error("manager: leader sync failed", dlog.Err(err), dlog.String("collection", reg.name()))
		}
	}

	return true
}

// followerCatchUp applies the active snapshot wherever the local version is behind,
// repairing a lost notification. Forward only — see allowForward.
func (m *Manager) followerCatchUp(ctx context.Context) {
	for _, reg := range m.configs {
		activeVersion, err := m.getActiveVersion(ctx, reg.name())
		if err != nil {
			if errors.Is(err, storage.ErrSnapshotNotFound) {
				continue
			}
			m.logger.Error("manager: follower catch-up version check",
				dlog.Err(err), dlog.String("collection", reg.name()))
			continue
		}

		parsedVersion, err := config.ParseVersion(activeVersion)
		if err != nil {
			continue
		}

		if reg.version().Equal(parsedVersion) {
			continue
		}

		// Checked before the content read so a stale active version costs one
		// version query per tick.
		if !m.allowForward(reg, parsedVersion, pathCatchUp) {
			continue
		}

		m.logger.Info("manager: follower catch-up detected stale version",
			dlog.String("collection", reg.name()),
			dlog.String("local", reg.version().String()),
			dlog.String("active", activeVersion),
		)

		snap, err := m.storage.GetActiveSnapshot(ctx, reg.name())
		if err != nil {
			m.logger.Error("manager: follower catch-up get snapshot",
				dlog.Err(err), dlog.String("collection", reg.name()))
			continue
		}

		version, err := config.ParseVersion(snap.Version)
		if err != nil {
			continue
		}

		// Two separate queries, so re-check what is actually about to be applied.
		if !m.allowForward(reg, version, pathCatchUp) {
			continue
		}

		if err := reg.swapFromBytes(version, snap.Content); err != nil {
			m.logger.Error("manager: follower catch-up swap",
				dlog.Err(err), dlog.String("collection", reg.name()))
			continue
		}

		m.logApplyStatus(ctx, reg.name(), snap.Version, "applied")
		m.metrics.FollowerApplied(reg.name())

		m.logger.Info("manager: follower catch-up applied",
			dlog.String("collection", reg.name()),
			dlog.String("version", snap.Version),
		)
	}
}

// allowForward reports whether a follower path may move reg onto ver — forward only,
// but a zero local version always passes. docs/sync-protocol.md "Follower Catch-Up".
func (m *Manager) allowForward(reg registrable, ver config.Version, path string) bool {
	local := reg.version()
	if local.IsZero() || ver.After(local) {
		return true
	}

	m.metrics.FollowerFailed(reg.name(), ErrActiveSnapshotBehind)

	fields := []dlog.Field{
		dlog.String("collection", reg.name()),
		dlog.String("local", local.String()),
		dlog.String("active", ver.String()),
		dlog.String("path", path),
	}

	// One warn per active version: the condition persists across reconcile ticks.
	// The metric above is deliberately not deduped — it is the alerting signal.
	if reg.shouldReport(ver, activeRegressionKind) {
		m.logger.Warn("manager: refusing to move to an older active version", fields...)
	} else {
		m.logger.Debug("manager: refusing to move to an older active version (dedup)", fields...)
	}

	return false
}

// syncVersion resolves the version a leader cycle moves to: a forced cycle mints one
// from the clock rather than move backwards, since the content is new either way.
func syncVersion(reported time.Time, local config.Version, force bool) config.Version {
	ver := config.NewVersion(reported)
	if force && !ver.After(local) {
		return config.NewVersion(time.Now().UTC())
	}

	return ver
}

// allowLeaderAdvance reports whether a leader cycle may move reg onto ver — forward
// only, zero local excepted. docs/sync-protocol.md "Forward-Only Leader".
func (m *Manager) allowLeaderAdvance(reg registrable, ver config.Version) bool {
	local := reg.version()
	if local.IsZero() || ver.After(local) {
		return true
	}

	m.sourceMetrics.SourceVersionRegressed(reg.name())

	fields := []dlog.Field{
		dlog.String("collection", reg.name()),
		dlog.String("local", local.String()),
		dlog.String("reported", ver.String()),
	}

	// One warn per reported version: an unchanged source repeats it every poll.
	if reg.shouldReport(ver, sourceRegressionKind) {
		m.logger.Warn("manager: declining to sync a source version behind the one held", fields...)
	} else {
		m.logger.Debug("manager: declining to sync a source version behind the one held (dedup)", fields...)
	}

	return false
}

// getActiveVersion returns the active snapshot version, via the cheap
// ActiveVersionChecker when storage implements it, else a full snapshot load.
func (m *Manager) getActiveVersion(ctx context.Context, collection string) (string, error) {
	if vc, ok := m.storage.(storage.ActiveVersionChecker); ok {
		return vc.GetActiveVersion(ctx, collection)
	}

	snap, err := m.storage.GetActiveSnapshot(ctx, collection)
	if err != nil {
		return "", err
	}

	return snap.Version, nil
}

// runMaintenance deletes data past the retentions. Leader only, so followers do not
// stampede the same deletes — they see ErrLockNotAcquired and stop.
func (m *Manager) runMaintenance(ctx context.Context) {
	if m.opts.SnapshotRetention <= 0 && m.opts.InstanceRetention <= 0 {
		return
	}

	release, err := m.storage.AcquireLock(ctx, m.opts.AdvisoryLockKey)
	if err != nil {
		if errors.Is(err, storage.ErrLockNotAcquired) {
			return
		}
		m.logger.Error("manager: maintenance acquire lock", dlog.Err(err))
		return
	}
	defer release()

	now := time.Now()

	if m.opts.SnapshotRetention > 0 {
		cutoff := now.Add(-m.opts.SnapshotRetention)
		deleted, err := m.storage.DeleteOldSnapshots(ctx, cutoff)
		if err != nil {
			m.logger.Error("manager: delete old snapshots",
				dlog.Err(err),
				dlog.String("cutoff", cutoff.Format(time.RFC3339)),
			)
		} else if deleted > 0 {
			m.logger.Info("manager: deleted old snapshots",
				dlog.Int("count", deleted),
				dlog.String("cutoff", cutoff.Format(time.RFC3339)),
			)
		}
	}

	if m.opts.InstanceRetention > 0 {
		cutoff := now.Add(-m.opts.InstanceRetention)
		deleted, err := m.registry.DeleteStaleInstances(ctx, cutoff)
		if err != nil {
			m.logger.Error("manager: delete stale instances",
				dlog.Err(err),
				dlog.String("cutoff", cutoff.Format(time.RFC3339)),
			)
		} else if deleted > 0 {
			m.logger.Info("manager: deleted stale instances",
				dlog.Int("count", deleted),
				dlog.String("cutoff", cutoff.Format(time.RFC3339)),
			)
		}
	}
}

// leaderSync runs the leader protocol for one config; force skips the version check
// (see syncVersion). Dispatches to leaderSync2PC under RequireUnanimousApply.
func (m *Manager) leaderSync(ctx context.Context, reg registrable, force bool) error {
	if m.opts.RequireUnanimousApply {
		return m.leaderSync2PC(ctx, reg, force)
	}

	collection := reg.name()
	syncStart := time.Now()

	// 1. Check version.
	updatedAt, err := reg.fetchVersion(ctx)
	if err != nil {
		return fmt.Errorf("fetch version: %w", err)
	}

	currentVersion := reg.version()
	newVersion := syncVersion(updatedAt, currentVersion, force)

	if !force && !currentVersion.IsZero() && newVersion.Equal(currentVersion) {
		m.repairCacheEntry(ctx, collection)
		m.logger.Debug("manager: no version change, skipping sync",
			dlog.String("collection", collection),
			dlog.String("version", currentVersion.String()),
		)
		return nil
	}

	if !m.allowLeaderAdvance(reg, newVersion) {
		return nil
	}

	m.logger.Info("manager: version change detected",
		dlog.String("collection", collection),
		dlog.String("old_version", currentVersion.String()),
		dlog.String("new_version", newVersion.String()),
		dlog.Bool("forced", force),
	)

	version := newVersion.String()

	// 2. Fetch and stage without applying: the in-memory version must not run ahead
	// of the active snapshot. TTL 0 — only this function commits or drops it.
	content, staged, err := reg.fetchAndStage(ctx, newVersion, uuid.NewString(), 0)
	if err != nil {
		return fmt.Errorf("fetch and stage: %w", err)
	}

	m.logger.Debug("manager: fetched and staged data",
		dlog.String("collection", collection),
		dlog.Int("content_bytes", len(content)),
		dlog.String("version", version),
	)

	// 3. Save snapshot to storage.
	if err := m.storage.SaveSnapshot(ctx, collection, version, content); err != nil {
		reg.abortStaged(staged)
		return fmt.Errorf("save snapshot: %w", err)
	}

	// 4. Activate before announcing or applying: replicas reconcile against the
	// active snapshot, so a newer in-memory version would stall the collection.
	if err := m.storage.ActivateSnapshot(ctx, collection, version); err != nil {
		reg.abortStaged(staged)
		return fmt.Errorf("activate snapshot: %w", err)
	}

	// 5. Apply locally.
	if err := m.applyStaged(reg, staged, newVersion); err != nil {
		return fmt.Errorf("apply staged value: %w", err)
	}

	// 6. Notify other replicas.
	event := notify.Event{
		Action:     notify.ActionSync,
		Collection: collection,
		Version:    version,
	}

	if err := m.publish(ctx, event); err != nil {
		return fmt.Errorf("publish sync event: %w", err)
	}

	// 7. Write to cache if strategy requires it.
	m.cacheWrite(ctx, collection, version, content)

	// 8. Log own apply. The value is live and the announcement is out, so a failed
	// write loses a diagnostic row, not the sync — as in the 2PC path.
	m.logApplyStatus(ctx, collection, version, "applied")

	// 9. Wait for confirmations from other replicas. Observational only — the
	// snapshot is already active, so a laggard delays a log line, not the cluster.
	if err := m.waitConfirmations(ctx, collection, version); err != nil {
		m.logger.Warn("manager: confirmations timed out", dlog.Err(err), dlog.String("collection", collection))
	}

	m.metrics.SyncCompleted(collection, time.Since(syncStart), len(content))

	m.logger.Info("manager: sync complete",
		dlog.String("collection", collection),
		dlog.String("version", version),
	)

	return nil
}

// applyStaged swaps a staged value into memory once the move is durable. config.Swap
// publishes before running hooks, so ver already live means only a hook failed.
func (m *Manager) applyStaged(reg registrable, staged stagedRef, ver config.Version) error {
	err := reg.commitStaged(staged)
	if err == nil || !reg.version().Equal(ver) {
		return err
	}

	m.logger.Warn("manager: on-change hook failed after the value was applied",
		dlog.Err(err),
		dlog.String("collection", reg.name()),
		dlog.String("version", ver.String()),
	)

	return nil
}

// repairCacheEntry writes the active snapshot to cache when the entry is missing. A
// wrong-version entry is left to loadFromStorage, which overrides the cache anyway.
func (m *Manager) repairCacheEntry(ctx context.Context, collection string) {
	if m.cache == nil || !m.cacheStrategy.WritesToCache() {
		return
	}

	if _, err := m.cache.Get(ctx, collection); err == nil {
		return
	} else if !errors.Is(err, cache.ErrCacheMiss) {
		m.logger.Warn("manager: cache repair read failed",
			dlog.Err(err), dlog.String("collection", collection))
		return
	}

	snap, err := m.storage.GetActiveSnapshot(ctx, collection)
	if err != nil {
		if !errors.Is(err, storage.ErrSnapshotNotFound) {
			m.logger.Warn("manager: cache repair load snapshot failed",
				dlog.Err(err), dlog.String("collection", collection))
		}
		return
	}

	m.cacheWrite(ctx, collection, snap.Version, snap.Content)
	m.logger.Info("manager: warmed cold cache",
		dlog.String("collection", collection),
		dlog.String("version", snap.Version),
	)
}

// warmCacheIfMissing warms cold cache entries from storage under the advisory lock,
// for ManualSyncOnly startup where leaderSync's version-skip repair never runs.
func (m *Manager) warmCacheIfMissing(ctx context.Context) {
	if m.cache == nil || !m.cacheStrategy.WritesToCache() {
		return
	}

	release, err := m.storage.AcquireLock(ctx, m.opts.AdvisoryLockKey)
	if err != nil {
		if !errors.Is(err, storage.ErrLockNotAcquired) {
			m.logger.Warn("manager: cache warm acquire lock failed", dlog.Err(err))
		}
		return
	}
	defer release()

	for _, reg := range m.configs {
		m.repairCacheEntry(ctx, reg.name())
	}
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

// publish stamps this instance's ID and broadcasts. Every publish site must go
// through here — the stamp is what lets handleEvent drop the echoed copy.
func (m *Manager) publish(ctx context.Context, event notify.Event) error {
	event.InstanceID = m.instanceID

	return m.notifier.Publish(ctx, event)
}

// handleEvent runs the follower path for an incoming notification, dropping our own
// (transports echo them) but not unstamped ones: docs/sync-protocol.md "Event Origin".
func (m *Manager) handleEvent(ctx context.Context, event notify.Event) {
	if event.InstanceID != "" && event.InstanceID == m.instanceID {
		m.logger.Debug("manager: dropping self-published event",
			dlog.String("action", event.Action),
			dlog.String("collection", event.Collection),
			dlog.String("version", event.Version),
			dlog.String("round_id", event.RoundID),
		)
		return
	}

	m.logger.Debug("manager: received notify event",
		dlog.String("action", event.Action),
		dlog.String("collection", event.Collection),
		dlog.String("version", event.Version),
		dlog.String("round_id", event.RoundID),
	)

	switch event.Action {
	case notify.ActionSync:
		m.handleSyncEvent(ctx, event)
	case notify.ActionRollback:
		m.handleRollbackEvent(ctx, event)
	case notify.ActionPrepare:
		if m.opts.RequireUnanimousApply {
			m.handlePrepareEvent(ctx, event)
		}
	case notify.ActionCommit:
		if m.opts.RequireUnanimousApply {
			m.handleCommitEvent(ctx, event)
		}
	case notify.ActionAbort:
		if m.opts.RequireUnanimousApply {
			m.handleAbortEvent(ctx, event)
		}
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

	if reg.version().Equal(version) {
		return
	}

	// Defence in depth: a leader cannot announce a regression, but this is the
	// path that would split the cluster if one ever escaped.
	if !m.allowForward(reg, version, pathSyncEvent) {
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
		status := "error"
		if errors.Is(err, ErrValidationFailed) {
			status = "validation_failed"
			// reg.swapFromBytes already deduped + logged via reportFailure.
		} else {
			m.logger.Error("manager: swap from snapshot",
				dlog.Err(err),
				dlog.String("collection", event.Collection),
				dlog.String("version", event.Version),
			)
		}
		m.logApplyStatus(ctx, event.Collection, event.Version, status)
		return
	}

	m.logApplyStatus(ctx, event.Collection, event.Version, "applied")
	m.metrics.FollowerApplied(event.Collection)

	m.logger.Info("manager: follower applied sync",
		dlog.String("collection", event.Collection),
		dlog.String("version", event.Version),
	)
}

// handleRollbackEvent applies the active snapshot in either direction — the only path
// that moves a replica backwards, hence the Warn. Procedure: docs/rollback-runbook.md.
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

	local := reg.version()

	if err := reg.swapFromBytes(version, snap.Content); err != nil {
		m.logger.Error("manager: rollback swap",
			dlog.Err(err),
			dlog.String("collection", event.Collection),
			dlog.String("version", snap.Version),
		)
		return
	}

	// The cache still names the version rolled back from, and repairCacheEntry needs
	// the lock the operator holds. Safe unlocked: every replica writes these bytes.
	m.cacheWrite(ctx, event.Collection, snap.Version, snap.Content)

	m.logApplyStatus(ctx, event.Collection, snap.Version, rollbackApplyStatus)

	m.logger.Warn("manager: operator rollback applied to the active snapshot",
		dlog.String("collection", event.Collection),
		dlog.String("from", local.String()),
		dlog.String("to", snap.Version),
		dlog.Bool("backwards", !version.After(local)),
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

		// Storage is canonical and overrides the cache in either direction; nothing
		// else repairs a cache naming a rolled-back-from version. No snapshot, no swap.
		if reg.version().Equal(version) {
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

// syncOneForced runs the leader sync protocol for one config, skipping the
// version check — the WebSocket event already confirmed a change.
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
		if errors.Is(err, ErrValidationFailed) {
			return // already logged via reportFailure dedup
		}
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

// Two-phase commit (2PC) statuses written to the apply log.
const (
	applyStatusPrepared      = "prepared"
	applyStatusPrepareFailed = "prepare_failed"
	applyStatusCommitted     = "committed"
)

// roundAbort describes why a 2PC round is being torn down. reason labels the
// metric and the log; dedupKind keys the per-version warn dedup.
type roundAbort struct {
	reason    string
	dedupKind string
	cause     error
}

// abortRound tears down a 2PC round: warn, broadcast abort, drop the staged value,
// fail the snapshot. Nothing advances, so the leader retries the same version.
func (m *Manager) abortRound(
	ctx context.Context,
	reg registrable,
	staged stagedRef,
	ver config.Version,
	roundID string,
	ab roundAbort,
) {
	collection, version := reg.name(), ver.String()

	fields := []dlog.Field{
		dlog.Err(ab.cause),
		dlog.String("collection", collection),
		dlog.String("version", version),
		dlog.String("round_id", roundID),
		dlog.String("reason", ab.reason),
	}
	// Per-version dedup says "this version is bad"; it must not silence "this
	// replica has never come up", which repeats until a round finally lands.
	if reg.version().IsZero() || reg.shouldReport(ver, ab.dedupKind) {
		m.logger.Warn("manager: 2PC aborting round", fields...)
	} else {
		m.logger.Debug("manager: 2PC aborting round (dedup)", fields...)
	}

	abortEvent := notify.Event{
		Action:     notify.ActionAbort,
		Collection: collection,
		Version:    version,
		RoundID:    roundID,
	}
	if pubErr := m.publish(ctx, abortEvent); pubErr != nil {
		m.logger.Error("manager: publish abort failed", dlog.Err(pubErr), dlog.String("round_id", roundID))
	}

	reg.abortStaged(staged)
	m.metrics.StagedDropped(collection, ab.reason)
	m.failSnapshotUnlessActive(ctx, collection, version)

	m.metrics.SyncFailed(collection, ab.cause)
}

// failSnapshotUnlessActive marks a snapshot failed unless it may be active — an
// activation can commit server-side yet error, and demoting it strands restarts.
func (m *Manager) failSnapshotUnlessActive(ctx context.Context, collection, version string) {
	active, err := m.getActiveVersion(ctx, collection)
	switch {
	case errors.Is(err, storage.ErrSnapshotNotFound):
		// No active snapshot to protect.
	case err != nil:
		m.logger.Warn("manager: leaving snapshot unfailed, active version unreadable",
			dlog.Err(err), dlog.String("collection", collection), dlog.String("version", version))
		return
	case active == version:
		m.logger.Warn("manager: activation reported an error but the version is active, leaving the snapshot in place",
			dlog.String("collection", collection), dlog.String("version", version))
		return
	}

	if failErr := m.storage.FailSnapshot(ctx, collection, version); failErr != nil {
		m.logger.Error("manager: fail snapshot after abort",
			dlog.Err(failErr), dlog.String("collection", collection), dlog.String("version", version))
	}
}

// leaderSync2PC runs strict two-phase commit for one config: either all alive
// replicas move or none do, and an aborted round is retried on the next cycle.
func (m *Manager) leaderSync2PC(ctx context.Context, reg registrable, force bool) error {
	collection := reg.name()
	syncStart := time.Now()

	// 1. Version check.
	updatedAt, err := reg.fetchVersion(ctx)
	if err != nil {
		return fmt.Errorf("fetch version: %w", err)
	}

	currentVersion := reg.version()
	newVersion := syncVersion(updatedAt, currentVersion, force)

	if !force && !currentVersion.IsZero() && newVersion.Equal(currentVersion) {
		m.repairCacheEntry(ctx, collection)
		m.logger.Debug("manager: no version change, skipping 2PC sync",
			dlog.String("collection", collection),
			dlog.String("version", currentVersion.String()),
		)
		return nil
	}

	if !m.allowLeaderAdvance(reg, newVersion) {
		return nil
	}

	roundID := uuid.NewString()
	version := newVersion.String()

	m.logger.Info("manager: 2PC round starting",
		dlog.String("collection", collection),
		dlog.String("old_version", currentVersion.String()),
		dlog.String("new_version", version),
		dlog.String("round_id", roundID),
		dlog.Bool("forced", force),
	)

	// 2. Fetch + stage locally (no swap yet).
	content, staged, err := reg.fetchAndStage(ctx, newVersion, roundID, m.opts.PrepareTTL)
	if err != nil {
		return fmt.Errorf("fetch and stage: %w", err)
	}

	// 3. Persist snapshot so followers can load it.
	if err := m.storage.SaveSnapshot(ctx, collection, version, content); err != nil {
		reg.abortStaged(staged)
		return fmt.Errorf("save snapshot: %w", err)
	}

	// 3a. Reset the apply log so statuses from a prior aborted round of the SAME
	// version cannot count towards this one. Safe under the advisory lock.
	if err := m.storage.ResetApplyLog(ctx, collection, version); err != nil {
		reg.abortStaged(staged)
		return fmt.Errorf("reset apply log: %w", err)
	}

	// 4. Snapshot target set of alive instances and log self-prepare.
	target, err := m.registry.AliveInstances(ctx, m.opts.ServiceName)
	if err != nil {
		reg.abortStaged(staged)
		return fmt.Errorf("alive instances: %w", err)
	}

	m.metrics.PreparePhaseStarted(collection, roundID)

	if err := m.storage.LogApply(ctx, m.instanceID, collection, version, applyStatusPrepared); err != nil {
		reg.abortStaged(staged)
		return fmt.Errorf("log self prepared: %w", err)
	}

	// 5. Publish prepare to followers.
	prepareEvent := notify.Event{
		Action:     notify.ActionPrepare,
		Collection: collection,
		Version:    version,
		RoundID:    roundID,
	}
	if err := m.publish(ctx, prepareEvent); err != nil {
		reg.abortStaged(staged)
		return fmt.Errorf("publish prepare: %w", err)
	}

	// 6. Wait for all targets to prepare, or abort.
	waitErr := m.waitPreparesOrAbort(ctx, collection, version, target)
	if waitErr != nil {
		reason := "timeout"
		if errors.Is(waitErr, errPrepareFailed) {
			reason = "prepare_failed"
		}

		m.metrics.PreparePhaseFailed(collection, roundID, reason)
		// Dedup on "round_aborted" rather than the reason, so a follower that
		// times out once and prepare_fails next still warns once per version.
		m.abortRound(ctx, reg, staged, newVersion, roundID, roundAbort{
			reason:    reason,
			dedupKind: "round_aborted",
			cause:     waitErr,
		})

		return fmt.Errorf("2PC prepare phase: %w", waitErr)
	}

	m.metrics.PreparePhaseSucceeded(collection, roundID)

	// 7. Activation IS the commit decision, so it must be durable before the decision
	// is announced: replicas reconcile against the active snapshot as the event lands.
	if err := m.storage.ActivateSnapshot(ctx, collection, version); err != nil {
		m.abortRound(ctx, reg, staged, newVersion, roundID, roundAbort{
			reason:    "activate_failed",
			dedupKind: "activate_failed",
			cause:     err,
		})

		return fmt.Errorf("activate snapshot: %w", err)
	}

	// 8. Announce the decision.
	commitEvent := notify.Event{
		Action:     notify.ActionCommit,
		Collection: collection,
		Version:    version,
		RoundID:    roundID,
	}
	if err := m.publish(ctx, commitEvent); err != nil {
		m.logger.Error("manager: publish commit failed — followers may lag until next sync",
			dlog.Err(err), dlog.String("collection", collection), dlog.String("round_id", roundID))
		// Commit locally anyway; the decision is durable and followers catch up.
	}

	// 9. Apply locally, now that the decision is durable and broadcast.
	if err := m.applyStaged(reg, staged, newVersion); err != nil {
		return fmt.Errorf("2PC local apply after commit: %w", err)
	}

	// 10. Record this instance's outcome.
	m.logApplyStatus(ctx, collection, version, applyStatusCommitted)

	// 11. Cache write AFTER commit so an aborted round never warms the cache.
	m.cacheWrite(ctx, collection, version, content)

	// 12. Fallback for a follower that missed the commit event: the snapshot is
	// active by now, so the eventually-consistent path can apply it too.
	fallbackEvent := notify.Event{
		Action:     notify.ActionSync,
		Collection: collection,
		Version:    version,
	}
	if pubErr := m.publish(ctx, fallbackEvent); pubErr != nil {
		m.logger.Warn("manager: 2PC fallback sync publish failed", dlog.Err(pubErr))
	}

	m.metrics.SyncCompleted(collection, time.Since(syncStart), len(content))
	m.logger.Info("manager: 2PC round committed",
		dlog.String("collection", collection),
		dlog.String("version", version),
		dlog.String("round_id", roundID),
	)

	return nil
}

// waitPreparesOrAbort polls the apply log until every target has logged "prepared",
// short-circuiting on a "prepare_failed". Targets that stop heartbeating are dropped.
func (m *Manager) waitPreparesOrAbort(ctx context.Context, collection, version string, target []string) error {
	if len(target) == 0 {
		return nil
	}

	targetSet := make(map[string]struct{}, len(target))
	for _, id := range target {
		targetSet[id] = struct{}{}
	}

	deadline := time.After(m.opts.WaitConfirmationsTimeout)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		// Check immediately without waiting for the first tick.
		done, err := m.checkPrepares(ctx, collection, version, targetSet)
		if err != nil {
			return err
		}
		if done {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("prepare phase timeout")
		case <-ticker.C:
			continue
		}
	}
}

// checkPrepares reports whether every still-alive target has logged "prepared",
// erroring with errPrepareFailed as soon as one logs "prepare_failed".
func (m *Manager) checkPrepares(ctx context.Context, collection, version string, targetSet map[string]struct{}) (bool, error) {
	alive, err := m.registry.AliveInstances(ctx, m.opts.ServiceName)
	if err != nil {
		return false, fmt.Errorf("alive instances during wait: %w", err)
	}
	aliveSet := make(map[string]struct{}, len(alive))
	for _, id := range alive {
		aliveSet[id] = struct{}{}
	}

	failed, err := m.storage.AppliedInstances(ctx, collection, version, applyStatusPrepareFailed)
	if err != nil {
		return false, fmt.Errorf("applied instances (failed): %w", err)
	}
	for _, id := range failed {
		if _, tgt := targetSet[id]; tgt {
			if _, live := aliveSet[id]; live {
				return false, fmt.Errorf("instance %s: %w", id, errPrepareFailed)
			}
		}
	}

	prepared, err := m.storage.AppliedInstances(ctx, collection, version, applyStatusPrepared)
	if err != nil {
		return false, fmt.Errorf("applied instances (prepared): %w", err)
	}
	preparedSet := make(map[string]struct{}, len(prepared))
	for _, id := range prepared {
		preparedSet[id] = struct{}{}
	}

	for id := range targetSet {
		if _, live := aliveSet[id]; !live {
			// Dropped from live during the round — exclude from effective target.
			continue
		}
		if _, ok := preparedSet[id]; !ok {
			return false, nil
		}
	}
	return true, nil
}

// handlePrepareEvent is the follower side of 2PC phase 1: load the snapshot,
// stage it locally, and log "prepared" or "prepare_failed".
func (m *Manager) handlePrepareEvent(ctx context.Context, event notify.Event) {
	reg, ok := m.configs[event.Collection]
	if !ok {
		// Acknowledge a collection this replica does not manage, or a rolling
		// deployment that adds one would block every round on the older pods.
		m.logApplyStatus(ctx, event.Collection, event.Version, applyStatusPrepared)
		return
	}

	version, err := config.ParseVersion(event.Version)
	if err != nil {
		m.logger.Error("manager: parse prepare version", dlog.Err(err), dlog.String("version", event.Version))
		m.logApplyStatus(ctx, event.Collection, event.Version, applyStatusPrepareFailed)
		m.metrics.FollowerPrepareFailed(event.Collection, err)
		return
	}

	snap, err := m.storage.GetSnapshot(ctx, event.Collection, event.Version)
	if err != nil {
		m.logger.Error("manager: get snapshot for prepare",
			dlog.Err(err),
			dlog.String("collection", event.Collection),
			dlog.String("version", event.Version),
		)
		m.logApplyStatus(ctx, event.Collection, event.Version, applyStatusPrepareFailed)
		m.metrics.FollowerPrepareFailed(event.Collection, err)
		return
	}

	if _, err := reg.stageFromBytes(version, event.RoundID, snap.Content, m.opts.PrepareTTL); err != nil {
		m.logger.Error("manager: stage snapshot failed",
			dlog.Err(err),
			dlog.String("collection", event.Collection),
			dlog.String("version", event.Version),
			dlog.String("round_id", event.RoundID),
		)
		m.logApplyStatus(ctx, event.Collection, event.Version, applyStatusPrepareFailed)
		m.metrics.FollowerPrepareFailed(event.Collection, err)
		return
	}

	m.logApplyStatus(ctx, event.Collection, event.Version, applyStatusPrepared)
	m.metrics.FollowerPrepared(event.Collection)

	m.logger.Info("manager: follower prepared",
		dlog.String("collection", event.Collection),
		dlog.String("version", event.Version),
		dlog.String("round_id", event.RoundID),
	)
}

// handleCommitEvent is the follower side of 2PC phase 2: swap the staged value live,
// falling back to a storage reload when it is gone (TTL expired).
func (m *Manager) handleCommitEvent(ctx context.Context, event notify.Event) {
	reg, ok := m.configs[event.Collection]
	if !ok {
		return
	}

	found, err := reg.commitByRoundID(event.RoundID)
	if err != nil {
		m.metrics.FollowerFailed(event.Collection, err)
		m.logger.Error("manager: commit staged failed",
			dlog.Err(err),
			dlog.String("collection", event.Collection),
			dlog.String("round_id", event.RoundID),
		)
		m.logApplyStatus(ctx, event.Collection, event.Version, "error")
		return
	}

	if !found {
		// This replica logged "prepared", so it must honour the commit even with
		// the staged value gone — reload the payload from storage.
		m.logger.Warn("manager: staged entry missing on commit, loading from storage",
			dlog.String("collection", event.Collection),
			dlog.String("round_id", event.RoundID),
		)

		version, perr := config.ParseVersion(event.Version)
		if perr != nil {
			m.logger.Error("manager: parse commit version", dlog.Err(perr), dlog.String("version", event.Version))
			return
		}

		snap, gerr := m.storage.GetSnapshot(ctx, event.Collection, event.Version)
		if gerr != nil {
			m.metrics.FollowerFailed(event.Collection, gerr)
			m.logger.Error("manager: get snapshot for commit fallback",
				dlog.Err(gerr),
				dlog.String("collection", event.Collection),
				dlog.String("version", event.Version),
			)
			m.logApplyStatus(ctx, event.Collection, event.Version, "error")
			return
		}

		if sErr := reg.swapFromBytes(version, snap.Content); sErr != nil {
			m.metrics.FollowerFailed(event.Collection, sErr)
			m.logger.Error("manager: swap from storage on commit fallback",
				dlog.Err(sErr),
				dlog.String("collection", event.Collection),
				dlog.String("version", event.Version),
			)
			m.logApplyStatus(ctx, event.Collection, event.Version, "error")
			return
		}
	}

	m.logApplyStatus(ctx, event.Collection, event.Version, applyStatusCommitted)
	m.metrics.FollowerApplied(event.Collection)

	m.logger.Info("manager: follower committed",
		dlog.String("collection", event.Collection),
		dlog.String("version", event.Version),
		dlog.String("round_id", event.RoundID),
	)
}

// handleAbortEvent drops the follower's staged snapshot without applying. No
// apply_log entry on purpose — the leader marks the snapshot failed itself.
func (m *Manager) handleAbortEvent(_ context.Context, event notify.Event) {
	reg, ok := m.configs[event.Collection]
	if !ok {
		return
	}

	reg.abortByRoundID(event.RoundID)
	m.metrics.StagedDropped(event.Collection, "abort")

	m.logger.Info("manager: follower aborted round",
		dlog.String("collection", event.Collection),
		dlog.String("version", event.Version),
		dlog.String("round_id", event.RoundID),
	)
}
