# Package: `cache/`

Optional caching layer with two distinct purposes:

1. **Snapshot cache** (`cache.go`, `redis.go`) -- caches raw collection data for fast cold starts
2. **View store** (`view_store.go`, `memory.go`) -- persists precomputed View results for cross-replica sharing

## Cache Interface

```go
type Cache interface {
    Get(ctx context.Context, collection string) (*Entry, error)
    Set(ctx context.Context, entry Entry) error
    Delete(ctx context.Context, collection string) error
    Close() error
}
```

Sentinel errors: `ErrCacheMiss` (key not found or expired), `ErrClosed`.

## Snapshot Cache

Used by the Manager to cache full collection snapshots in Redis.

### Strategies

| Strategy | Reads | Writes | Async | Best for |
|---|---|---|---|---|
| `ReadThrough` | yes | no | - | Fast cold starts, Directus fallback |
| `WriteThrough` | no | yes | no | Guaranteed cache freshness |
| `WriteBehind` | no | yes | yes | Lower sync latency |
| `ReadWriteThrough` | yes | yes | no | Production default |

```go
import rediscache "github.com/swchck/director/cache/redis"

redisCache := rediscache.NewCache(redisClient,
    rediscache.WithTTL(15 * time.Minute),   // default: 10 minutes
    rediscache.WithKeyPrefix("myapp:config:"),
)

mgr := manager.New(store, notifier, reg, opts,
    manager.WithCache(redisCache, cache.ReadWriteThrough),
)
```

### How it integrates with the Manager

**On startup** (if `ReadsFromCache()`):
```
1. For each collection: Redis GET -> unmarshal -> Config.Swap()
2. Then load from Postgres storage (if newer)
3. Then sync from Directus (source of truth)
```

**On sync** (if `WritesToCache()`):
```
After fetching from Directus:
  - WriteThrough: Redis SET (blocking, before sync is "complete")
  - WriteBehind:  go Redis SET (async, fire-and-forget)
```

### Key format

Default: `director:{collection_name}`

Configurable via `WithKeyPrefix`.

## ViewPersistence Interface

Defined in `config/view.go`, implemented in the cache package:

```go
type ViewPersistence interface {
    Save(ctx context.Context, key string, data []byte) error
    Load(ctx context.Context, key string) ([]byte, error)
}
```

This interface decouples the config package from any specific storage backend. Views (View, IndexedView, IndexedViewT, SingletonView) use this interface for optional persistence.

## RedisViewStore (`view_store.go`)

Implements `config.ViewPersistence` using Redis. Allows View results to be shared across replicas.

```go
import rediscache "github.com/swchck/director/cache/redis"

viewStore := rediscache.NewViewStore(redisClient,
    rediscache.WithViewTTL(10 * time.Minute),
    rediscache.WithViewKeyPrefix("myapp:views:"),
)

view := config.NewView("food-sorted", businesses, filters,
    config.WithPersistence[Business](viewStore),
)
```

### How it works

- When a View recomputes (triggered by Collection.Swap), it saves the filtered/sorted result to Redis **asynchronously**
- On View creation, it attempts to load from Redis for a warm start
- Other replicas with the same view name can read the precomputed result without re-running the filter pipeline

### Key format

Default: `director:view:{view_name}`

### When to use View persistence

- Multiple replicas with expensive filter/sort operations
- You want views to be "warm" immediately on startup
- You want to share computed views between different services

### When NOT to use it

- Single replica
- Cheap filters (simple predicate checks)
- Views that change every sync cycle anyway

## MemoryViewStore (`memory.go`)

Implements `config.ViewPersistence` using an in-memory map with `sync.RWMutex`.

```go
import memcache "github.com/swchck/director/cache/memory"

memStore := memcache.NewViewStore()

view := config.NewView("food-sorted", businesses, filters,
    config.WithPersistence[Business](memStore),
)
```

### When to use it

- Testing (no Redis needed)
- Single-replica deployments where views should survive recreation within the same process
- Sharing precomputed results between multiple View instances in the same process

### Implementation details

- Thread-safe via `sync.RWMutex`
- Stores copies of byte slices (no shared references)
- Returns nil data and nil error for missing keys (consistent with Redis behavior)

### Limitation

Data does NOT survive process restarts. Use `RedisViewStore` for that.

## View Persistence Comparison

| Store | Survives restart | Cross-replica | Requires |
|---|---|---|---|
| None (default) | no | no | nothing |
| `MemoryViewStore` | no | no | nothing |
| `RedisViewStore` | yes | yes | Redis |

All three always have lock-free in-memory reads. Persistence only affects save/load of precomputed results.

## Supported View Types

All of these accept persistence options:

| View Type | Persistence Option | Error Handler Option |
|---|---|---|
| `View[T]` | `config.WithPersistence[T](store)` | `config.WithErrorHandler[T](fn)` |
| `IndexedView[T, K]` | `config.WithIndexPersistence[T, K](store)` | `config.WithIndexErrorHandler[T, K](fn)` |
| `IndexedViewT[T, K, V]` | `config.WithIndexTPersistence[T, K, V](store)` | `config.WithIndexTErrorHandler[T, K, V](fn)` |
| `SingletonView[T, R]` | `config.WithSingletonViewPersistence[T, R](store)` | -- |
