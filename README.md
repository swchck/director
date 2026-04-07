# Director

Source-agnostic Go library for syncing CMS/API data into your application's memory, keeping it up-to-date across multiple replicas, and providing fast, type-safe querying. Ships with [Directus](https://directus.io) adapters but works with any backend.

## Features

- **Source-agnostic** — implement two methods (`List` + `LastModified`) to sync from any backend
- **Generic typed configs** — `Collection[T]` and `Singleton[T]` with lock-free reads via `atomic.Pointer`
- **Auto-sync** — poll-based and real-time WebSocket change detection with debouncing
- **Multi-replica coordination** — leader election via Postgres advisory locks, follower sync via notifications
- **Materialized views** — precomputed filtered/sorted/grouped views that auto-update
- **Relational support** — M2O, O2M, M2M, translations via deep queries and `RelatedView`
- **Flexible caching** — in-memory, Redis snapshot cache, Redis view persistence
- **Directus batteries included** — schema, ACL, flows, folders management
- **No panics** — all user-callback panics are recovered and returned as errors

## Quick Start

```go
// 1. Create a Directus client.
dc := directus.NewClient("https://directus.example.com", "your-token")

// 2. Define your config types.
type Product struct {
    ID       int    `json:"id"`
    Name     string `json:"name"`
    Category string `json:"category"`
    Price    float64 `json:"price"`
}

// 3. Create in-memory configs.
products := config.NewCollection[Product]("products")

// 4. Wire up the manager.
mgr := manager.New(store, notifier, registry, manager.Options{
    PollInterval: 5 * time.Minute,
    ServiceName:  "my-service",
})
manager.RegisterCollection(mgr, products, directus.NewItems[Product](dc, "products"))
go mgr.Start(ctx)

// 5. Query in your service code — lock-free, zero allocations for reads.
product, ok := products.Find(func(p Product) bool { return p.ID == 42 })
expensive := products.Filter(
    config.Where(func(p Product) bool { return p.Price > 100 }),
    config.SortBy(func(a, b Product) int { return cmp.Compare(a.Price, b.Price) }),
    config.Limit[Product](10),
)
```

## Custom Data Source

The library works with any backend — not just Directus. Implement two methods:

```go
type MyAPI struct { baseURL string }

func (a *MyAPI) List(ctx context.Context) ([]Product, error) {
    // Fetch from your API, database, file, etc.
}

func (a *MyAPI) LastModified(ctx context.Context) (time.Time, error) {
    // Return latest change timestamp, or time.Time{} to always refetch.
}

// Register with the manager — everything else works the same.
manager.RegisterCollectionSource(mgr, products, &MyAPI{baseURL: "https://my-api.com"})
```

All views, caching, sync protocol, and multi-replica coordination work identically regardless of the source.

## Package Structure

```
director/
├── source/         # Data source interfaces (CollectionSource, SingletonSource)
├── directus/       # Directus HTTP client, schema, ACL, flows, WebSocket
├── config/         # Generic in-memory config stores, views, translations
├── manager/        # Sync orchestrator (poll, WS, leader/follower, debounce)
├── storage/        # Storage interfaces + Postgres implementation (storage/postgres/)
├── notify/         # Notification interfaces + implementations (notify/postgres/, notify/redis/)
├── registry/       # Registry interfaces + Postgres implementation (registry/postgres/)
├── cache/          # Cache interfaces + implementations (cache/redis/, cache/memory/)
├── example/        # Usage examples (9 focused topics)
├── e2e/            # End-to-end tests against real Directus
└── docs/           # Detailed documentation
```

## Sync Protocol

```
Directus ──(poll/WS)──► Leader ──(snapshot)──► Postgres
                            │                      │
                            ├──(notify)──► Follower A ──(load snapshot)
                            └──(notify)──► Follower B ──(load snapshot)
```

1. **Leader** (holds Postgres advisory lock) polls Directus or reacts to WebSocket events
2. On change: fetches all items, swaps in-memory config, saves snapshot to Postgres
3. Publishes notification to all replicas
4. **Followers** load the snapshot from Postgres and swap locally
5. All replicas have identical data — views auto-recompute

See [docs/sync-protocol.md](docs/sync-protocol.md) for the full protocol spec.

## Debouncing

WebSocket events are debounced to prevent mass rebuilds during bulk operations (e.g. importing 100 items). Events for the same collection within the debounce window (default 2s) are batched into a single sync:

