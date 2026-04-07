# Infrastructure Packages

## `storage/` — Postgres Persistence

Stores config snapshots and apply logs for the sync protocol.

### Tables

| Table | Purpose |
|---|---|
| `config_snapshots` | Versioned JSON snapshots per collection (pending → active → inactive/failed) |
| `config_apply_log` | Records which instance applied which version (for confirmation counting) |
| `config_instances` | Instance registry with heartbeats |

### Advisory Locks

`AcquireLock` uses `pg_try_advisory_lock` (non-blocking) at the session level. The returned `release` function calls `pg_advisory_unlock` and releases the connection back to the pool.

If the process crashes, Postgres automatically releases session-level advisory locks.

### Snapshot Lifecycle

```
pending → active   (ActivateSnapshot: on full confirmation)
pending → failed   (FailSnapshot: on timeout/error)
active  → inactive (ActivateSnapshot: old active demoted)
```

### Usage

```go
import pgstorage "github.com/swchck/director/storage/postgres"

pool, _ := pgxpool.New(ctx, "postgres://...")
store := pgstorage.NewStorage(pool)
store.Migrate(ctx) // creates tables if not exist
```

Note: `Migrate()` is available on the concrete `*pgstorage.Storage` type, not on the `storage.Storage` interface.

---

## `notify/` — Cross-Replica Notifications

Delivers sync/rollback events between replicas. Two implementations:

### Postgres LISTEN/NOTIFY (`postgres.go`)

Uses a dedicated connection for `LISTEN`. Messages are JSON payloads sent via `pg_notify()`.

```go
import pgnotify "github.com/swchck/director/notify/postgres"

notifier := pgnotify.NewChannel(pgPool,
    pgnotify.WithChannel("config_sync"),   // default
    pgnotify.WithLogger(logger),
)
```

**Pros**: No additional infrastructure. Uses the same Postgres you already have.
**Cons**: Requires a dedicated connection per listener. Notifications are lost if no one is listening.

### Redis Pub/Sub (`redis.go`)

Uses `redis.UniversalClient` (works with standalone, cluster, and sentinel).

```go
import redisnotify "github.com/swchck/director/notify/redis"

notifier := redisnotify.NewChannel(redisClient,
    redisnotify.WithChannel("config_sync"),
    redisnotify.WithLogger(logger),
)
```

**Pros**: Better throughput. Works across databases.
**Cons**: Requires Redis infrastructure.

### Event Format

```json
{
    "action": "sync",
    "collection": "businesses",
    "version": "2025-01-02T10:30:00Z"
}
```

Actions: `"sync"` (new version available) or `"rollback"` (revert to active snapshot).

### Who sends notifications?

**Only the leader sends notifications.** The leader is the replica that successfully acquires the Postgres advisory lock during a poll cycle. After fetching from Directus and saving a snapshot, the leader publishes a `"sync"` event. Followers receive this event and load the snapshot from Postgres storage.

---

## `registry/` — Instance Registry

Tracks live service instances so the leader knows how many confirmations to wait for.

### How it works

1. On startup: `Register(instanceID, serviceName)` — upserts into `config_instances`
2. Every 10s: `Heartbeat(instanceID)` — updates `last_heartbeat`
3. On shutdown: `Deregister(instanceID)` — deletes the row
4. Leader calls `AliveCount(serviceName)` — counts instances with `last_heartbeat > NOW() - threshold`

### Stale threshold

Default: 30 seconds. If a replica misses 3 heartbeats (10s interval), it's considered dead and excluded from confirmation counts.

```go
import pgregistry "github.com/swchck/director/registry/postgres"

reg := pgregistry.NewRegistry(pgPool,
    pgregistry.WithStaleThreshold(30 * time.Second),
)
```
