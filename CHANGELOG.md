# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- **Strict consistency mode (opt-in two-phase commit).** Set `manager.Options.RequireUnanimousApply = true` to guarantee every alive replica runs the same config version. The leader stages each update, publishes `prepare`, waits for every alive replica to confirm, and only then publishes `commit`. A single `prepare_failed` or prepare timeout aborts the round and the leader retries on the next poll/WS cycle. See `docs/sync-protocol.md` for the full protocol and operational notes.
- `manager.Options.PrepareTTL` — bounds how long followers hold a staged snapshot before dropping it (default: `2 × WaitConfirmationsTimeout`).
- `registry.Registry.AliveInstances(ctx, service) []string` — returns instance IDs (used by 2PC target tracking so stale-heartbeat deaths drop out instead of blocking the round).
- `storage.Storage.AppliedInstances(ctx, collection, version, status) []string` — returns instance IDs with the given apply-log status.
- `storage.Storage.ResetApplyLog(ctx, collection, version)` — deletes apply-log rows for `(collection, version)`. Called at the start of each 2PC round so stale statuses from a prior aborted round of the same version don't leak into the new round's quorum check.
- `notify.Event.RoundID` (omitempty) and action constants `ActionSync`/`ActionRollback`/`ActionPrepare`/`ActionCommit`/`ActionAbort`.
- `Metrics`: `PreparePhaseStarted`/`Succeeded`/`Failed`, `FollowerPrepared`/`FollowerPrepareFailed`, `StagedDropped` for 2PC observability.
- `OnChange` on all types now returns an unsubscribe function (`func()`) to remove hooks
- `WithPersistenceTimeout` option for `View`, `SingletonView`, `IndexedView`, `IndexedViewT` (default: 10s)
- `WithSingletonViewErrorHandler` option for error callbacks on `SingletonView` persistence failures
- `WithSingletonViewPersistenceTimeout` option for `SingletonView`
- `Metrics` interface in `manager` package for observability (sync counts, latency, cache hits, follower events, WS events)
- `WithMetrics(m Metrics)` manager option; `NopMetrics()` no-op default
- Benchmark tests for `Collection`, `View`, `IndexedView` (100 to 100K items, parallel reads)
- `CONTRIBUTING.md` with development setup and PR guidelines
- `SECURITY.md` with vulnerability reporting policy
- Runtime guard: `manager.register()` panics if called after `Start()`

### Fixed
- `safeCallHooks` now runs **all** hooks even if earlier hooks panic, collecting errors via `errors.Join`
- `View.recompute` hook panics are now recovered (previously could crash if a view hook panicked)
- `RelatedView.OnChange` is now protected by `sync.RWMutex` (was a data race if called concurrently with `Swap`)
- `CompositeView.Count()` no longer allocates merged slice when no dedup function is set
- `log.Err()` now preserves the original `error` value (supports `errors.Is`/`errors.As` in custom loggers)
- README: corrected WebSocket dependency from `nhooyr.io/websocket` to `github.com/coder/websocket`

### Changed
- `leaderSync` and `leaderSyncForced` consolidated into single `leaderSync(ctx, reg, force)` method
- Cache write logic extracted into `cacheWrite` helper to eliminate duplication
- Persistence goroutines now use `context.WithTimeout` (default 10s) instead of `context.Background()`
- Persistence goroutines bounded by semaphore (max 2 concurrent per view); excess saves are dropped (next Swap produces a fresher save)

### Migration Notes
- `OnChange` now returns `func()`. Existing code that ignores the return value compiles without changes.
- `log.Err()` now stores `error` (not `string`) in `Field.Value`. Custom `Logger` implementations that type-switch on `Field.Value` and expect `string` for the "error" key should add an `error` case.
- `registry.Registry` gained `AliveInstances`. `storage.Storage` gained `AppliedInstances` and `ResetApplyLog`. Code that implements these interfaces directly must add them (the built-in `registry/postgres` and `storage/postgres` implementations do so already). The default `RequireUnanimousApply = false` preserves all existing behavior.