```go
manager.Options{
    WSDebounce: 2 * time.Second,  // default: batch events within 2s window
    WSDebounce: 5 * time.Second,  // longer window for heavy bulk imports
    WSDebounce: -1,               // disable debouncing (sync immediately)
}
```

Each collection is independent — a change in collection A never triggers a rebuild of collection B.

## Views

Views are precomputed, auto-updating projections of your collections:

```go
// Filtered + sorted view — recomputes when source changes.
foodByPrice := config.NewView("food-by-price", products,
    []config.FilterOption[Product]{
        config.Where(func(p Product) bool { return p.Category == "food" }),
        config.SortBy(func(a, b Product) int { return cmp.Compare(a.Price, b.Price) }),
    },
)

// M2M related view — flattened from parent.
allTags := config.NewRelatedView("product-tags", products,
    func(p Product) []Tag { return p.Tags },
    config.WithDedup[Product, Tag](func(a, b Tag) bool { return a.ID == b.ID }),
)

// Translated view — per-language flattening.
enProducts := config.NewTranslatedView("products-en", products,
    func(p Product) LocalizedProduct { /* flatten translations */ },
)
```

Views always cache in memory (lock-free reads). Optionally persist to Redis or in-memory store:

```go
// Redis persistence — share across replicas.
config.WithPersistence[Product](rediscache.NewViewStore(redisClient))

// In-memory persistence — warm start within same process.
config.WithPersistence[Product](memcache.NewViewStore())
```

## WebSocket Real-Time Sync

```go
ws := directus.NewWSClient("https://directus.example.com", "token")

mgr := manager.New(store, notifier, reg, opts,
    manager.WithWebSocket(ws), // instant sync on changes
)
```

When a Directus item changes, the WebSocket delivers the event immediately. The manager triggers a forced sync (skipping version comparison) so your config updates in real-time. Polling continues as a safety net at a longer interval.

## Schema Management

Create Directus collections, fields, and relations programmatically:

```go
// Create collection with M2O relation.
dc.CreateCollection(ctx, directus.CreateCollectionInput{
    Collection: "products",
    Fields: []directus.FieldInput{
        directus.PrimaryKeyField("id"),
        directus.DateUpdatedField(),
        {Field: "name", Type: directus.FieldTypeString},
        {Field: "category_id", Type: directus.FieldTypeInteger},
    },
})
dc.CreateRelation(ctx, directus.M2O("products", "category_id", "categories"))

// M2M with junction.
source, target := directus.M2M(directus.M2MInput{
    Collection: "products", Related: "tags",
    JunctionCollection: "products_tags",
    JunctionSourceField: "products_id", JunctionTargetField: "tags_id",
    AliasField: "tags",
})
dc.CreateRelation(ctx, source)
dc.CreateRelation(ctx, target)
```

## Flows (Automation)

Define Directus automation flows as code:

```go
flow, _ := dc.CreateFlow(ctx, directus.NewHookFlow("On Product Create",
    directus.HookFlowOptions{
        Type:        "action",
        Scope:       []string{"items.create"},
        Collections: []string{"products"},
    },
))

op, _ := dc.CreateOperation(ctx, directus.NewLogOperation("log_it", "Product created!"))
dc.UpdateFlow(ctx, flow.ID, directus.Flow{Operation: &op.ID})
```

## Running Tests

```bash
# Unit tests
task test

# E2E tests (requires Docker)
task e2e          # full cycle: start services, run tests, stop
# or manually:
task up           # start Directus + Postgres + Redis
task test:e2e     # run e2e tests
task down         # stop services
```

## Dependencies

| Package                        | Purpose                           |
| ------------------------------ | --------------------------------- |
| `github.com/jackc/pgx/v5`      | Postgres driver                   |
| `github.com/redis/go-redis/v9` | Redis client                      |
| `log/slog` (stdlib)            | Structured logging (slog adapter) |
| `github.com/coder/websocket`   | WebSocket client                  |
| `github.com/google/uuid`       | Instance ID generation            |

## Documentation

- [Architecture Overview](docs/architecture.md)
- [Sync Protocol](docs/sync-protocol.md)
- [directus/ package](docs/directus-package.md)
- [config/ package](docs/config-package.md)
- [cache/ package](docs/cache-package.md)
- [manager/ package](docs/manager-package.md)
- [Infrastructure packages](docs/infrastructure-packages.md)
