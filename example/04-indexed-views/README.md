# 04 — Indexed Views (GroupBy)

Demonstrates grouping collection items by a key into a `map[K][]V` that auto-updates.

**What you'll learn:**
- `IndexedView` — group items by a field: `map[string][]Product`
- `IndexedViewT` — group and transform: `map[string][]Level` from `[]Business`
- O(1) lookups by key after initial computation

```bash
go run example/04-indexed-views/main.go
```

No infrastructure needed — this example uses in-memory data only.
