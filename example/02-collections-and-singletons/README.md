# 02 — Collections and Singletons

Demonstrates using the Manager to sync Directus data into in-memory `Collection[T]` and `Singleton[T]` stores with lock-free reads.

**What you'll learn:**
- Creating Collection and Singleton configs
- Registering them with the Manager
- Querying data: `All()`, `Find()`, `FindMany()`, `Filter()`
- OnChange hooks

```bash
task up
go run example/02-collections-and-singletons/main.go
```
