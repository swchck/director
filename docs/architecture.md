# Architecture Overview

`director` is a Go library that syncs data from any CMS/API into your application's memory, keeps it up-to-date across multiple replicas, and provides fast, type-safe querying. Ships with Directus adapters but is source-agnostic via the `source/` package interfaces.

## High-Level Flow

```
+-----------+     poll / WS      +---------------+
| Directus  | <---------------- |   Manager     |
|  (CMS)    | ----------------> |  (leader)     |
+-----------+    fetch items     +------+--------+
                                       |
                        +--------------+--------------+
                        v              v              v
                  +-----------+  +-----------+  +-----------+
                  | Postgres  |  |  Redis    |  |  Notify   |
                  | snapshot  |  |  cache    |  | channel   |
                  +-----------+  +-----------+  +-----+-----+
                                                      |
                                       +--------------+--------------+
                                       v              v              v
                                 +-----------+  +-----------+  +-----------+
                                 | Replica   |  | Replica   |  | Replica   |
                                 |(follower) |  |(follower) |  |(follower) |
                                 +-----------+  +-----------+  +-----------+
```

## Package Map

| Package | Purpose |
|---|---|
| `source/` | **Data source interfaces** -- `CollectionSource[T]`, `SingletonSource[T]` |
| `directus/` | Directus adapters -- HTTP + WebSocket client, schema, ACL, flows, system endpoints |
| `config/` | In-memory stores (`Collection[T]`, `Singleton[T]`), views, indexes, translations |
| `manager/` | Sync orchestrator -- poll, WS, leader/follower, debounce, heartbeat |
| `storage/` | Storage interfaces; `storage/postgres/` for Postgres snapshot persistence + advisory locks |
| `notify/` | Notification interfaces; `notify/postgres/` for LISTEN/NOTIFY, `notify/redis/` for Pub/Sub |
| `registry/` | Registry interfaces; `registry/postgres/` for instance registry with heartbeat |
| `cache/` | Cache interfaces; `cache/redis/` for Redis caching, `cache/memory/` for in-memory view store |
| `log/` | Logger interface + slog adapter |

## Package Dependency Graph

```
example/  cmd/
   |       |
   v       v
 manager ----------+
   |   |   |   |   |
   v   v   v   v   v
config directus storage notify registry cache
   |
   v
 source
```

No circular dependencies. Each package can be used independently.

## Data Flow on Sync

### Poll-Based (default)

1. **Manager** polls the source for `LastModified()` per collection
2. If version changed -> fetches all items via `source.CollectionSource[T].List()`
3. Calls `config.Collection[T].Swap()` -> atomic pointer swap -> fires OnChange hooks
4. OnChange hooks trigger `View[T].recompute()` -> views auto-update
5. Snapshot saved to `storage` (Postgres)
6. Notification published via `notify` channel
7. Other replicas receive notification -> load snapshot from storage -> Swap locally

### WebSocket-Based (optional, lower latency)

1. Directus WebSocket delivers `ChangeEvent` (create/update/delete) with item data
2. Manager receives event -> identifies collection via UID mapping
3. Events are **debounced** (default 2s window) to batch rapid changes
4. Triggers **forced sync** (skips version comparison -- WS already confirmed a change)
5. Same fetch -> swap -> snapshot -> notify flow as poll-based
6. Polling continues as safety net at a longer interval (`WSPollInterval`, default 15m)

## Startup Sequence

```
1. Register instance in registry
2. Load from cache (Redis/memory -- fastest, may be stale)
3. Load from storage (Postgres active snapshot)
4. Initial sync from source (source of truth)
5. Subscribe to notifications + WebSocket
6. Enter event loop: poll + heartbeat + notification + WS listener
```

Replicas can serve requests after step 2 or 3, before the source is contacted.

## Consistency Modes

Two protocols are available, selected per manager via `Options.RequireUnanimousApply`:

- **Eventually consistent (default).** Leader applies new version locally, notifies followers, moves on. Broken followers don't block the leader. Replicas may briefly run on different versions. Best throughput and availability.
- **Strict (2PC).** `RequireUnanimousApply = true`. Leader stages the new version, publishes `prepare`, waits until every alive replica confirms; only then publishes `commit`. A single failed `prepare` aborts the round for everyone. Guarantees all alive replicas run the same version (with a brief skew window during commit propagation) at the cost of liveness when any replica is broken. See [sync-protocol.md](./sync-protocol.md#two-phase-commit-mode-strict-consistency).

## View Update Chain

```
Collection.Swap(v2, newItems)
    +-- atomic pointer swap (data committed)
    +-- fire OnChange hooks
            +-- View.recompute() -> filter + sort -> atomic swap
            |       +-- async: persist to Redis/memory (if configured)
            |       +-- fire View.OnChange hooks
            +-- IndexedView.recompute() -> group by key -> atomic swap
            |       +-- async: persist to Redis/memory (if configured)
            |       +-- fire IndexedView.OnChange hooks
            +-- IndexedViewT.recompute() -> group + transform -> atomic swap
            |       +-- async: persist (if configured)
            +-- RelatedView.recompute() -> extract + flatten -> atomic swap
            +-- TranslatedView -> derived Collection.Swap -> View update
            +-- SingletonView.recompute() -> transform -> atomic swap
                    +-- async: persist (if configured)
```

All updates are synchronous within the Swap call. By the time Swap returns,
every derived view is already up-to-date. Persistence writes happen
asynchronously after the swap -- they never delay data availability.

## View Type Hierarchy

```
                    Collection[T]
                         |
          +--------------+--------------+---------+
          |              |              |         |
     View[T]    IndexedView[T,K]  IndexedViewT  RelatedView
          |              |         [T,K,V]
          |              |
   CompositeView[T]    (merged)
   (merges Views)

                    Singleton[T]
                         |
                 SingletonView[T,R]

          TranslatedView (Collection -> Collection via transform)
```

All view types share:
- Lock-free reads via `atomic.Pointer`
- Synchronous recomputation on source change
- Optional async persistence (`ViewPersistence` interface)
