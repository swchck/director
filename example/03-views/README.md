# 03 — Views

Demonstrates auto-updating materialized views: filtered, sorted subsets of a collection that recompute when the source changes.

**What you'll learn:**
- Creating views with `Where`, `SortBy`, `Limit`
- Views auto-update when the source collection syncs
- Querying views: `All()`, `Find()`, `Count()`
- View OnChange hooks

```bash
task up
go run example/03-views/main.go
```
