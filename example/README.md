# Examples

Each example is a standalone Go program demonstrating one topic.
Run any example with `go run example/<name>/main.go`.

**Prerequisites:** `task up` to start Directus, Postgres, and Redis.

| # | Directory | Topic |
|---|---|---|
| 01 | `01-basic-crud` | Read and write items via the Directus client |
| 02 | `02-collections-and-singletons` | In-memory Collection[T] and Singleton[T] with the manager |
| 03 | `03-views` | Filtered, sorted, auto-updating views |
| 04 | `04-indexed-views` | GroupBy views: map[K][]V from a collection |
| 05 | `05-sync-polling` | Multi-replica sync via polling |
| 06 | `06-sync-websocket` | Real-time sync via Directus WebSocket |
| 07 | `07-schema-as-code` | Create collections, relations, flows programmatically |
| 08 | `08-translations` | Multi-language content with translations |
| 09 | `09-caching` | Redis and in-memory caching strategies |
| 10 | `10-full-setup` | Production-style setup with config units and views |

Each directory has its own `README.md` explaining what it demonstrates.
