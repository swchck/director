# 09 — Caching

Demonstrates caching strategies for config snapshots and view results.

**What you'll learn:**
- Cache strategies: ReadThrough, WriteThrough, WriteBehind, ReadWriteThrough
- `MemoryViewStore` for in-process view persistence
- `RedisViewStore` for cross-replica view sharing
- How caching integrates with the Manager startup sequence
- Views with persistence: warm start from cache

```bash
go run example/09-caching/main.go
```

No infrastructure needed — this example uses `MemoryViewStore` only.
For Redis caching, see the code comments.
