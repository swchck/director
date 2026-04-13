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
			if errors.Is(err, ErrValidationFailed) {
				// reg.fetchAndSwap / fetchAndStage already deduped + warn-logged
				// via reportFailure; suppress the generic "leader sync failed"
				// noise so polling on a chronically-bad version doesn't spam.
				continue
			}
			m.logger.Error("manager: leader sync failed", dlog.Err(err), dlog.String("collection", reg.name()))
		}
	}
}

// runMaintenance is invoked by the maintenance ticker. Only the leader
// (advisory-lock holder) actually performs deletions to avoid stampedes.
// Followers see ErrLockNotAcquired and do nothing.
func (m *Manager) runMaintenance(ctx context.Context) {
	if m.opts.SnapshotRetention <= 0 && m.opts.InstanceRetention <= 0 {
		return
	}

	release, err := m.storage.AcquireLock(ctx, m.opts.AdvisoryLockKey)
	if err != nil {
		if errors.Is(err, storage.ErrLockNotAcquired) {
			return // someone else is leader; they'll run maintenance.
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

// leaderSync performs the full leader sync protocol for a single config.
// When force is true (e.g. triggered by WebSocket), the version check is skipped
// and zero timestamps fall back to time.Now().
//
// Dispatches to leaderSync2PC when Options.RequireUnanimousApply is set.
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

// leaderSync2PC runs the strict two-phase-commit protocol for one config.
// Enabled via Options.RequireUnanimousApply. Either all alive replicas
// transition to the new version or none do; on any prepare failure or timeout
// the round aborts and the leader retries on the next poll/WS cycle.
func (m *Manager) leaderSync2PC(ctx context.Context, reg registrable, force bool) error {
	collection := reg.name()
	syncStart := time.Now()

	// 1. Version check.
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
		m.logger.Debug("manager: no version change, skipping 2PC sync",
			dlog.String("collection", collection),
			dlog.String("version", currentVersion.String()),
		)
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

	// 3a. Reset apply log for (collection, version) so stale statuses from a
	// prior aborted round of the SAME version don't leak into this round's
	// quorum check. Safe under the advisory lock (only one leader at a time).
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
	if err := m.notifier.Publish(ctx, prepareEvent); err != nil {
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
		// Dedup on bare "round_aborted" (not the reason) so a flaky follower
		// that times out one round and prepare_fails the next still produces
		// a single warn per (collection, version).
		if reg.shouldReport(newVersion, "round_aborted") {
			m.logger.Warn("manager: 2PC aborting round",
				dlog.Err(waitErr),
				dlog.String("collection", collection),
				dlog.String("version", version),
				dlog.String("round_id", roundID),
				dlog.String("reason", reason),
			)
		} else {
			m.logger.Debug("manager: 2PC aborting round (dedup)",
				dlog.String("collection", collection),
				dlog.String("version", version),
				dlog.String("round_id", roundID),
				dlog.String("reason", reason),
			)
		}

		abortEvent := notify.Event{
			Action:     notify.ActionAbort,
			Collection: collection,
			Version:    version,
			RoundID:    roundID,
		}
		if pubErr := m.notifier.Publish(ctx, abortEvent); pubErr != nil {
			m.logger.Error("manager: publish abort failed", dlog.Err(pubErr), dlog.String("round_id", roundID))
		}

		reg.abortStaged(staged)
		m.metrics.StagedDropped(collection, reason)

		if failErr := m.storage.FailSnapshot(ctx, collection, version); failErr != nil {
			m.logger.Error("manager: fail snapshot after abort",
				dlog.Err(failErr), dlog.String("collection", collection), dlog.String("version", version))
		}

		m.metrics.SyncFailed(collection, waitErr)
		return fmt.Errorf("2PC prepare phase: %w", waitErr)
	}

	m.metrics.PreparePhaseSucceeded(collection, roundID)

	// 7. Publish commit and apply locally.
	commitEvent := notify.Event{
		Action:     notify.ActionCommit,
		Collection: collection,
		Version:    version,
		RoundID:    roundID,
	}
	if err := m.notifier.Publish(ctx, commitEvent); err != nil {
		m.logger.Error("manager: publish commit failed — followers may lag until next sync",
			dlog.Err(err), dlog.String("collection", collection), dlog.String("round_id", roundID))
		// Commit locally anyway; state in storage is correct and followers will catch up.
	}

	if err := reg.commitStaged(staged); err != nil {
		return fmt.Errorf("commit staged: %w", err)
	}

	if err := m.storage.LogApply(ctx, m.instanceID, collection, version, applyStatusCommitted); err != nil {
		m.logger.Error("manager: log self committed", dlog.Err(err))
	}

	// 8. Cache write AFTER commit so an aborted round never warms the cache.
	m.cacheWrite(ctx, collection, version, content)

	// 9. Activate snapshot.
	if err := m.storage.ActivateSnapshot(ctx, collection, version); err != nil {
		return fmt.Errorf("activate snapshot: %w", err)
	}

	m.metrics.SyncCompleted(collection, time.Since(syncStart), len(content))
	m.logger.Info("manager: 2PC round committed",
		dlog.String("collection", collection),
		dlog.String("version", version),
		dlog.String("round_id", roundID),
	)

	return nil
}

// waitPreparesOrAbort polls the apply log until every instance in target has
// logged "prepared", short-circuiting with errPrepareFailed as soon as any
// target logs "prepare_failed". Targets that stop heartbeating during the
// wait are dropped (effectiveTarget = target ∩ alive) so a dead replica
// cannot block the round.
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

// checkPrepares returns done=true when every still-alive member of targetSet
// has logged "prepared". Returns errPrepareFailed as soon as any still-alive
// target logs "prepare_failed".
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

// handleCommitEvent is the follower side of 2PC phase 2: swap the staged value
// live. Falls back to reloading the snapshot from storage if the staged value
// is gone (e.g., TTL expired).
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
		// Staged entry missing — fall back to storage. We already logged
		// "prepared", so we must honor commit to preserve the invariant.
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

// handleAbortEvent drops the follower's staged snapshot without applying.
// Intentionally does NOT write an apply_log entry: an aborted round leaves
// no trace in the log (the leader marks the snapshot itself as failed).
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
